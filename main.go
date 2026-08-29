package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/recolabs/gnata"
)

// Build metadata injected via -ldflags during compilation
var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

// Options holds parsed command-line flags and positional arguments.
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

	// Detect if stdin has data flowing into it (pipe or file redirection)
	stat, err := os.Stdin.Stat()
	hasStdin := (err == nil) && ((stat.Mode() & os.ModeCharDevice) == 0)

	var exprStr string
	var jsonBytes []byte

	if opts.Expression != "" {
		// Case 1 & Case 2: Inline expression supplied as a positional argument
		exprStr = opts.Expression

		if opts.FilePath != "" && opts.FilePath != "-" {
			// Case 1: Read JSON payload from specified file
			data, err := os.ReadFile(opts.FilePath)
			if err != nil {
				return fmt.Errorf("failed to read data file %q: %w", opts.FilePath, err)
			}
			jsonBytes = data
		} else if hasStdin || opts.FilePath == "-" {
			// Case 2: Stream JSON payload from stdin
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
		// Case 3: No inline expression provided; check for piped JSONata instructions
		if !hasStdin {
			printHelp(os.Stderr)
			return errors.New("no expression or input stream provided")
		}

		// Read the transformation expression from stdin
		stdinExpr, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read JSONata expression from stdin: %w", err)
		}

		exprStr = strings.TrimSpace(string(stdinExpr))
		if exprStr == "" {
			return errors.New("received empty JSONata expression from stdin")
		}

		// In Case 3, the data file (-f/--file) is strictly mandatory
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

	// 1. Compile expression once for maximum performance
	expr, err := gnata.Compile(exprStr)
	if err != nil {
		return fmt.Errorf("compile error: %w", err)
	}

	// 2. Evaluate expression directly against raw JSON bytes
	result, err := expr.EvalBytes(context.Background(), jsonBytes)
	if err != nil {
		return fmt.Errorf("eval error: %w", err)
	}

	// If the JSONata expression evaluates to undefined (nil), output nothing
	if result == nil {
		return nil
	}

	// 3. Serialize output to raw JSON and write to stdout with a trailing newline
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

// parseArgs parses POSIX-style CLI arguments and flags in any order.
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
  -v, --version        Print version and exit
  -h, --help           Show this help message and exit

Examples:
  # Case 1: Inline expression and data file
  jsonata "Account.Order" -f data.json

  # Case 2: Inline expression with data from stdin
  cat data.json | jsonata "Account.Order"

  # Case 3: Piped long transformation file with data file
  jsonata -f data.json < transform.jsonata
`)
}

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "jsonata version %s (commit: %s, built: %s, runtime: pure-go/gnata)\n",
		Version, Commit, BuildDate)
}