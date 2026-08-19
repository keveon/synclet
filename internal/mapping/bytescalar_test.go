package mapping

import (
	"testing"

	"github.com/keveon/synclet/internal/model"
)

// TestNormalizeValueKeepsBareByteScalars pins the MySQL []byte guard:
// VARCHAR/DECIMAL columns arrive as []byte and must keep their byte
// identity (string types preserved) unless they are JSON documents.
func TestNormalizeValueKeepsBareByteScalars(t *testing.T) {
	station := normalizeValue([]byte("30600100"))
	if s, ok := station.([]byte); !ok || string(s) != "30600100" {
		t.Errorf("station code lost byte identity: %#v", station)
	}

	doc := normalizeValue([]byte(`{"tier":"T1"}`))
	if _, ok := doc.(map[string]any); !ok {
		t.Errorf("JSON document must decode: %#v", doc)
	}
}

// TestJSONPathRootNormalizesScalars pins raw_payload generation: the row
// snapshot taken via json_path $ must serialize []byte leaves as JSON
// scalars, never base64.
func TestJSONPathRootNormalizesScalars(t *testing.T) {
	mapper, err := NewMapper(Config{Fields: map[string]ValueMapping{
		"raw_payload": {Type: "json_path", Path: "$"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	record, err := mapper.Map(model.SourceRow{
		"stcd": []byte("30600100"),
		"z":    []byte("36.120"),
		"meta": []byte(`{"kind":"net"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := record.Fields["raw_payload"].(map[string]any)
	if !ok {
		t.Fatalf("raw_payload = %#v", record.Fields["raw_payload"])
	}
	if s, ok := payload["stcd"].(string); !ok || s != "30600100" {
		// stcd may surface as json.Number (bare numeric scalar decoded as
		// JSON); either way it must not be base64 []byte.
		if n, isNum := payload["stcd"].(interface{ String() string }); !isNum || n.String() != "30600100" {
			t.Errorf("stcd in payload = %#v", payload["stcd"])
		}
	}
	if _, isBytes := payload["stcd"].([]byte); isBytes {
		t.Error("stcd must not stay []byte (would base64)")
	}
	if _, isBytes := payload["z"].([]byte); isBytes {
		t.Error("z must not stay []byte (would base64)")
	}
	nested, ok := payload["meta"].(map[string]any)
	if !ok || nested["kind"] != "net" {
		t.Errorf("nested doc = %#v", payload["meta"])
	}
}

// TestColumnMappingKeepsByteIdentity pins the column path: after the
// []byte guard, a plain column mapping still yields the raw []byte for
// writers that bind it as a string parameter.
func TestColumnMappingKeepsByteIdentity(t *testing.T) {
	mapper, err := NewMapper(Config{Fields: map[string]ValueMapping{
		"station_code": {Type: "column", Column: "stcd"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	record, err := mapper.Map(model.SourceRow{"stcd": []byte("30600100")})
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := record.Fields["station_code"].([]byte); !ok || string(s) != "30600100" {
		t.Errorf("column mapping lost byte identity: %#v", record.Fields["station_code"])
	}
}
