package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// exampleYAML mirrors config.example.yaml with documentation-safe values.
const exampleYAML = `
connections:
  source:
    type: postgres
    dsn_env: SOURCE_DSN
  target:
    type: mysql
    dsn_env: TARGET_DSN

checkpoint:
  type: file
  path: /var/lib/synclet/state.json

sync:
  poll_interval: 30s
  batch_size: 500

scope:
  allow_all: false
  allowed_codes:
    - "C001"
    - "C002"

jobs:
  - name: customers
    mode: snapshot
    reader:
      connection: source
      table: customers
      columns: [code, name, region, metadata, updated_at]
      filters:
        - column: enabled
          op: eq
          value: true
        - column: code
          op: in
          values_from: scope.allowed_codes
      order_by: [code]
    mapping:
      fields:
        customer_code:
          type: column
          column: code
        tier:
          type: json_path
          path: $.metadata.tier_code
          required: true
        source_system:
          type: literal
          value: erp
    writer:
      connection: target
      table: customers
      key_columns: [customer_code]
      update_columns: [tier, source_system]
      timezone: UTC

  - name: orders
    mode: incremental
    reader:
      connection: source
      table: orders
      alias: o
      columns: [o.id, o.customer_id, o.ordered_at, o.submitted_at, o.attributes, c.metadata]
      joins:
        - type: left
          table: customers
          alias: c
          on:
            left: o.customer_id
            right: c.code
      cursor:
        column: submitted_at
        tie_breaker_column: id
      filters:
        - column: o.status
          op: eq
          value: confirmed
      batch_size: 500
    mapping:
      fields:
        customer_code:
          type: column
          column: customer_id
        shipping_weight:
          selectors:
            - type: json_path
              path: $.attributes.weight
            - type: json_path
              path: $.attributes.parcel_weight
          transforms:
            - type: require_column_in
              column: metadata.weight_kind
              values: [net, gross]
            - type: add_column
              column: metadata.tare_weight
              when:
                column: metadata.weight_kind
                equals: net
    writer:
      connection: target
      table: orders
      key_columns: [customer_code, ordered_at]
      update_columns: [shipping_weight]
      null_update_policy: keep_existing
      timezone: UTC
`

func TestParseExampleConfig(t *testing.T) {
	cfg, err := Parse([]byte(exampleYAML))
	if err != nil {
		t.Fatalf("example config must parse and validate: %v", err)
	}
	if len(cfg.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(cfg.Jobs))
	}
	if cfg.Jobs[0].CanonicalMode() != "snapshot" {
		t.Errorf("job 0 mode = %s, want snapshot", cfg.Jobs[0].CanonicalMode())
	}
	if cfg.Jobs[1].CanonicalMode() != "incremental" {
		t.Errorf("job 1 mode = %s, want incremental", cfg.Jobs[1].CanonicalMode())
	}
	if cfg.Sync.PollInterval.Duration != 30*time.Second {
		t.Errorf("poll_interval = %s, want 30s", cfg.Sync.PollInterval.Duration)
	}
}

func TestLoadRealExampleFile(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Skipf("config.example.yaml not found: %v", err)
	}
	if _, err := Parse(data); err != nil {
		t.Fatalf("config.example.yaml must load and validate: %v", err)
	}
}

func TestParseFailsClosed(t *testing.T) {
	base := func(mutation func(s *string)) string {
		yaml := exampleYAML
		mutation(&yaml)
		return yaml
	}
	cases := map[string]string{
		"empty scope allowlist": base(func(s *string) {
			*s = strings.Replace(*s, "  allowed_codes:\n    - \"C001\"\n    - \"C002\"\n", "", 1)
		}),
		"no jobs":             strings.Replace(exampleYAML, "jobs:", "jobs: []", 1),
		"unknown key":         exampleYAML + "\nunknown_section: {}\n",
		"literal dsn":         strings.Replace(exampleYAML, "dsn_env: SOURCE_DSN", "dsn: postgres://user:pass@host/db", 1),
		"bad connection type": strings.Replace(exampleYAML, "type: postgres", "type: oracle", 1),
		"missing cursor":      strings.Replace(exampleYAML, "tie_breaker_column: id", "", 1),
		"duplicated job":      exampleYAML + strings.Replace(strings.SplitN(strings.SplitN(exampleYAML, "  - name: orders", 2)[1], "    mapping:", 2)[0], "orders", "orders2", 1) + "    mapping:\n      fields:\n        x:\n          type: literal\n          value: 1\n    writer:\n      connection: target\n      table: orders\n      key_columns: [x]\n      update_columns: [x]\n",
	}
	for name, yaml := range cases {
		if _, err := Parse([]byte(yaml)); err == nil {
			t.Errorf("%s: config must fail closed", name)
		}
	}
}

func TestScopeAllowAll(t *testing.T) {
	yaml := strings.Replace(exampleYAML, "allow_all: false", "allow_all: true", 1)
	if _, err := Parse([]byte(yaml)); err != nil {
		t.Errorf("allow_all: true must validate: %v", err)
	}
}

func TestJobCanonicalModeDefaultsIncremental(t *testing.T) {
	if got := (JobConfig{}).CanonicalMode(); got != "incremental" {
		t.Errorf("empty mode = %q, want incremental", got)
	}
}

func TestMySQLReaderConnectionAllowed(t *testing.T) {
	yaml := strings.Replace(exampleYAML, "  source:\n    type: postgres", "  source:\n    type: mysql", 1)
	if _, err := Parse([]byte(yaml)); err != nil {
		t.Errorf("mysql source must validate: %v", err)
	}
}

func TestBothDirectionsValidate(t *testing.T) {
	// Swap the connection types: postgres source <-> mysql target become
	// mysql source <-> postgres target. Both directions must validate.
	reversed := strings.Replace(exampleYAML, "  source:\n    type: postgres", "  source:\n    type: __SWAP__", 1)
	reversed = strings.Replace(reversed, "  target:\n    type: mysql", "  target:\n    type: postgres", 1)
	reversed = strings.Replace(reversed, "type: __SWAP__", "type: mysql", 1)
	if _, err := Parse([]byte(reversed)); err != nil {
		t.Errorf("reversed directions must validate: %v", err)
	}
}

func TestDurationUnmarshal(t *testing.T) {
	cfg, err := Parse([]byte(strings.Replace(exampleYAML, "poll_interval: 30s", "poll_interval: 1m", 1)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Sync.PollInterval.Duration != time.Minute {
		t.Errorf("poll_interval = %s, want 1m", cfg.Sync.PollInterval.Duration)
	}
}
