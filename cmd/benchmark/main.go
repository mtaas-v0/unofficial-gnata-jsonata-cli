package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type TestCase struct {
	Expr            string          `json:"expr"`
	ExprFile        string          `json:"expr-file"`
	Dataset         string          `json:"dataset"`
	Data            json.RawMessage `json:"data"`
	Result          json.RawMessage `json:"result"`
	UndefinedResult bool            `json:"undefinedResult"`
	Code            string          `json:"code"`
}

type TestResult struct {
	Group       string
	CaseName    string
	Expression  string
	Duration    time.Duration
	Passed      bool
	ExpectedErr bool
	ErrorMsg    string
}

type GroupStats struct {
	Name      string
	Total     int
	Passed    int
	Failed    int
	TotalTime time.Duration
	MinTime   time.Duration
	MaxTime   time.Duration
}

func main() {
	binPath := flag.String("bin", "./bin/jsonata", "Path to compiled jsonata binary")
	suiteDir := flag.String("suite", "", "Path to official jsonata test-suite directory")
	summaryFile := flag.String("summary", os.Getenv("GITHUB_STEP_SUMMARY"), "Path to write GitHub Step Summary")
	workers := flag.Int("workers", runtime.NumCPU(), "Number of parallel benchmark workers")
	flag.Parse()

	if *suiteDir == "" {
		fmt.Fprintf(os.Stderr, "Error: -suite path is required\n")
		os.Exit(1)
	}

	absBin, err := filepath.Abs(*binPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving binary path: %v\n", err)
		os.Exit(1)
	}

	groupsDir := filepath.Join(*suiteDir, "groups")
	datasetsDir := filepath.Join(*suiteDir, "datasets")

	// Pre-load datasets into memory
	datasets := loadDatasets(datasetsDir)

	// Collect all test cases
	type task struct {
		group    string
		caseFile string
		casePath string
		tc       TestCase
	}

	var tasks []task
	err = filepath.Walk(groupsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}

		rel, _ := filepath.Rel(groupsDir, path)
		group := filepath.Dir(rel)
		caseFile := filepath.Base(path)

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		var tc TestCase
		if err := json.Unmarshal(content, &tc); err != nil {
			return nil
		}

		tasks = append(tasks, task{
			group:    group,
			caseFile: caseFile,
			casePath: path,
			tc:       tc,
		})
		return nil
	})

	if err != nil || len(tasks) == 0 {
		fmt.Fprintf(os.Stderr, "No test cases found in %s\n", groupsDir)
		os.Exit(1)
	}

	fmt.Printf("Loaded %d test cases across official suite. Running on %d workers...\n", len(tasks), *workers)

	// Create temp directory for scratch json data files
	tmpDir, err := os.MkdirTemp("", "jsonata-bench-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	// Worker Pool Execution
	taskChan := make(chan task, len(tasks))
	resultChan := make(chan TestResult, len(tasks))
	var wg sync.WaitGroup

	overallStart := time.Now()

	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for t := range taskChan {
				res := runSingleTest(absBin, t.group, t.caseFile, t.casePath, t.tc, datasets, tmpDir, workerID)
				resultChan <- res
			}
		}(i)
	}

	for _, t := range tasks {
		taskChan <- t
	}
	close(taskChan)

	wg.Wait()
	close(resultChan)

	totalDuration := time.Since(overallStart)

	// Aggregate Results
	var results []TestResult
	groupMap := make(map[string]*GroupStats)
	totalPassed, totalFailed := 0, 0

	for r := range resultChan {
		results = append(results, r)
		if r.Passed {
			totalPassed++
		} else {
			totalFailed++
		}

		gs, ok := groupMap[r.Group]
		if !ok {
			gs = &GroupStats{
				Name:    r.Group,
				MinTime: time.Hour,
			}
			groupMap[r.Group] = gs
		}

		gs.Total++
		if r.Passed {
			gs.Passed++
		} else {
			gs.Failed++
		}
		gs.TotalTime += r.Duration
		if r.Duration < gs.MinTime {
			gs.MinTime = r.Duration
		}
		if r.Duration > gs.MaxTime {
			gs.MaxTime = r.Duration
		}
	}

	// Generate Markdown Summary
	summaryMD := generateMarkdownSummary(results, groupMap, totalPassed, totalFailed, totalDuration)

	// Output to GitHub Actions Step Summary
	if *summaryFile != "" {
		if err := os.WriteFile(*summaryFile, []byte(summaryMD), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to write summary: %v\n", err)
		} else {
			fmt.Printf("Summary successfully written to %s\n", *summaryFile)
		}
	}

	// Print CLI summary to stdout
	fmt.Printf("\n=== BENCHMARK COMPLETE ===\n")
	fmt.Printf("Total Tests:  %d\n", len(results))
	fmt.Printf("Passed:       %d\n", totalPassed)
	fmt.Printf("Failed:       %d\n", totalFailed)
	fmt.Printf("Pass Rate:    %.2f%%\n", float64(totalPassed)/float64(len(results))*100)
	fmt.Printf("Total Time:   %v\n", totalDuration)
	fmt.Printf("Avg Time:     %v\n", totalDuration/time.Duration(len(results)))
}

