package mapping

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/keveon/synclet/internal/model"
)

func TestColumnMapping(t *testing.T) {
	mapper, err := NewMapper(Config{Fields: map[string]ValueMapping{
		"code": {Type: "column", Column: "code"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	record, err := mapper.Map(model.SourceRow{"code": "C001"})
	if err != nil {
		t.Fatal(err)
	}
	if record.Fields["code"] != "C001" {
		t.Errorf("code = %v", record.Fields["code"])
	}
}

func TestRequiredFailsOnMissing(t *testing.T) {
	mapper, err := NewMapper(Config{Fields: map[string]ValueMapping{
		"tier": {Type: "json_path", Path: "$.metadata.tier_code", Required: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mapper.Map(model.SourceRow{"metadata": map[string]any{}}); err == nil {
		t.Error("required json_path miss must fail")
	}
}

func TestDefaultAppliesWhenMissing(t *testing.T) {
	mapper, err := NewMapper(Config{Fields: map[string]ValueMapping{
		"name": {Type: "column", Column: "name", Default: ""},
	}})
	if err != nil {
		t.Fatal(err)
	}
	record, err := mapper.Map(model.SourceRow{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := record.Fields["name"]; !ok {
		t.Error("default must fill the field")
	}
}

func TestLiteralAndJsonObject(t *testing.T) {
	mapper, err := NewMapper(Config{Fields: map[string]ValueMapping{
		"source_system": {Type: "literal", Value: "erp"},
		"extra": {Type: "json_object", Fields: map[string]ValueMapping{
			"tier_code": {Type: "json_path", Path: "$.metadata.tier_code", Required: true},
			"tier_name": {Type: "json_path", Path: "$.metadata.tier_name"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	record, err := mapper.Map(model.SourceRow{"metadata": map[string]any{"tier_code": "T1"}})
	if err != nil {
		t.Fatal(err)
	}
	if record.Fields["source_system"] != "erp" {
		t.Errorf("literal = %v", record.Fields["source_system"])
	}
	extra, ok := record.Fields["extra"].(map[string]any)
	if !ok || extra["tier_code"] != "T1" {
		t.Errorf("json_object = %#v", record.Fields["extra"])
	}
}

func TestSelectorFirstNumericWins(t *testing.T) {
	mapper, err := NewMapper(Config{Fields: map[string]ValueMapping{
		"weight": {Selectors: []SourceSelector{
			{Type: "json_path", Path: "$.attributes.weight"},
			{Type: "json_path", Path: "$.attributes.parcel_weight"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	record, err := mapper.Map(model.SourceRow{"attributes": map[string]any{"weight": json.Number("12.5")}})
	if err != nil {
		t.Fatal(err)
	}
	weight, ok := record.Fields["weight"].(*decimal.Decimal)
	if !ok || weight.String() != "12.5" {
		t.Errorf("weight = %#v", record.Fields["weight"])
	}
}

func TestSelectorExactDecimalRejectsFloats(t *testing.T) {
	mapper, err := NewMapper(Config{Fields: map[string]ValueMapping{
		"weight": {Selectors: []SourceSelector{
			{Type: "json_path", Path: "$.attributes.weight"},
		}, Transforms: []Transform{{Type: "add_column", Column: "tare"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// float64 source: add_column demands exactness -> must error, not guess
	if _, err := mapper.Map(model.SourceRow{"attributes": map[string]any{"weight": 12.5}, "tare": "0.3"}); err == nil {
		t.Error("float selector with add_column must fail as inexact")
	}
}

func TestTransformsAddColumnConditional(t *testing.T) {
	mapper, err := NewMapper(Config{Fields: map[string]ValueMapping{
		"weight": {Selectors: []SourceSelector{
			{Type: "json_path", Path: "$.attributes.weight"},
		}, Transforms: []Transform{
			{Type: "require_column_in", Column: "metadata.weight_kind", Values: []string{"net", "gross"}},
			{Type: "add_column", Column: "metadata.tare_weight", When: &Condition{Column: "metadata.weight_kind", Equals: "net"}},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	// net + tare
	record, err := mapper.Map(model.SourceRow{
		"attributes": map[string]any{"weight": json.Number("10.2")},
		"metadata":   map[string]any{"weight_kind": "net", "tare_weight": json.Number("0.3")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := decimalString(record.Fields["weight"]); got != "10.5" {
		t.Errorf("net+tare = %s, want 10.5", got)
	}

	// gross passes through
	record, err = mapper.Map(model.SourceRow{
		"attributes": map[string]any{"weight": json.Number("11.0")},
		"metadata":   map[string]any{"weight_kind": "gross"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := decimalString(record.Fields["weight"]); got != "11" {
		t.Errorf("gross passthrough = %s, want 11", got)
	}

	// unknown kind fails
	if _, err := mapper.Map(model.SourceRow{
		"attributes": map[string]any{"weight": json.Number("1")},
		"metadata":   map[string]any{"weight_kind": "mystery"},
	}); err == nil {
		t.Error("unknown weight_kind must fail")
	}
}

func TestNegativeToZeroTransform(t *testing.T) {
	mapper, err := NewMapper(Config{Fields: map[string]ValueMapping{
		"value": {Type: "column", Column: "value", Transforms: []Transform{{Type: "negative_to_zero"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	record, err := mapper.Map(model.SourceRow{"value": json.Number("-3.5")})
	if err != nil {
		t.Fatal(err)
	}
	if got := decimalString(record.Fields["value"]); got != "0" {
		t.Errorf("negative_to_zero = %s, want 0", got)
	}
}

// decimalString renders a decimal value (value or pointer) for assertions.
func decimalString(value any) string {
	switch typed := value.(type) {
	case decimal.Decimal:
		return typed.String()
	case *decimal.Decimal:
		if typed == nil {
			return "<nil>"
		}
		return typed.String()
	default:
		return "<not-decimal>"
	}
}

func TestJSONBytesNormalization(t *testing.T) {
	mapper, err := NewMapper(Config{Fields: map[string]ValueMapping{
		"tier": {Type: "json_path", Path: "$.metadata.tier_code"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	record, err := mapper.Map(model.SourceRow{"metadata": []byte(`{"tier_code":"T9"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if record.Fields["tier"] != "T9" {
		t.Errorf("tier = %v", record.Fields["tier"])
	}
}

func TestValidateConfigFailsClosed(t *testing.T) {
	if err := ValidateConfig(Config{}); err == nil {
		t.Error("empty fields must fail")
	}
	if err := ValidateConfig(Config{Fields: map[string]ValueMapping{"x": {Type: "bogus"}}}); err == nil {
		t.Error("unknown type must fail")
	}
	if err := ValidateConfig(Config{Fields: map[string]ValueMapping{"x": {Type: "json_path", Path: "no-root"}}}); err == nil {
		t.Error("unrooted path must fail")
	}
	if err := ValidateConfig(Config{Fields: map[string]ValueMapping{"x": {Type: "column"}}}); err == nil {
		t.Error("column without name must fail")
	}
}

func TestExactDecimalFromPGNumeric(t *testing.T) {
	// pgtype.Numeric arrives from the PostgreSQL reader for numeric columns
	// handled via rows.Values() fallback paths.
	if _, ok := ExactDecimalValue(3.14); ok {
		t.Error("float64 must not be an exact decimal")
	}
	if d, ok := ExactDecimalValue(json.Number("12.34")); !ok || d.String() != "12.34" {
		t.Errorf("json.Number conversion failed: %v", d)
	}
	if d, ok := ExactDecimalValue(int64(7)); !ok || d.String() != "7" {
		t.Errorf("int64 conversion failed: %v", d)
	}
}

func TestStringValueCoversTypes(t *testing.T) {
	if s, ok := StringValue(time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)); !ok || s == "" {
		t.Error("time must render")
	}
	if _, ok := StringValue(map[string]any{}); ok {
		t.Error("maps must not render")
	}
}

func TestSelectorElementPicksValueByCode(t *testing.T) {
	mapper, err := NewMapper(Config{Fields: map[string]ValueMapping{
		"water_level": {Selectors: []SourceSelector{
			{Type: "element", Path: "$.elements", Code: "39", ValuePath: "value"},
			{Type: "element", Path: "$.elements", Code: "3a", ValuePath: "value"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	record, err := mapper.Map(model.SourceRow{"elements": []any{
		map[string]any{"code": "36", "value": json.Number("1.25")},
		map[string]any{"code": "39", "value": json.Number("12.5")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := decimalString(record.Fields["water_level"]); got != "12.5" {
		t.Errorf("element selector = %s, want 12.5", got)
	}
}

func TestSelectorElementFallsThroughToNext(t *testing.T) {
	mapper, err := NewMapper(Config{Fields: map[string]ValueMapping{
		"water_level": {Default: "", Selectors: []SourceSelector{
			{Type: "element", Path: "$.elements", Code: "39", ValuePath: "value"},
			{Type: "element", Path: "$.elements", Code: "3a", ValuePath: "value"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// 没有 39 也没有 3a：整体落空 -> ok=false -> default 生效，不报错
	record, err := mapper.Map(model.SourceRow{"elements": []any{
		map[string]any{"code": "36", "value": json.Number("1.25")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if record.Fields["water_level"] != "" {
		t.Errorf("miss must resolve to empty default, got %#v", record.Fields["water_level"])
	}
}

func TestSelectorElementNonArrayIsError(t *testing.T) {
	mapper, err := NewMapper(Config{Fields: map[string]ValueMapping{
		"water_level": {Selectors: []SourceSelector{
			{Type: "element", Path: "$.elements", Code: "39", ValuePath: "value"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// path 指到的是标量（非数组非对象）：fail-closed，必须报错
	if _, err := mapper.Map(model.SourceRow{"elements": json.Number("42")}); err == nil {
		t.Error("non-collection element source must fail")
	}
}

func TestSelectorElementRequiresCodeAndValuePath(t *testing.T) {
	_, err := NewMapper(Config{Fields: map[string]ValueMapping{
		"water_level": {Selectors: []SourceSelector{
			{Type: "element", Path: "$.elements"},
		}},
	}})
	if err == nil {
		t.Fatal("element selector without code/value_path must fail validation")
	}
}

func TestSelectorElementMapKeyedEntries(t *testing.T) {
	mapper, err := NewMapper(Config{Fields: map[string]ValueMapping{
		"water_level": {Selectors: []SourceSelector{
			{Type: "element", Path: "$.elements", Code: "39", ValuePath: "value"},
			{Type: "element", Path: "$.elements", Code: "3a", ValuePath: "value"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Axis 形态：elements 是以要素 code 为键的 JSON 对象
	record, err := mapper.Map(model.SourceRow{"elements": map[string]any{
		"36": map[string]any{"value": json.Number("1.25")},
		"39": map[string]any{"value": json.Number("12.5")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := decimalString(record.Fields["water_level"]); got != "12.5" {
		t.Errorf("map-keyed element selector = %s, want 12.5", got)
	}
}

func TestSelectorElementMapMissFallsThrough(t *testing.T) {
	mapper, err := NewMapper(Config{Fields: map[string]ValueMapping{
		"water_level": {Default: "", Selectors: []SourceSelector{
			{Type: "element", Path: "$.elements", Code: "39", ValuePath: "value"},
			{Type: "element", Path: "$.elements", Code: "3a", ValuePath: "value"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// map 里没有请求的 code：落空走 default，不报错
	record, err := mapper.Map(model.SourceRow{"elements": map[string]any{
		"36": map[string]any{"value": json.Number("1.25")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if record.Fields["water_level"] != "" {
		t.Errorf("map miss must resolve to empty default, got %#v", record.Fields["water_level"])
	}
}

func TestSelectorElementNonArrayNonMapStillError(t *testing.T) {
	mapper, err := NewMapper(Config{Fields: map[string]ValueMapping{
		"water_level": {Selectors: []SourceSelector{
			{Type: "element", Path: "$.elements", Code: "39", ValuePath: "value"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// 既不是数组也不是对象：依旧是配置/数据形态错误
	if _, err := mapper.Map(model.SourceRow{"elements": json.Number("42")}); err == nil {
		t.Error("scalar element source must fail")
	}
}
