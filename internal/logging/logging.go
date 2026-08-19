package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"golang.org/x/term"
)

type Mode string

const (
	Auto   Mode = "auto"
	Always Mode = "always"
	Never  Mode = "never"
)

type Options struct {
	Mode     Mode
	Terminal bool
	NoColor  bool
}

type labelStyle struct {
	prefix string
	label  string
	ansi   string
}

var labelStyles = []labelStyle{
	{prefix: "sync failed:", label: "sync failed", ansi: "\x1b[1;31m"},
	{prefix: "synclet:", label: "synclet:", ansi: "\x1b[1;31m"},
	{prefix: "job complete ", label: "job complete", ansi: "\x1b[1;32m"},
	{prefix: "job read ", label: "job read", ansi: "\x1b[1;34m"},
	{prefix: "job start ", label: "job start", ansi: "\x1b[1;36m"},
}

const reset = "\x1b[0m"

// ParseMode parses SYNCLET_LOG_COLOR values.
func ParseMode(raw string) (Mode, error) {
	mode := Mode(strings.ToLower(strings.TrimSpace(raw)))
	if mode == "" {
		return Auto, nil
	}
	switch mode {
	case Auto, Always, Never:
		return mode, nil
	default:
		return "", fmt.Errorf("SYNCLET_LOG_COLOR must be auto, always, or never")
	}
}

// IsTerminal reports whether the writer is a terminal.
func IsTerminal(output io.Writer) bool {
	file, ok := output.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

// Logger writes structured, greppable event lines with fixed label
// colors. It never logs DSNs, SQL parameters, checkpoint values or
// business payloads.
type Logger struct {
	base  *log.Logger
	color bool
}

// New builds a Logger.
func New(output io.Writer, opts Options) *Logger {
	if output == nil {
		output = io.Discard
	}
	return &Logger{
		base:  log.New(output, "", log.LstdFlags),
		color: colorEnabled(opts),
	}
}

// Printf writes one event line.
func (l *Logger) Printf(format string, args ...any) {
	l.base.Printf(l.colorize(format), args...)
}

// Fatalf writes one event line and exits.
func (l *Logger) Fatalf(format string, args ...any) {
	l.base.Fatalf(l.colorize(format), args...)
}

func (l *Logger) colorize(format string) string {
	if !l.color {
		return format
	}
	for _, style := range labelStyles {
		if strings.HasPrefix(format, style.prefix) {
			return style.ansi + style.label + reset + format[len(style.label):]
		}
	}
	return format
}

func colorEnabled(opts Options) bool {
	if opts.NoColor {
		return false
	}
	switch opts.Mode {
	case Always:
		return true
	case Never:
		return false
	case Auto, "":
		return opts.Terminal
	default:
		return false
	}
}
