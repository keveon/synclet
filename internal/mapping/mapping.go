package mapping

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/keveon/synclet/internal/jsonpath"
	"github.com/keveon/synclet/internal/model"
)

// Config is the mapping section of a job.
type Config struct {
	Fields map[string]ValueMapping `yaml:"fields"`
}

// ValueMapping maps one target column from the source row.
type ValueMapping struct {
	Type       string                  `yaml:"type"`
	Column     string                  `yaml:"column"`
	Path       string                  `yaml:"path"`
	Value      any                     `yaml:"value"`
	Default    any                     `yaml:"default"`
	Required   bool                    `yaml:"required"`
	Transforms []Transform             `yaml:"transforms"`
	Selectors  []SourceSelector        `yaml:"selectors"`
	Fields     map[string]ValueMapping `yaml:"fields"`
}

// Transform is an ordered row-level transform.
type Transform struct {
	Type   string     `yaml:"type"`
	Column string     `yaml:"column"`
	Values []string   `yaml:"values"`
	When   *Condition `yaml:"when"`
}

// Condition guards a transform by column equality.
type Condition struct {
	Column string `yaml:"column"`
	Equals string `yaml:"equals"`
}

// SourceSelector tries source paths in order; the first resolvable value
// wins. json_path selectors evaluate a rooted dot path; element selectors
// locate an entry by its `code` key inside the array found at `path` and
// then resolve `value_path` relative to that entry.
type SourceSelector struct {
	Type      string `yaml:"type"`
	Path      string `yaml:"path"`
	Code      string `yaml:"code"`
	ValuePath string `yaml:"value_path"`
}

// Mapper maps source rows to target records.
type Mapper struct {
	cfg Config
}

// NewMapper validates and constructs a Mapper.
func NewMapper(cfg Config) (*Mapper, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	return &Mapper{cfg: cfg}, nil
}

// ValidateConfig validates the mapping section of a job.
func ValidateConfig(cfg Config) error {
	if len(cfg.Fields) == 0 {
		return fmt.Errorf("fields is required")
	}
	for field, valueMapping := range cfg.Fields {
		if strings.TrimSpace(field) == "" {
			return fmt.Errorf("field name is required")
		}
		if err := valueMapping.Validate(); err != nil {
			return fmt.Errorf("field %s: %w", field, err)
		}
	}
	return nil
}

// Validate checks one field mapping against its declared type.
func (m ValueMapping) Validate() error {
	kind := m.kind()
	switch kind {
	case "column":
		if strings.TrimSpace(m.Column) == "" {
			return fmt.Errorf("column is required")
		}
	case "literal":
		// Any YAML scalar/object is acceptable.
	case "json_path":
		if _, err := jsonpath.Parse(m.Path); err != nil {
			return fmt.Errorf("path: %w", err)
		}
	case "json_object":
		if len(m.Fields) == 0 {
			return fmt.Errorf("json_object fields is required")
		}
		for field, nested := range m.Fields {
			if strings.TrimSpace(field) == "" {
				return fmt.Errorf("json_object field name is required")
			}
			if err := nested.Validate(); err != nil {
				return fmt.Errorf("json_object field %s: %w", field, err)
			}
		}
	case "selector":
		if len(m.Selectors) == 0 {
			return fmt.Errorf("selectors is required")
		}
		for i, selector := range m.Selectors {
			if err := selector.Validate(); err != nil {
				return fmt.Errorf("selector %d: %w", i, err)
			}
		}
	default:
		return fmt.Errorf("unsupported mapping type %q", m.Type)
	}
	for i, transform := range m.Transforms {
		if err := transform.Validate(); err != nil {
			return fmt.Errorf("transform %d: %w", i, err)
		}
	}
	return nil
}

func (m ValueMapping) kind() string {
	kind := strings.TrimSpace(m.Type)
	if kind == "" && len(m.Selectors) > 0 {
		return "selector"
	}
	return kind
}