func runSingleTest(bin, group, caseFile, casePath string, tc TestCase, datasets map[string][]byte, tmpDir string, workerID int) TestResult {
	// 1. Resolve Expression
	expr := tc.Expr
	if expr == "" && tc.ExprFile != "" {
		exprBytes, err := os.ReadFile(filepath.Join(filepath.Dir(casePath), tc.ExprFile))
		if err == nil {
			expr = string(exprBytes)
		}
	}

	// 2. Resolve Input Data File
	var dataBytes []byte
	if len(tc.Data) > 0 && string(tc.Data) != "null" {
		dataBytes = tc.Data
	} else if tc.Dataset != "" {
		dataBytes = datasets[tc.Dataset]
	}
	if len(dataBytes) == 0 {
		dataBytes = []byte("{}")
	}

	dataFile := filepath.Join(tmpDir, fmt.Sprintf("data_w%d_%s_%s.json", workerID, group, caseFile))
	_ = os.WriteFile(dataFile, dataBytes, 0600)
	defer os.Remove(dataFile)

	// 3. Execute Binary with Monotonic High-Resolution Timing
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, expr, "-f", dataFile)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	// 4. Validate Result against Spec
	passed := false
	var errMsg string

	if tc.Code != "" {
		// Expecting execution/syntax error
		if err != nil {
			passed = true
		} else {
			errMsg = fmt.Sprintf("Expected error %q, but CLI succeeded with: %s", tc.Code, stdout.String())
		}
	} else if tc.UndefinedResult {
		// Expecting undefined -> zero output
		if err == nil && len(strings.TrimSpace(stdout.String())) == 0 {
			passed = true
		} else {
			errMsg = fmt.Sprintf("Expected undefined (no output), got: %s (err: %v)", stdout.String(), err)
		}
	} else if len(tc.Result) > 0 {
		if err != nil {
			errMsg = fmt.Sprintf("Command error: %v, stderr: %s", err, stderr.String())
		} else {
			var actualVal, expectedVal any
			if errA := json.Unmarshal(stdout.Bytes(), &actualVal); errA != nil {
				errMsg = fmt.Sprintf("Invalid JSON output: %s", stdout.String())
			} else if errE := json.Unmarshal(tc.Result, &expectedVal); errE != nil {
				errMsg = fmt.Sprintf("Invalid expected JSON fixture: %s", string(tc.Result))
			} else if deepEqualJSON(actualVal, expectedVal) {
				passed = true
			} else {
				errMsg = fmt.Sprintf("Mismatch.\nExpected: %s\nGot:      %s", string(tc.Result), stdout.String())
			}
		}
	} else {
		// Default: successful execution
		if err == nil {
			passed = true
		} else {
			errMsg = stderr.String()
		}
	}

	return TestResult{
		Group:       group,
		CaseName:    caseFile,
		Expression:  expr,
		Duration:    duration,
		Passed:      passed,
		ExpectedErr: tc.Code != "",
		ErrorMsg:    errMsg,
	}
}

