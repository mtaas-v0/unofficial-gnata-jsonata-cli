package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantExpr    string
		wantFile    string
		wantVersion bool
		wantHelp    bool
		wantErr     bool
	}{
		{
			name:     "Case 1: Inline expression with -f file",
			args:     []string{"Account.Order", "-f", "data.json"},
			wantExpr: "Account.Order",
			wantFile: "data.json",
		},
		{
			name:     "Case 1 (equals syntax): Inline expression with --file=file",
			args:     []string{"Account.Order", "--file=data.json"},
			wantExpr: "Account.Order",
			wantFile: "data.json",
		},
		{
			name:     "Case 2: Inline expression only (stdin data)",
			args:     []string{"Account.Order"},
			wantExpr: "Account.Order",
		},
		{
			name:     "Case 3: Piped expression with -f file",
			args:     []string{"-f", "data.json"},
			wantFile: "data.json",
		},
		{
			name:        "Version flag",
			args:        []string{"-v"},
			wantVersion: true,
		},
		{
			name:     "Help flag",
			args:     []string{"--help"},
			wantHelp: true,
		},
		{
			name:    "Missing file argument after -f",
			args:    []string{"-f"},
			wantErr: true,
		},
		{
			name:    "Unknown flag",
			args:    []string{"--invalid-flag"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := parseArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseArgs() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if opts.Expression != tt.wantExpr {
				t.Errorf("opts.Expression = %q, want %q", opts.Expression, tt.wantExpr)
			}
			if opts.FilePath != tt.wantFile {
				t.Errorf("opts.FilePath = %q, want %q", opts.FilePath, tt.wantFile)
			}
			if opts.ShowVersion != tt.wantVersion {
				t.Errorf("opts.ShowVersion = %v, want %v", opts.ShowVersion, tt.wantVersion)
			}
			if opts.ShowHelp != tt.wantHelp {
				t.Errorf("opts.ShowHelp = %v, want %v", opts.ShowHelp, tt.wantHelp)
			}
		})
	}
}

func TestRun_FileEvaluation(t *testing.T) {
	// Create temporary JSON test file
	tmpDir := t.TempDir()
	jsonPath := filepath.Join(tmpDir, "data.json")
	sampleJSON := `{"Account": {"Order": [{"id": 101, "price": 42.5}]}}`
	if err := os.WriteFile(jsonPath, []byte(sampleJSON), 0600); err != nil {
		t.Fatalf("failed to create temp json file: %v", err)
	}

	// Test Case 1: jsonata "Account.Order[0].price" -f data.json
	err := run([]string{"Account.Order[0].price", "-f", jsonPath})
	if err != nil {
		t.Fatalf("run() failed: %v", err)
	}
}
