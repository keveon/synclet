package writer

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/keveon/synclet/internal/config"
)

func TestBuildMySQLUpsert(t *testing.T) {
	cfg := config.WriterConfig{
		Table:         "orders",
		KeyColumns:    []string{"customer_code", "ordered_at"},
		UpdateColumns: []string{"shipping_weight", "extra"},
	}
	sqlText, columns, err := BuildMySQLUpsert(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if sqlText != "INSERT INTO `orders` (`customer_code`, `ordered_at`, `shipping_weight`, `extra`) VALUES (?, ?, ?, ?) ON DUPLICATE KEY UPDATE `shipping_weight` = VALUES(`shipping_weight`), `extra` = VALUES(`extra`)" {
		t.Errorf("unexpected upsert: %s", sqlText)
	}
	if len(columns) != 4 {
		t.Errorf("columns = %v", columns)
	}
}

func TestBuildMySQLUpsertPolicies(t *testing.T) {
	cfg := config.WriterConfig{
		Table:                 "orders",
		KeyColumns:            []string{"k"},
		UpdateColumns:         []string{"v", "extra"},
		NullUpdatePolicy:      "keep_existing",
		JSONMergePatchColumns: []string{"extra"},
	}
	sqlText, _, err := BuildMySQLUpsert(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(sqlText, "`v` = COALESCE(VALUES(`v`), `orders`.`v`)") {
		t.Errorf("keep_existing clause missing: %s", sqlText)
	}
	if !contains(sqlText, "`extra` = COALESCE(JSON_MERGE_PATCH(`orders`.`extra`, VALUES(`extra`)), VALUES(`extra`))") {
		t.Errorf("json merge clause missing: %s", sqlText)
	}
}

func TestBuildPostgresUpsert(t *testing.T) {
	cfg := config.WriterConfig{
		Table:         "orders",
		KeyColumns:    []string{"customer_code", "ordered_at"},
		UpdateColumns: []string{"shipping_weight"},
	}
	upsert, selectSQL, columns, err := BuildPostgresUpsertForTest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(upsert, `INSERT INTO "orders" ("customer_code", "ordered_at", "shipping_weight")`) {
		t.Errorf("unexpected insert: %s", upsert)
	}
	if !contains(upsert, `ON CONFLICT ("customer_code", "ordered_at") DO UPDATE SET "shipping_weight" = EXCLUDED."shipping_weight"`) {
		t.Errorf("conflict clause missing: %s", upsert)
	}
	if !contains(upsert, "RETURNING (xmax = 0)") {
		t.Errorf("xmax classification missing: %s", upsert)
	}
	if selectSQL != "" {
		t.Errorf("no merge columns -> no select: %s", selectSQL)
	}
	if len(columns) != 3 {
		t.Errorf("columns = %v", columns)
	}
}

func TestBuildPostgresKeepExisting(t *testing.T) {
	cfg := config.WriterConfig{
		Table:            "orders",
		KeyColumns:       []string{"k"},
		UpdateColumns:    []string{"v"},
		NullUpdatePolicy: "keep_existing",
	}
	upsert, _, _, err := BuildPostgresUpsertForTest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(upsert, `"v" = COALESCE(EXCLUDED."v", "orders"."v")`) {
		t.Errorf("keep_existing clause missing: %s", upsert)
	}
}

func TestBuildPostgresMergeSelect(t *testing.T) {
	cfg := config.WriterConfig{
		Table:                 "orders",
		KeyColumns:            []string{"k"},
		UpdateColumns:         []string{"v", "extra"},
		JSONMergePatchColumns: []string{"extra"},
	}
	_, selectSQL, _, err := BuildPostgresUpsertForTest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(selectSQL, `SELECT "extra"::text FROM "orders" WHERE "k" = $1 FOR UPDATE`) {
		t.Errorf("locking select missing: %s", selectSQL)
	}
}

func TestMergeJSONPatchRFC7386(t *testing.T) {
	current := []byte(`{"a":1,"b":{"x":1,"y":2},"c":"keep"}`)
	var patch map[string]any
	if err := json.Unmarshal([]byte(`{"b":{"x":2},"c":null}`), &patch); err != nil {
		t.Fatal(err)
	}
	merged, err := MergeJSONPatch(current, patch)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(merged, &out); err != nil {
		t.Fatal(err)
	}
	if out["a"] != float64(1) {
		t.Errorf("a = %v, want 1", out["a"])
	}
	b := out["b"].(map[string]any)
	if b["x"] != float64(2) || b["y"] != float64(2) {
		t.Errorf("nested merge wrong: %v", b)
	}
	if _, exists := out["c"]; exists {
		t.Error("null patch member must delete the key")
	}
}

func TestMergeJSONPatchEmptyCurrent(t *testing.T) {
	merged, err := MergeJSONPatch(nil, map[string]any{"a": json.Number("1")})
	if err != nil {
		t.Fatal(err)
	}
	if string(merged) != `{"a":1}` {
		t.Errorf("merged = %s", merged)
	}
}

func TestFormatArg(t *testing.T) {
	loc, _ := time.LoadLocation("UTC")
	if got := formatArg(time.Date(2026, 8, 19, 1, 2, 3, 4000000, time.UTC), loc); got != "2026-08-19 01:02:03.004" {
		t.Errorf("datetime = %v", got)
	}
	if got := formatArg(nil, loc); got != nil {
		t.Errorf("nil = %v", got)
	}
}

func TestClassifyMySQLAffected(t *testing.T) {
	if i, u, un := classifyMySQLAffected(0); i != 0 || u != 0 || un != 1 {
		t.Errorf("0 -> unchanged, got %d/%d/%d", i, u, un)
	}
	if i, u, _ := classifyMySQLAffected(1); i != 1 || u != 0 {
		t.Errorf("1 -> inserted, got %d/%d", i, u)
	}
	if i, u, _ := classifyMySQLAffected(2); i != 0 || u != 1 {
		t.Errorf("2 -> updated, got %d/%d", i, u)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
