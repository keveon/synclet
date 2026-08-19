package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
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
	if err := run(context.Background(), options{showHelp: true}, &buf); err != nil {
		t.Fatalf("--help must not fail: %v", err)
	}
	if !strings.Contains(buf.String(), "Usage:") {
		t.Error("--help must print usage")
	}
}

func TestRunVersionSucceeds(t *testing.T) {
	var buf bytes.Buffer
	if err := run(context.Background(), options{showVersion: true}, &buf); err != nil {
		t.Fatalf("--version error = %v", err)
	}
	if !strings.Contains(buf.String(), "synclet") {
		t.Error("--version must print the binary name")
	}
}

func TestRunSyncReturnsNotImplemented(t *testing.T) {
	var buf bytes.Buffer
	err := run(context.Background(), options{configPath: "config.yaml"}, &buf)
	if !errors.Is(err, errNotImplemented) {
		t.Fatalf("run on skeleton should return errNotImplemented, got %v", err)
	}
}
