package config

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/keveon/synclet/internal/dbutil"
	"github.com/keveon/synclet/internal/filter"
	"github.com/keveon/synclet/internal/mapping"
)

var envVarNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValuesFromScope is the only supported values_from reference.
const ValuesFromScope = "scope.allowed_codes"

// Config is the whole synclet configuration.
type Config struct {
	Connections map[string]ConnectionConfig `yaml:"connections"`
	Checkpoint  CheckpointConfig            `yaml:"checkpoint"`
	Sync        SyncConfig                  `yaml:"sync"`
	Scope       filter.Config               `yaml:"scope"`
	Jobs        []JobConfig                 `yaml:"jobs"`
}

// ConnectionConfig declares a database connection by environment variable
// name — never a literal DSN.
type ConnectionConfig struct {
	Type   string `yaml:"type"`
	DSNEnv string `yaml:"dsn_env"`
}

// CheckpointConfig selects the state backend.
type CheckpointConfig struct {
	Type string `yaml:"type"`
	Path string `yaml:"path"`
}

// SyncConfig holds loop-level defaults.
type SyncConfig struct {
	PollInterval Duration `yaml:"poll_interval"`
	BatchSize    int      `yaml:"batch_size"`
}

// JobConfig is one sync job: reader -> mapping -> writer.
type JobConfig struct {
	Name    string         `yaml:"name"`
	Mode    string         `yaml:"mode"`
	Reader  ReaderConfig   `yaml:"reader"`
	Mapping mapping.Config `yaml:"mapping"`
	Writer  WriterConfig   `yaml:"writer"`
}

// ReaderConfig describes the read-only source query.
type ReaderConfig struct {
	Connection string         `yaml:"connection"`
	Table      string         `yaml:"table"`
	Alias      string         `yaml:"alias"`
	Columns    []string       `yaml:"columns"`
	Joins      []JoinConfig   `yaml:"joins"`
	Filters    []FilterConfig `yaml:"filters"`
	Cursor     CursorConfig   `yaml:"cursor"`
	BatchSize  int            `yaml:"batch_size"`
	OrderBy    []string       `yaml:"order_by"`
}

// JoinConfig is a restricted inner/left equi-join.
type JoinConfig struct {
	Type  string       `yaml:"type"`
	Table string       `yaml:"table"`
	Alias string       `yaml:"alias"`
	On    JoinOnConfig `yaml:"on"`
}

// JoinOnConfig references alias.column = alias.column sides.
type JoinOnConfig struct {
	Left  string `yaml:"left"`
	Right string `yaml:"right"`
}

// CursorConfig declares the incremental keyset columns.
type CursorConfig struct {
	Column           string `yaml:"column"`
	TieBreakerColumn string `yaml:"tie_breaker_column"`
}

// FilterConfig is a single WHERE condition. Ops: eq, in.
type FilterConfig struct {
	Column     string `yaml:"column"`
	Op         string `yaml:"op"`
	Value      any    `yaml:"value"`
	ValuesFrom string `yaml:"values_from"`
}

// WriterConfig describes the target upsert.
type WriterConfig struct {
	Connection            string   `yaml:"connection"`
	Table                 string   `yaml:"table"`
	KeyColumns            []string `yaml:"key_columns"`
	UpdateColumns         []string `yaml:"update_columns"`
	NullUpdatePolicy      string   `yaml:"null_update_policy"`
	JSONMergePatchColumns []string `yaml:"json_merge_patch_columns"`
	Timezone              string   `yaml:"timezone"`
}

// Duration is a time.Duration that unmarshals from YAML strings like 30s.
type Duration struct {
	time.Duration
}

// CanonicalMode returns the canonical job mode; the empty string means
// incremental.
func (j JobConfig) CanonicalMode() string {
	mode := strings.TrimSpace(j.Mode)
	if mode == "" {
		return "incremental"
	}
	return mode
}

// Load reads and validates a config file.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return Parse(data)
}

