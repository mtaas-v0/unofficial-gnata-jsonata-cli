package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/recolabs/gnata"
)

// Injected at build time via -ldflags
var (
	Version      = "dev"     // Our repo tag or "dev"
	Commit       = "none"    // Our repo git commit SHA
	GnataVersion = ""        // Upstream gnata module version
	GnataCommit  = ""        // Upstream gnata git commit SHA
	BuildDate    = "unknown" // Build timestamp
)

func getGnataMetadata() (version, commit string) {
	version = GnataVersion
	commit = GnataCommit

	// Runtime fallback via Go buildinfo if not injected via ldflags
	if version == "" || commit == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, dep := range info.Deps {
				if dep.Path == "github.com/recolabs/gnata" {
					if version == "" {
						if dep.Replace != nil {
							version = dep.Replace.Version
						} else {
							version = dep.Version
						}
					}
					if commit == "" {
						// Extract commit from pseudo-version (e.g., v0.0.0-20240820120000-abcdef123456)
						parts := strings.Split(version, "-")
						if len(parts) >= 3 {
							hash := parts[len(parts)-1]
							if len(hash) >= 7 {
								commit = hash[:7]
							}
						}
					}
					break
				}
			}
		}
	}

	if version == "" {
		version = "unknown"
	}
	if commit == "" {
		commit = "unknown"
	}
	return version, commit
}

func printVersion(w io.Writer) {
	gnataVer, gnataCommit := getGnataMetadata()
	fmt.Fprintf(w, "jsonata version %s (commit: %s, gnata-version: %s, gnata-commit: %s, built: %s)\n",
		Version, Commit, gnataVer, gnataCommit, BuildDate)
}

type Options struct {
	FilePath    string
	ShowVersion bool
	ShowHelp    bool
	Expression  string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "jsonata: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	opts, err := parseArgs(args)
	if err != nil {
		return err
	}

	if opts.ShowHelp {
		printHelp(os.Stdout)
		return nil
	}

	if opts.ShowVersion {
		printVersion(os.Stdout)
		return nil
	}

	stat, err := os.Stdin.Stat()
	hasStdin := (err == nil) && ((stat.Mode() & os.ModeCharDevice) == 0)

	var exprStr string
	var jsonBytes []byte

	if opts.Expression != "" {
		exprStr = opts.Expression

		if opts.FilePath != "" && opts.FilePath != "-" {
			data, err := os.ReadFile(opts.FilePath)
			if err != nil {
				return fmt.Errorf("failed to read data file %q: %w", opts.FilePath, err)
			}
			jsonBytes = data
		} else if hasStdin || opts.FilePath == "-" {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf("failed to read JSON data from stdin: %w", err)
			}
			if len(strings.TrimSpace(string(data))) == 0 {
				return errors.New("stdin was empty; expected JSON data payload")
			}
			jsonBytes = data
		} else {
			return errors.New("no JSON input data provided (use -f/--file <file> or pipe data via stdin)")
		}
	} else {
		if !hasStdin {
			printHelp(os.Stderr)
			return errors.New("no expression or input stream provided")
		}

		stdinExpr, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read JSONata expression from stdin: %w", err)
		}

		exprStr = strings.TrimSpace(string(stdinExpr))
		if exprStr == "" {
			return errors.New("received empty JSONata expression from stdin")
		}

		if opts.FilePath == "" {
			return errors.New("the -f/--file flag is required to supply data when the JSONata expression is piped via stdin")
		}
		if opts.FilePath == "-" {
			return errors.New("cannot read both expression and JSON data from stdin simultaneously")
		}

		data, err := os.ReadFile(opts.FilePath)
		if err != nil {
			return fmt.Errorf("failed to read data file %q: %w", opts.FilePath, err)
		}
		jsonBytes = data
	}

	expr, err := gnata.Compile(exprStr)
	if err != nil {
		return fmt.Errorf("compile error: %w", err)
	}

	result, err := expr.EvalBytes(context.Background(), jsonBytes)
	if err != nil {
		return fmt.Errorf("eval error: %w", err)
	}

	if result == nil {
		return nil
	}

	var out []byte
	switch v := result.(type) {
	case json.RawMessage:
		out = v
	case []byte:
		out = v
	default:
		out, err = json.Marshal(result)
		if err != nil {
			return fmt.Errorf("failed to serialize result to JSON: %w", err)
		}
	}

	if _, err := os.Stdout.Write(out); err != nil {
		return fmt.Errorf("failed to write stdout: %w", err)
	}
	if _, err := os.Stdout.WriteString("\n"); err != nil {
		return fmt.Errorf("failed to write trailing newline: %w", err)
	}

	return nil
}

func parseArgs(args []string) (*Options, error) {
	opts := &Options{}
	var positional []string
	skipNext := false

	for i := 0; i < len(args); i++ {
		if skipNext {
			skipNext = false
			continue
		}
		arg := args[i]

		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}

		if arg == "-v" || arg == "--version" {
			opts.ShowVersion = true
			continue
		}

		if arg == "-h" || arg == "--help" {
			opts.ShowHelp = true
			continue
		}

		if arg == "-f" || arg == "--file" {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("flag %s requires a file path argument", arg)
			}
			opts.FilePath = args[i+1]
			skipNext = true
			continue
		}

		if strings.HasPrefix(arg, "-f=") {
			opts.FilePath = strings.TrimPrefix(arg, "-f=")
			continue
		}

		if strings.HasPrefix(arg, "--file=") {
			opts.FilePath = strings.TrimPrefix(arg, "--file=")
			continue
		}

		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			return nil, fmt.Errorf("unknown flag: %s", arg)
		}

		positional = append(positional, arg)
	}

	if len(positional) > 0 {
		opts.Expression = positional[0]
		if len(positional) > 1 {
			return nil, fmt.Errorf("unexpected extra argument: %s", positional[1])
		}
	}

	return opts, nil
}

func printHelp(w io.Writer) {
	fmt.Fprintf(w, `JSONata Command Line Interface (powered by gnata)

Usage:
  jsonata [flags] <expression>
  jsonata -f <data.json> < <transform.jsonata>

Flags:
  -f, --file <path>    Input JSON file path (use '-' for stdin)
  -v, --version        Print version information and exit
  -h, --help           Show this help message and exit
`)
}
