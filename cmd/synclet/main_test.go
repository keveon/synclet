package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/keveon/synclet/internal/config"
)

func TestParseOptionsDefaults(t *testing.T) {
	opts, err := parseOptions(nil)
	if err != nil {
		t.Fatalf("parseOptions(nil) error = %v", err)
	}
	if opts.configPath != defaultConfigPath {
		t.Errorf("default configPath = %q, want %q", opts.configPath, defaultConfigPath)
	}
	if opts.once || opts.showVersion || opts.showHelp {
		t.Errorf("unexpected defaults: %+v", opts)
	}
}

func TestParseOptionsAcceptsLongOptions(t *testing.T) {
	opts, err := parseOptions([]string{"--config", "config.yaml", "--once"})
	if err != nil {
		t.Fatalf("parseOptions error = %v", err)
	}
	if opts.configPath != "config.yaml" || !opts.once {
		t.Errorf("unexpected options: %+v", opts)
	}
}

func TestParseOptionsAcceptsConfigEqualsForm(t *testing.T) {
	opts, err := parseOptions([]string{"--config=config.yaml"})
	if err != nil {
		t.Fatalf("parseOptions error = %v", err)
	}
	if opts.configPath != "config.yaml" {
		t.Errorf("configPath = %q, want config.yaml", opts.configPath)
	}
}

func TestParseOptionsHelpWinsImmediately(t *testing.T) {
	opts, err := parseOptions([]string{"--once", "--help"})
	if err != nil {
		t.Fatalf("parseOptions error = %v", err)
	}
	if !opts.showHelp {
		t.Error("--help should set showHelp")
	}
}

func TestParseOptionsRejectsSingleDash(t *testing.T) {
	for _, arg := range []string{"-once", "-help", "-config"} {
		if _, err := parseOptions([]string{arg}); err == nil {
			t.Errorf("single-dash option %q must be rejected", arg)
		}
	}
}

func TestParseOptionsRejectsUnknownPositionalAndMissingValues(t *testing.T) {
	for _, args := range [][]string{
		{"extra"},
		{"--nonsense"},
		{"--config"},
		{"--config", "--once"},
		{"--config="},
	} {
		if _, err := parseOptions(args); err == nil {
			t.Errorf("parseOptions(%q) should fail", args)
		}
	}
}

func TestRunHelpSucceedsAndPrintsUsage(t *testing.T) {
	var buf bytes.Buffer
	if err := run(context.Background(), options{showHelp: true}, &buf, &buf); err != nil {
		t.Fatalf("--help must not fail: %v", err)
	}
	if !strings.Contains(buf.String(), "Usage:") {
		t.Error("--help must print usage")
	}
	if strings.Contains(buf.String(), "Only long options") {
		t.Error("usage must not carry implementation notes")
	}
}

func TestRunVersionSucceeds(t *testing.T) {
	var buf bytes.Buffer
	if err := run(context.Background(), options{showVersion: true}, &buf, &buf); err != nil {
		t.Fatalf("--version error = %v", err)
	}
	if !strings.Contains(buf.String(), "synclet") {
		t.Error("--version must print the binary name")
	}
}

func TestRunMissingConfigFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), options{configPath: "/nonexistent/synclet-test-config.yaml"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run without a config must fail")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error should mention config loading, got %v", err)
	}
}

func TestRequiredEnvRejectsInvalidNames(t *testing.T) {
	if _, err := requiredEnv(""); err == nil {
		t.Error("empty name must be rejected")
	}
	if _, err := requiredEnv("1BAD-NAME"); err == nil {
		t.Error("invalid name must be rejected")
	}
	t.Setenv("SYNCLET_TEST_ENV", "")
	if _, err := requiredEnv("SYNCLET_TEST_ENV"); err == nil {
		t.Error("unset/empty variable must be rejected")
	}
}

func TestConnectionNamesRequireConsistency(t *testing.T) {
	jobs := []config.JobConfig{
		{Reader: config.ReaderConfig{Connection: "source"}, Writer: config.WriterConfig{Connection: "target"}},
		{Reader: config.ReaderConfig{Connection: "other"}, Writer: config.WriterConfig{Connection: "target"}},
	}
	if _, _, err := connectionNames(jobs); err == nil {
		t.Error("mixed reader connections must be rejected")
	}
}