func deepEqualJSON(a, b any) bool {
	if a == nil || b == nil {
		return a == b
	}
	switch vA := a.(type) {
	case float64:
		if vB, ok := b.(float64); ok {
			return math.Abs(vA-vB) < 1e-9
		}
		return false
	case map[string]any:
		vB, ok := b.(map[string]any)
		if !ok || len(vA) != len(vB) {
			return false
		}
		for k, valA := range vA {
			valB, exists := vB[k]
			if !exists || !deepEqualJSON(valA, valB) {
				return false
			}
		}
		return true
	case []any:
		vB, ok := b.([]any)
		if !ok || len(vA) != len(vB) {
			return false
		}
		for i := range vA {
			if !deepEqualJSON(vA[i], vB[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}

func loadDatasets(dir string) map[string][]byte {
	datasets := make(map[string][]byte)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return datasets
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			name := strings.TrimSuffix(e.Name(), ".json")
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err == nil {
				datasets[name] = data
			}
		}
	}
	return datasets
}

func generateMarkdownSummary(results []TestResult, groups map[string]*GroupStats, passed, failed int, totalDuration time.Duration) string {
	var sb strings.Builder
	total := passed + failed
	passRate := float64(passed) / float64(total) * 100
	avgTime := totalDuration / time.Duration(total)

	statusIcon := "🟢"
	if passRate < 95.0 {
		statusIcon = "🟡"
	}
	if passRate < 80.0 {
		statusIcon = "🔴"
	}

	sb.WriteString("# " + statusIcon + " Official JSONata Test Suite Benchmark Report\n\n")
	sb.WriteString("> **Environment**: Ubuntu 22.04 LTS (`x86_64`) | **Engine**: `gnata` (Pure Go)\n\n")

	// Overview KPI Table
	sb.WriteString("### 📊 High-Level Metrics\n\n")
	sb.WriteString("| Metric | Value |\n")
	sb.WriteString("| :--- | :--- |\n")
	sb.WriteString(fmt.Sprintf("| **Total Test Cases** | `%d` |\n", total))
	sb.WriteString(fmt.Sprintf("| **Passed (✅)** | `%d` |\n", passed))
	sb.WriteString(fmt.Sprintf("| **Failed (❌)** | `%d` |\n", failed))
	sb.WriteString(fmt.Sprintf("| **Pass Rate** | `%.2f%%` |\n", passRate))
	sb.WriteString(fmt.Sprintf("| **Total Benchmark Duration** | `%v` |\n", totalDuration.Round(time.Millisecond)))
	sb.WriteString(fmt.Sprintf("| **Average Latency / Test** | `%v` |\n\n", avgTime))

	// Group Breakdown
	sb.WriteString("### 📁 Test Group Breakdown\n\n")
	sb.WriteString("| Test Group | Total | Passed | Failed | Pass Rate | Total Time | Avg Latency |\n")
	sb.WriteString("| :--- | :--- | :--- | :--- | :--- | :--- | :--- |\n")

	var sortedGroups []*GroupStats
	for _, g := range groups {
		sortedGroups = append(sortedGroups, g)
	}
	sort.Slice(sortedGroups, func(i, j int) bool { return sortedGroups[i].Name < sortedGroups[j].Name })

	for _, g := range sortedGroups {
		grpRate := float64(g.Passed) / float64(g.Total) * 100
		grpAvg := g.TotalTime / time.Duration(g.Total)
		badge := "✅"
		if g.Failed > 0 {
			badge = "⚠️"
		}
		sb.WriteString(fmt.Sprintf("| %s **%s** | %d | %d | %d | `%.1f%%` | `%v` | `%v` |\n",
			badge, g.Name, g.Total, g.Passed, g.Failed, grpRate, g.TotalTime.Round(time.Millisecond), grpAvg))
	}
	sb.WriteString("\n")

	// Top 10 Slowest Tests Table
	sort.Slice(results, func(i, j int) bool { return results[i].Duration > results[j].Duration })
	sb.WriteString("### ⏱️ Top 10 Slowest Evaluations\n\n")
	sb.WriteString("| Rank | Group / Case | Expression | Duration | Status |\n")
	sb.WriteString("| :--- | :--- | :--- | :--- | :--- |\n")
	limit := 10
	if len(results) < limit {
		limit = len(results)
	}
	for i := 0; i < limit; i++ {
		r := results[i]
		st := "✅ Passed"
		if !r.Passed {
			st = "❌ Failed"
		}
		cleanExpr := strings.ReplaceAll(r.Expression, "\n", " ")
		if len(cleanExpr) > 40 {
			cleanExpr = cleanExpr[:37] + "..."
		}
		sb.WriteString(fmt.Sprintf("| #%d | `%s/%s` | `%s` | `%v` | %s |\n",
			i+1, r.Group, r.CaseName, cleanExpr, r.Duration, st))
	}
	sb.WriteString("\n")

	// Collapsible Failure Details
	if failed > 0 {
		sb.WriteString("<details>\n<summary><b>🔍 View Failed Test Details (" + fmt.Sprintf("%d", failed) + " cases)</b></summary>\n\n")
		sb.WriteString("| Group / Case | Expression | Error Details |\n")
		sb.WriteString("| :--- | :--- | :--- |\n")
		for _, r := range results {
			if !r.Passed {
				cleanExpr := strings.ReplaceAll(r.Expression, "\n", " ")
				cleanErr := strings.ReplaceAll(r.ErrorMsg, "\n", "<br>")
				sb.WriteString(fmt.Sprintf("| `%s/%s` | `%s` | <pre>%s</pre> |\n",
					r.Group, r.CaseName, cleanExpr, cleanErr))
			}
		}
		sb.WriteString("\n</details>\n")
	}

	return sb.String()
}