// Parse decodes and validates config bytes. Unknown YAML keys are errors.
func Parse(data []byte) (Config, error) {
	cfg := Default()
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Default returns the built-in defaults applied before decoding.
func Default() Config {
	return Config{
		Connections: map[string]ConnectionConfig{},
		Checkpoint:  CheckpointConfig{Type: "file", Path: "/var/lib/synclet/state.json"},
		Sync: SyncConfig{
			PollInterval: Duration{Duration: 30 * time.Second},
			BatchSize:    500,
		},
		Jobs: []JobConfig{},
	}
}

// Validate enforces the fail-closed contract.
func (cfg Config) Validate() error {
	if len(cfg.Connections) == 0 {
		return fmt.Errorf("connections is required")
	}
	for name, conn := range cfg.Connections {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("connection name is required")
		}
		switch strings.TrimSpace(conn.Type) {
		case "postgres", "mysql":
		default:
			return fmt.Errorf("connections.%s.type must be postgres or mysql", name)
		}
		if err := validateEnvVarName(fmt.Sprintf("connections.%s.dsn_env", name), conn.DSNEnv); err != nil {
			return err
		}
	}

	if strings.TrimSpace(cfg.Checkpoint.Type) == "" {
		cfg.Checkpoint.Type = "file"
	}
	if cfg.Checkpoint.Type != "file" {
		return fmt.Errorf("checkpoint.type must be file")
	}
	if strings.TrimSpace(cfg.Checkpoint.Path) == "" {
		return fmt.Errorf("checkpoint.path is required")
	}
	if cfg.Sync.PollInterval.Duration <= 0 {
		return fmt.Errorf("sync.poll_interval must be greater than zero")
	}
	if cfg.Sync.BatchSize <= 0 {
		return fmt.Errorf("sync.batch_size must be greater than zero")
	}
	if !cfg.Scope.AllowAll && len(cfg.Scope.EffectiveAllowedCodes()) == 0 {
		return fmt.Errorf("scope.allowed_codes is empty and scope.allow_all is false")
	}
	if len(cfg.Jobs) == 0 {
		return fmt.Errorf("jobs is required")
	}

	seenJobs := map[string]struct{}{}
	for i, job := range cfg.Jobs {
		if err := cfg.validateJob(i, job, seenJobs); err != nil {
			return err
		}
	}
	return nil
}

