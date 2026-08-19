// Command synclet is the synclet CLI entrypoint.
//
// Skeleton stage: option parsing and version output only; the sync engine
// (reader -> mapping -> writer) is not implemented yet, and running a sync
// returns an explicit not-implemented error instead of silently no-op'ing.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
)

// version is injected at release time via -ldflags "-X main.version=...".
var version = "dev"

// errNotImplemented makes the skeleton state explicit so an empty run is
// never mistaken for a successful sync.
var errNotImplemented = errors.New("engine not implemented yet: skeleton repository, see README roadmap")

// defaultConfigPath is the FHS runtime default; deployments read the config
// from /etc/synclet/config.yaml.
const defaultConfigPath = "/etc/synclet/config.yaml"

const usage = `Usage: synclet [options]

Options:
  --config <path>   path to the config file (default /etc/synclet/config.yaml)
  --once            run a single sync pass and exit
  --version         print version and exit
  --help            print this help and exit

Only long options (double dash) are accepted.
`

// options holds the parsed command-line options.
type options struct {
	configPath  string
	once        bool
	showVersion bool
	showHelp    bool
}

// parseOptions parses args. Only double-dash long options are accepted:
// single-dash forms and unknown options are errors, and --help exits
// cleanly instead of being treated as a parse failure.
func parseOptions(args []string) (options, error) {
	opts := options{configPath: defaultConfigPath}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help":
			opts.showHelp = true
			return opts, nil
		case arg == "--version":
			opts.showVersion = true
		case arg == "--once":
			opts.once = true
		case arg == "--config":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				return opts, errors.New("--config requires a value")
			}
			i++
			opts.configPath = args[i]
		case strings.HasPrefix(arg, "--config="):
			v := strings.TrimPrefix(arg, "--config=")
			if v == "" {
				return opts, errors.New("--config requires a value")
			}
			opts.configPath = v
		default:
			return opts, fmt.Errorf("unknown option %q: only long options (--config, --once, --version, --help) are accepted", arg)
		}
	}
	return opts, nil
}

// run executes the main flow. --help and --version succeed and print to
// stdout; sync paths return errNotImplemented at the skeleton stage.
func run(ctx context.Context, opts options, stdout io.Writer) error {
	switch {
	case opts.showHelp:
		fmt.Fprint(stdout, usage)
		return nil
	case opts.showVersion:
		fmt.Fprintf(stdout, "synclet %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return nil
	}
	// ctx is reserved for signal handling and timeouts once the engine is
	// implemented.
	_ = ctx
	return errNotImplemented
}

func main() {
	opts, err := parseOptions(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "synclet:", err)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err := run(context.Background(), opts, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "synclet:", err)
		os.Exit(1)
	}
}
