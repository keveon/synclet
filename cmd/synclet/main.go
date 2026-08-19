// Command synclet is the synclet CLI entrypoint.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/keveon/synclet/internal/checkpoint"
	"github.com/keveon/synclet/internal/config"
	"github.com/keveon/synclet/internal/engine"
	"github.com/keveon/synclet/internal/filter"
	"github.com/keveon/synclet/internal/logging"
	"github.com/keveon/synclet/internal/reader"
	"github.com/keveon/synclet/internal/writer"
)

// version is injected at release time via -ldflags "-X main.version=...".
var version = "dev"

// defaultConfigPath is the FHS runtime default; deployments read the config
// from /etc/synclet/config.yaml.
const defaultConfigPath = "/etc/synclet/config.yaml"

const usage = `Usage: synclet [options]

Options:
  --config <path>   path to the config file (default /etc/synclet/config.yaml)
  --once            run a single sync pass and exit
  --version         print version and exit
  --help            print this help and exit
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
// stdout.
func run(ctx context.Context, opts options, stdout, stderr io.Writer) error {
	switch {
	case opts.showHelp:
		fmt.Fprint(stdout, usage)
		return nil
	case opts.showVersion:
		fmt.Fprintf(stdout, "synclet %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return nil
	}

	logger, err := newLogger(stderr, logging.IsTerminal(stderr), os.Getenv)
	if err != nil {
		return err
	}

	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	readerConnName, writerConnName, err := connectionNames(cfg.Jobs)
	if err != nil {
		return err
	}
	readerConn := cfg.Connections[readerConnName]
	writerConn := cfg.Connections[writerConnName]

	readerDSN, err := requiredEnv(readerConn.DSNEnv)
	if err != nil {
		return err
	}
	writerDSN, err := requiredEnv(writerConn.DSNEnv)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	var source reader.Reader
	switch strings.TrimSpace(readerConn.Type) {
	case "postgres":
		source, err = reader.OpenPostgres(ctx, readerDSN)
	case "mysql":
		source, err = reader.OpenMySQL(ctx, readerDSN)
	default:
		return fmt.Errorf("unsupported reader connection type %q", readerConn.Type)
	}
	if err != nil {
		return err
	}
	defer source.Close()

	var target writer.Writer
	switch strings.TrimSpace(writerConn.Type) {
	case "postgres":
		target, err = writer.OpenPostgres(ctx, writerDSN)
	case "mysql":
		target, err = writer.OpenMySQL(ctx, writerDSN)
	default:
		return fmt.Errorf("unsupported writer connection type %q", writerConn.Type)
	}
	if err != nil {
		return err
	}
	defer target.Close()

	runner, err := engine.New(source, target, engine.Options{
		CheckpointStore: checkpoint.FileStore{Path: cfg.Checkpoint.Path},
		Scope:           filter.New(cfg.Scope),
		PollInterval:    cfg.Sync.PollInterval.Duration,
		BatchSize:       cfg.Sync.BatchSize,
		Jobs:            cfg.Jobs,
		Logger:          logger,
	})
	if err != nil {
		return err
	}

	if opts.once {
		return runner.RunOnce(ctx)
	}

	if err := runner.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// newLogger resolves color mode from the environment.
func newLogger(output io.Writer, terminal bool, getenv func(string) string) (*logging.Logger, error) {
	noColor := getenv("NO_COLOR") != ""
	mode := logging.Never
	if !noColor {
		var err error
		mode, err = logging.ParseMode(getenv("SYNCLET_LOG_COLOR"))
		if err != nil {
			return nil, err
		}
	}
	return logging.New(output, logging.Options{
		Mode:     mode,
		Terminal: terminal,
		NoColor:  noColor,
	}), nil
}

// connectionNames resolves the single reader/writer connection pair all
// jobs must share.
func connectionNames(jobs []config.JobConfig) (string, string, error) {
	if len(jobs) == 0 {
		return "", "", fmt.Errorf("jobs are required")
	}
	readerConn := strings.TrimSpace(jobs[0].Reader.Connection)
	writerConn := strings.TrimSpace(jobs[0].Writer.Connection)
	for _, job := range jobs[1:] {
		if strings.TrimSpace(job.Reader.Connection) != readerConn {
			return "", "", fmt.Errorf("all jobs must use the same reader connection in this version")
		}
		if strings.TrimSpace(job.Writer.Connection) != writerConn {
			return "", "", fmt.Errorf("all jobs must use the same writer connection in this version")
		}
	}
	return readerConn, writerConn, nil
}

// requiredEnv reads a required environment variable by validated name.
func requiredEnv(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("environment variable name is empty")
	}
	if !config.IsEnvVarName(name) {
		return "", fmt.Errorf("environment variable name must be valid")
	}
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("required environment variable %s is not set", name)
	}
	return value, nil
}

func main() {
	opts, err := parseOptions(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "synclet:", err)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err := run(context.Background(), opts, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "synclet:", engine.ErrorSummary(err))
		os.Exit(1)
	}
}