func (cfg Config) validateJob(index int, job JobConfig, seen map[string]struct{}) error {
	prefix := fmt.Sprintf("jobs[%d]", index)
	name := strings.TrimSpace(job.Name)
	if name == "" {
		return fmt.Errorf("%s.name is required", prefix)
	}
	if _, ok := seen[name]; ok {
		return fmt.Errorf("job %q is duplicated", name)
	}
	seen[name] = struct{}{}

	switch job.CanonicalMode() {
	case "snapshot", "incremental":
	default:
		return fmt.Errorf("job %s mode must be snapshot or incremental", name)
	}

	readerConn, ok := cfg.Connections[job.Reader.Connection]
	if !ok {
		return fmt.Errorf("job %s reader.connection %q is not defined", name, job.Reader.Connection)
	}
	if readerConn.Type != "postgres" && readerConn.Type != "mysql" {
		return fmt.Errorf("job %s reader.connection must reference a postgres or mysql connection", name)
	}
	if strings.TrimSpace(job.Reader.Table) == "" {
		return fmt.Errorf("job %s reader.table is required", name)
	}
	if len(job.Reader.Columns) == 0 {
		return fmt.Errorf("job %s reader.columns is required", name)
	}
	if err := validateReaderJoins(job.Reader); err != nil {
		return fmt.Errorf("job %s reader: %w", name, err)
	}
	if job.CanonicalMode() == "incremental" {
		if strings.TrimSpace(job.Reader.Cursor.Column) == "" {
			return fmt.Errorf("job %s reader.cursor.column is required for incremental mode", name)
		}
		if strings.TrimSpace(job.Reader.Cursor.TieBreakerColumn) == "" {
			return fmt.Errorf("job %s reader.cursor.tie_breaker_column is required for incremental mode", name)
		}
	}
	for i, f := range job.Reader.Filters {
		switch strings.TrimSpace(f.Op) {
		case "eq", "in":
		default:
			return fmt.Errorf("job %s reader.filters[%d].op must be eq or in", name, i)
		}
		if strings.TrimSpace(f.Column) == "" {
			return fmt.Errorf("job %s reader.filters[%d].column is required", name, i)
		}
		if f.ValuesFrom != "" && f.ValuesFrom != ValuesFromScope {
			return fmt.Errorf("job %s reader.filters[%d].values_from is unsupported", name, i)
		}
	}

	writerConn, ok := cfg.Connections[job.Writer.Connection]
	if !ok {
		return fmt.Errorf("job %s writer.connection %q is not defined", name, job.Writer.Connection)
	}
	if writerConn.Type != "postgres" && writerConn.Type != "mysql" {
		return fmt.Errorf("job %s writer.connection must reference a postgres or mysql connection", name)
	}
	if strings.TrimSpace(job.Writer.Table) == "" {
		return fmt.Errorf("job %s writer.table is required", name)
	}
	if len(job.Writer.KeyColumns) == 0 {
		return fmt.Errorf("job %s writer.key_columns is required", name)
	}
	if len(job.Writer.UpdateColumns) == 0 {
		return fmt.Errorf("job %s writer.update_columns is required", name)
	}
	for _, column := range append(append([]string{}, job.Writer.KeyColumns...), job.Writer.UpdateColumns...) {
		if _, err := dbutil.QuoteIdentifier(column); err != nil {
			return fmt.Errorf("job %s writer column %s: %w", name, column, err)
		}
	}
	for _, column := range job.Writer.JSONMergePatchColumns {
		if _, err := dbutil.QuoteIdentifier(column); err != nil {
			return fmt.Errorf("job %s writer.json_merge_patch_columns %s: %w", name, column, err)
		}
		found := false
		for _, update := range job.Writer.UpdateColumns {
			if strings.TrimSpace(update) == strings.TrimSpace(column) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("job %s writer.json_merge_patch_columns %s must also be listed in update_columns", name, column)
		}
	}
	switch strings.TrimSpace(job.Writer.NullUpdatePolicy) {
	case "", "overwrite", "keep_existing":
	default:
		return fmt.Errorf("job %s writer.null_update_policy must be overwrite or keep_existing", name)
	}
	if strings.TrimSpace(job.Writer.Timezone) == "" {
		job.Writer.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(strings.TrimSpace(job.Writer.Timezone)); err != nil {
		return fmt.Errorf("job %s writer.timezone %q is invalid: %w", name, job.Writer.Timezone, err)
	}

	if err := mapping.ValidateConfig(job.Mapping); err != nil {
		return fmt.Errorf("job %s mapping: %w", name, err)
	}
	return nil
}

func validateReaderJoins(reader ReaderConfig) error {
	baseAlias := strings.TrimSpace(reader.Alias)
	if baseAlias != "" {
		if err := validateAlias(baseAlias); err != nil {
			return fmt.Errorf("reader.alias: %w", err)
		}
	}
	if len(reader.Joins) == 0 {
		return nil
	}
	if baseAlias == "" {
		return fmt.Errorf("reader.alias is required when joins are configured")
	}

	knownAliases := map[string]struct{}{baseAlias: {}}
	for i, join := range reader.Joins {
		joinType := strings.TrimSpace(join.Type)
		switch joinType {
		case "inner", "left":
		default:
			return fmt.Errorf("joins[%d].type must be inner or left", i)
		}
		if _, err := dbutil.QuoteIdentifier(join.Table); err != nil {
			return fmt.Errorf("joins[%d].table: %w", i, err)
		}
		joinAlias := strings.TrimSpace(join.Alias)
		if err := validateAlias(joinAlias); err != nil {
			return fmt.Errorf("joins[%d].alias: %w", i, err)
		}
		if _, exists := knownAliases[joinAlias]; exists {
			return fmt.Errorf("joins[%d].alias %q is duplicated", i, joinAlias)
		}

		leftAlias, err := joinReferenceAlias(join.On.Left)
		if err != nil {
			return fmt.Errorf("joins[%d].on.left: %w", i, err)
		}
		rightAlias, err := joinReferenceAlias(join.On.Right)
		if err != nil {
			return fmt.Errorf("joins[%d].on.right: %w", i, err)
		}
		available := map[string]struct{}{}
		for alias := range knownAliases {
			available[alias] = struct{}{}
		}
		available[joinAlias] = struct{}{}
		for _, alias := range []string{leftAlias, rightAlias} {
			if _, exists := available[alias]; !exists {
				return fmt.Errorf("joins[%d].on references unknown alias %q", i, alias)
			}
		}
		if leftAlias != joinAlias && rightAlias != joinAlias {
			return fmt.Errorf("joins[%d].on must reference join alias %q", i, joinAlias)
		}
		if leftAlias == joinAlias && rightAlias == joinAlias {
			return fmt.Errorf("joins[%d].on must connect join alias %q to a previously declared alias", i, joinAlias)
		}
		knownAliases[joinAlias] = struct{}{}
	}

	resultColumns := map[string]struct{}{}
	selectedSources := map[string]struct{}{}
	for i, column := range reader.Columns {
		resultColumn, err := validateJoinedColumnReference(column, knownAliases)
		if err != nil {
			return fmt.Errorf("columns[%d] %w", i, err)
		}
		if _, exists := resultColumns[resultColumn]; exists {
			return fmt.Errorf("result column %q is duplicated", resultColumn)
		}
		resultColumns[resultColumn] = struct{}{}
		selectedSources[strings.TrimSpace(column)] = struct{}{}
	}
	for i, filter := range reader.Filters {
		if _, err := validateJoinedColumnReference(filter.Column, knownAliases); err != nil {
			return fmt.Errorf("filters[%d] %w", i, err)
		}
		filterAlias := strings.Split(strings.TrimSpace(filter.Column), ".")[0]
		if filterAlias != baseAlias {
			return fmt.Errorf("filters[%d] must reference base alias %q", i, baseAlias)
		}
	}
	if err := validateJoinedCursorReference("cursor.column", reader.Cursor.Column, baseAlias, knownAliases, selectedSources); err != nil {
		return err
	}
	if err := validateJoinedCursorReference("cursor.tie_breaker_column", reader.Cursor.TieBreakerColumn, baseAlias, knownAliases, selectedSources); err != nil {
		return err
	}
	for i, column := range reader.OrderBy {
		if err := validateJoinedOrderReference(column, baseAlias, knownAliases); err != nil {
			return fmt.Errorf("order_by[%d] %w", i, err)
		}
	}
	return nil
}

func validateJoinedColumnReference(column string, knownAliases map[string]struct{}) (string, error) {
	column = strings.TrimSpace(column)
	parts := strings.Split(column, ".")
	if len(parts) != 2 {
		return "", fmt.Errorf("must be alias.column")
	}
	for _, part := range parts {
		if _, err := dbutil.QuoteIdentifier(part); err != nil {
			return "", err
		}
	}
	if _, exists := knownAliases[parts[0]]; !exists {
		return "", fmt.Errorf("references unknown alias %q", parts[0])
	}
	return parts[1], nil
}

func validateJoinedCursorReference(label string, column string, baseAlias string, knownAliases map[string]struct{}, selectedSources map[string]struct{}) error {
	if strings.TrimSpace(column) == "" {
		return nil
	}
	canonical, alias, err := resolveJoinedReference(column, baseAlias, knownAliases)
	if err != nil {
		return fmt.Errorf("%s %w", label, err)
	}
	if alias != baseAlias {
		return fmt.Errorf("%s must reference base alias %q", label, baseAlias)
	}
	if _, selected := selectedSources[canonical]; !selected {
		return fmt.Errorf("%s must be selected as %s", label, canonical)
	}
	return nil
}

func validateJoinedOrderReference(column string, baseAlias string, knownAliases map[string]struct{}) error {
	_, _, err := resolveJoinedReference(column, baseAlias, knownAliases)
	return err
}

func resolveJoinedReference(column string, baseAlias string, knownAliases map[string]struct{}) (string, string, error) {
	column = strings.TrimSpace(column)
	if _, err := dbutil.QuoteIdentifier(column); err != nil {
		return "", "", err
	}
	parts := strings.Split(column, ".")
	switch len(parts) {
	case 1:
		return baseAlias + "." + parts[0], baseAlias, nil
	case 2:
		if _, exists := knownAliases[parts[0]]; !exists {
			return "", "", fmt.Errorf("references unknown alias %q", parts[0])
		}
		return column, parts[0], nil
	default:
		return "", "", fmt.Errorf("must be column or alias.column")
	}
}

func validateAlias(alias string) error {
	if strings.TrimSpace(alias) == "" {
		return fmt.Errorf("is required")
	}
	if strings.Contains(alias, ".") {
		return fmt.Errorf("must be a single identifier")
	}
	if _, err := dbutil.QuoteIdentifier(alias); err != nil {
		return err
	}
	return nil
}

func joinReferenceAlias(column string) (string, error) {
	column = strings.TrimSpace(column)
	parts := strings.Split(column, ".")
	if len(parts) != 2 {
		return "", fmt.Errorf("must be alias.column")
	}
	for _, part := range parts {
		if _, err := dbutil.QuoteIdentifier(part); err != nil {
			return "", err
		}
	}
	return parts[0], nil
}

// IsEnvVarName reports whether name is a valid environment variable name.
func IsEnvVarName(name string) bool {
	return envVarNamePattern.MatchString(name)
}

func validateEnvVarName(field string, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if !IsEnvVarName(value) {
		return fmt.Errorf("%s must be an environment variable name", field)
	}
	return nil
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		if value.Value == "" {
			return nil
		}
		parsed, err := time.ParseDuration(value.Value)
		if err != nil {
			return err
		}
		d.Duration = parsed
		return nil
	default:
		return fmt.Errorf("duration must be a string such as 30s")
	}
}

func (d Duration) MarshalYAML() (any, error) {
	return d.Duration.String(), nil
}
