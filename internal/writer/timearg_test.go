package writer

import (
	"testing"
	"time"
)

// TestFormatArgPostgresPassesTimeThrough pins the timezone fix: PostgreSQL
// receives native time.Time so pgx encodes the exact instant; rendering a
// local-literal string here would shift the value by the writer timezone.
func TestFormatArgPostgresPassesTimeThrough(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	value := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)

	got, ok := formatArgPostgres(value, shanghai).(time.Time)
	if !ok {
		t.Fatalf("formatArgPostgres must pass time.Time through natively, got %T", got)
	}
	if !got.Equal(value) {
		t.Errorf("instant shifted: %v -> %v", value, got)
	}
}

// TestFormatArgMySQLRendersLocalLiteral pins the MySQL side: DATETIME has
// no zone, so values are rendered in the writer timezone.
func TestFormatArgMySQLRendersLocalLiteral(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	value := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)

	got, ok := formatArg(value, shanghai).(string)
	if !ok {
		t.Fatalf("formatArg must render a string literal for MySQL, got %T", got)
	}
	if got != "2026-08-19 16:00:00.000" {
		t.Errorf("local literal = %q, want 2026-08-19 16:00:00.000", got)
	}
}