// Validate checks one transform against its declared type.
func (t Transform) Validate() error {
	switch strings.TrimSpace(t.Type) {
	case "add_column":
		if strings.TrimSpace(t.Column) == "" {
			return fmt.Errorf("column is required")
		}
		if _, err := jsonpath.ParseRelative(t.Column); err != nil {
			return fmt.Errorf("column: %w", err)
		}
		if err := t.validateWhen(); err != nil {
			return err
		}
	case "negative_to_zero":
		if err := t.validateWhen(); err != nil {
			return err
		}
	case "require_column_in":
		if strings.TrimSpace(t.Column) == "" {
			return fmt.Errorf("column is required")
		}
		if _, err := jsonpath.ParseRelative(t.Column); err != nil {
			return fmt.Errorf("column: %w", err)
		}
		if len(t.Values) == 0 {
			return fmt.Errorf("values is required")
		}
		for i, value := range t.Values {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("values[%d] must not be empty", i)
			}
		}
		if t.When != nil {
			return fmt.Errorf("when is not supported")
		}
	default:
		return fmt.Errorf("unsupported transform type %q", t.Type)
	}
	return nil
}

func (t Transform) validateWhen() error {
	if t.When == nil {
		return nil
	}
	if strings.TrimSpace(t.When.Column) == "" {
		return fmt.Errorf("when.column is required")
	}
	if _, err := jsonpath.ParseRelative(t.When.Column); err != nil {
		return fmt.Errorf("when.column: %w", err)
	}
	if strings.TrimSpace(t.When.Equals) == "" {
		return fmt.Errorf("when.equals is required")
	}
	return nil
}

// Validate checks one selector against its declared type.
func (s SourceSelector) Validate() error {
	switch strings.TrimSpace(s.Type) {
	case "json_path":
		if _, err := jsonpath.Parse(s.Path); err != nil {
			return fmt.Errorf("path: %w", err)
		}
		return nil
	case "element":
		if _, err := jsonpath.Parse(s.Path); err != nil {
			return fmt.Errorf("path: %w", err)
		}
		if strings.TrimSpace(s.Code) == "" {
			return fmt.Errorf("code is required")
		}
		if _, err := jsonpath.ParseRelative(s.ValuePath); err != nil {
			return fmt.Errorf("value_path: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported selector type %q", s.Type)
	}
}

// Map turns one source row into a target record.
func (m *Mapper) Map(row model.SourceRow) (model.Record, error) {
	normalized := NormalizeMap(map[string]any(row))
	fields := make(map[string]any, len(m.cfg.Fields))
	for targetField, valueMapping := range m.cfg.Fields {
		value, ok, err := valueMapping.Resolve(normalized)
		if err != nil {
			return model.Record{}, fmt.Errorf("field %s: %w", targetField, err)
		}
		if valueMapping.Required && (!ok || isRequiredValueMissing(value)) {
			return model.Record{}, fmt.Errorf("field %s: required value is missing", targetField)
		}
		if !ok {
			value = normalizeValue(valueMapping.Default)
		}
		value, err = valueMapping.applyTransforms(value, normalized)
		if err != nil {
			return model.Record{}, fmt.Errorf("field %s: %w", targetField, err)
		}
		fields[targetField] = value
	}
	return model.Record{Fields: fields}, nil
}

// Resolve produces the field value and whether it was found.
func (m ValueMapping) Resolve(row map[string]any) (any, bool, error) {
	switch m.kind() {
	case "column":
		value, ok := row[strings.TrimSpace(m.Column)]
		if !ok || value == nil {
			return nil, false, nil
		}
		return value, true, nil
	case "literal":
		return normalizeValue(m.Value), true, nil
	case "json_path":
		value, ok, err := jsonpath.Get(row, m.Path)
		if err != nil || !ok {
			return value, ok, err
		}
		// Values destined for JSON documents must serialize natively:
		// decode remaining []byte scalars (MySQL VARCHAR/DECIMAL) into
		// json.Number or string so encoding/json never base64s them.
		switch value.(type) {
		case map[string]any, []any:
			return normalizeJSONScalars(value), true, nil
		}
		return value, true, nil
	case "json_object":
		out := make(map[string]any, len(m.Fields))
		for key, nested := range m.Fields {
			value, ok, err := nested.Resolve(row)
			if err != nil {
				return nil, false, fmt.Errorf("json_object field %s: %w", key, err)
			}
			if nested.Required && (!ok || isRequiredValueMissing(value)) {
				return nil, false, fmt.Errorf("json_object field %s: required value is missing", key)
			}
			if !ok {
				value = normalizeValue(nested.Default)
			}
			value, err = nested.applyTransforms(value, row)
			if err != nil {
				return nil, false, fmt.Errorf("json_object field %s: %w", key, err)
			}
			out[key] = value
		}
		return out, true, nil
	case "selector":
		return m.resolveSelector(row)
	default:
		return nil, false, fmt.Errorf("unsupported mapping type %q", m.Type)
	}
}

func (m ValueMapping) applyTransforms(value any, row map[string]any) (any, error) {
	if m.requiresExactDecimal() && value != nil {
		if _, ok := ExactDecimalValue(value); !ok {
			return nil, fmt.Errorf("value must be an exact decimal")
		}
	}
	var err error
	for i, transform := range m.Transforms {
		value, err = transform.Apply(value, row)
		if err != nil {
			return nil, fmt.Errorf("transform %d: %w", i, err)
		}
	}
	return value, nil
}

func (m ValueMapping) requiresExactDecimal() bool {
	for _, transform := range m.Transforms {
		if strings.TrimSpace(transform.Type) == "add_column" {
			return true
		}
	}
	return false
}

// Apply executes one transform.
func (t Transform) Apply(value any, row map[string]any) (any, error) {
	switch strings.TrimSpace(t.Type) {
	case "require_column_in":
		return t.requireColumnIn(value, row)
	case "negative_to_zero":
		apply, err := t.matches(row)
		if err != nil || !apply {
			return value, err
		}
		return negativeToZero(value), nil
	case "add_column":
		return t.addColumn(value, row)
	default:
		return nil, fmt.Errorf("unsupported transform type %q", t.Type)
	}
}

func resolveRowReference(row map[string]any, reference string) (any, bool, error) {
	reference = strings.TrimSpace(reference)
	if value, ok := row[reference]; ok {
		return value, true, nil
	}
	return jsonpath.GetRelative(row, reference)
}

func (t Transform) requireColumnIn(value any, row map[string]any) (any, error) {
	column := strings.TrimSpace(t.Column)
	columnValue, ok, err := resolveRowReference(row, column)
	if err != nil {
		return nil, fmt.Errorf("column %s: %w", column, err)
	}
	if !ok || columnValue == nil {
		return nil, fmt.Errorf("column %s is missing", column)
	}
	actual, ok := StringValue(columnValue)
	if !ok {
		return nil, fmt.Errorf("column %s is not comparable", column)
	}
	for _, allowed := range t.Values {
		if actual == strings.TrimSpace(allowed) {
			return value, nil
		}
	}
	return nil, fmt.Errorf("column %s has unsupported value %q", column, actual)
}

func (t Transform) addColumn(value any, row map[string]any) (any, error) {
	apply, err := t.matches(row)
	if err != nil || !apply {
		return value, err
	}
	column := strings.TrimSpace(t.Column)
	addendValue, ok, err := resolveRowReference(row, column)
	if err != nil {
		return nil, fmt.Errorf("column %s: %w", column, err)
	}
	if !ok || addendValue == nil {
		return nil, fmt.Errorf("column %s is missing", column)
	}
	addend, ok := ExactDecimalValue(addendValue)
	if !ok {
		return nil, fmt.Errorf("column %s must be an exact decimal", column)
	}
	if value == nil {
		return nil, nil
	}
	base, ok := ExactDecimalValue(value)
	if !ok {
		return nil, fmt.Errorf("value must be an exact decimal")
	}
	return base.Add(*addend), nil
}

func (t Transform) matches(row map[string]any) (bool, error) {
	if t.When == nil {
		return true, nil
	}
	column := strings.TrimSpace(t.When.Column)
	value, ok, err := resolveRowReference(row, column)
	if err != nil {
		return false, fmt.Errorf("when column %s: %w", column, err)
	}
	if !ok || value == nil {
		return false, fmt.Errorf("when column %s is missing", column)
	}
	actual, ok := StringValue(value)
	if !ok {
		return false, fmt.Errorf("when column %s is not comparable", column)
	}
	return actual == strings.TrimSpace(t.When.Equals), nil
}

func negativeToZero(value any) any {
	parsed, ok := decimalValue(value)
	if ok && parsed.IsNegative() {
		return decimal.Zero
	}
	return value
}

func (m ValueMapping) resolveSelector(payload map[string]any) (any, bool, error) {
	for _, selector := range m.Selectors {
		switch strings.TrimSpace(selector.Type) {
		case "json_path":
			value, ok, err := jsonpath.Get(payload, selector.Path)
			if err != nil {
				return nil, false, err
			}
			if !ok || value == nil {
				continue
			}
			if m.requiresExactDecimal() {
				switch value.(type) {
				case float32, float64:
					return nil, false, fmt.Errorf("selector value must be an exact decimal")
				}
				if parsed, ok := ExactDecimalValue(value); ok {
					return parsed, true, nil
				}
				continue
			}
			if parsed, ok := decimalValue(value); ok {
				return parsed, true, nil
			}
		case "element":
			value, ok, err := resolveElementSelector(payload, selector)
			if err != nil {
				return nil, false, err
			}
			if !ok || value == nil {
				continue
			}
			if m.requiresExactDecimal() {
				switch value.(type) {
				case float32, float64:
					return nil, false, fmt.Errorf("selector value must be an exact decimal")
				}
				if parsed, ok := ExactDecimalValue(value); ok {
					return parsed, true, nil
				}
				continue
			}
			if parsed, ok := decimalValue(value); ok {
				return parsed, true, nil
			}
		default:
			return nil, false, fmt.Errorf("unsupported selector type %q", selector.Type)
		}
	}
	return nil, false, nil
}

// resolveElementSelector locates the entry for a code and resolves
// value_path relative to that entry. Two source shapes are supported:
// an object keyed by code (entry looked up directly) and an array of
// entries each carrying a `code` key. A missing path, a missing code,
// or an entry without the requested code falls through; a present but
// non-collection path is a configuration error and fails closed.
func resolveElementSelector(payload map[string]any, selector SourceSelector) (any, bool, error) {
	source, ok, err := jsonpath.Get(payload, selector.Path)
	if err != nil || !ok || source == nil {
		return nil, false, err
	}
	code := strings.TrimSpace(selector.Code)
	switch typed := source.(type) {
	case map[string]any:
		entry, ok := typed[code]
		if !ok || entry == nil {
			return nil, false, nil
		}
		return jsonpath.GetRelative(entry, selector.ValuePath)
	case []any:
		for _, entry := range typed {
			object, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			entryCode, ok := StringValue(object["code"])
			if !ok || entryCode != code {
				continue
			}
			return jsonpath.GetRelative(object, selector.ValuePath)
		}
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("element selector path %q must point at an array or an object keyed by code", selector.Path)
	}
}

// NormalizeMap normalizes every value of a row for mapping: JSON bytes and
// JSON-looking strings become decoded maps, recursively.
func NormalizeMap(row map[string]any) map[string]any {
	out := make(map[string]any, len(row))
	for key, value := range row {
		out[key] = normalizeValue(value)
	}
	return out
}

func normalizeValue(value any) any {
	switch typed := value.(type) {
	case []byte:
		// MySQL delivers VARCHAR/DECIMAL columns as []byte too; only
		// object/array documents are JSON — bare scalars (numbers,
		// quoted strings) must keep their byte identity so column
		// mappings preserve string types.
		trimmed := bytes.TrimSpace(typed)
		if json.Valid(trimmed) && (bytes.HasPrefix(trimmed, []byte("{")) || bytes.HasPrefix(trimmed, []byte("["))) {
			return normalizeJSONBytes(trimmed)
		}
		return typed
	case string:
		trimmed := strings.TrimSpace(typed)
		if json.Valid([]byte(trimmed)) && (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) {
			return normalizeJSONBytes([]byte(trimmed))
		}
		return typed
	case map[string]any:
		return NormalizeMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, v := range typed {
			out[i] = normalizeValue(v)
		}
		return out
	default:
		return value
	}
}

// normalizeJSONScalars recursively converts []byte leaves of a decoded
// JSON document into json.Number or string so encoding/json serializes
// them as JSON scalars instead of base64.
func normalizeJSONScalars(value any) any {
	switch typed := value.(type) {
	case []byte:
		trimmed := bytes.TrimSpace(typed)
		if trimmed == nil {
			return nil
		}
		if json.Valid(trimmed) && !bytes.HasPrefix(trimmed, []byte("{")) && !bytes.HasPrefix(trimmed, []byte("[")) {
			decoder := json.NewDecoder(bytes.NewReader(trimmed))
			decoder.UseNumber()
			var scalar any
			if err := decoder.Decode(&scalar); err == nil {
				return scalar
			}
		}
		return string(typed)
	case string:
		return typed
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, v := range typed {
			out[key] = normalizeJSONScalars(v)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, v := range typed {
			out[i] = normalizeJSONScalars(v)
		}
		return out
	default:
		return value
	}
}

func normalizeJSONBytes(data []byte) any {
	if !json.Valid(data) {
		return data
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return data
	}
	return normalizeValue(value)
}

func isRequiredValueMissing(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case []byte:
		return len(bytes.TrimSpace(typed)) == 0
	default:
		return false
	}
}

// ExactDecimalValue converts a value to a decimal, rejecting floats as
// inexact. Strings that do not parse as decimals are not exact decimals.
func ExactDecimalValue(value any) (*decimal.Decimal, bool) {
	switch value.(type) {
	case float32, float64:
		return nil, false
	default:
		return decimalValue(value)
	}
}

func decimalValue(value any) (*decimal.Decimal, bool) {
	switch typed := value.(type) {
	case nil:
		return nil, false
	case decimal.Decimal:
		return &typed, true
	case *decimal.Decimal:
		if typed == nil {
			return nil, false
		}
		return typed, true
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return nil, false
		}
		parsed, err := decimal.NewFromString(trimmed)
		if err != nil {
			return nil, false
		}
		return &parsed, true
	case json.Number:
		parsed, err := decimal.NewFromString(typed.String())
		if err != nil {
			return nil, false
		}
		return &parsed, true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return nil, false
		}
		parsed := decimal.NewFromFloat(typed)
		return &parsed, true
	case float32:
		value := float64(typed)
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, false
		}
		parsed := decimal.NewFromFloat(value)
		return &parsed, true
	case int:
		return decimalPtr(decimal.NewFromInt(int64(typed)))
	case int8:
		return decimalPtr(decimal.NewFromInt(int64(typed)))
	case int16:
		return decimalPtr(decimal.NewFromInt(int64(typed)))
	case int64:
		return decimalPtr(decimal.NewFromInt(typed))
	case int32:
		return decimalPtr(decimal.NewFromInt(int64(typed)))
	case uint:
		return decimalFromUint64(uint64(typed))
	case uint8:
		return decimalFromUint64(uint64(typed))
	case uint16:
		return decimalFromUint64(uint64(typed))
	case uint32:
		return decimalFromUint64(uint64(typed))
	case uint64:
		return decimalFromUint64(typed)
	case pgtype.Numeric:
		return decimalFromPGNumeric(typed)
	case *pgtype.Numeric:
		if typed == nil {
			return nil, false
		}
		return decimalFromPGNumeric(*typed)
	default:
		return nil, false
	}
}

func decimalFromPGNumeric(value pgtype.Numeric) (*decimal.Decimal, bool) {
	if !value.Valid || value.NaN || value.InfinityModifier != pgtype.Finite {
		return nil, false
	}
	if value.Int == nil {
		parsed := decimal.Zero
		return &parsed, true
	}
	parsed := decimal.NewFromBigInt(value.Int, value.Exp)
	return &parsed, true
}

func decimalPtr(d decimal.Decimal) (*decimal.Decimal, bool) {
	return &d, true
}

func decimalFromUint64(value uint64) (*decimal.Decimal, bool) {
	parsed, err := decimal.NewFromString(strconv.FormatUint(value, 10))
	if err != nil {
		return nil, false
	}
	return &parsed, true
}

// StringValue renders a value as a comparable string when possible.
func StringValue(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		return trimmed, trimmed != ""
	case json.Number:
		return typed.String(), true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return "", false
		}
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	case int:
		return strconv.Itoa(typed), true
	case int64:
		return strconv.FormatInt(typed, 10), true
	case time.Time:
		return typed.Format(time.RFC3339Nano), true
	default:
		return "", false
	}
}
