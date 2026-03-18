package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// testEvent matches the JSON structure emitted by `go test -json`.
type testEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

type lastFailure struct {
	Package string `json:"package"`
	Test    string `json:"test,omitempty"`
}

const lastFailedPath = "build/last-failed.json"

func main() {
	lastFailed := flag.Bool("lf", false, "run only the tests that failed in the previous run")
	flag.BoolVar(lastFailed, "last-failed", false, "run only the tests that failed in the previous run")
	flag.Parse()

	failuresFromDisk, usingLastFailures := prepareLastFailures(*lastFailed)

	args := buildGoTestArgs(failuresFromDisk, usingLastFailures)
	cmd := exec.Command("go", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to capture test output: %v\n", err)
		os.Exit(1)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to capture test errors: %v\n", err)
		os.Exit(1)
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start tests: %v\n", err)
		os.Exit(1)
	}

	// Forward stderr as-is to preserve compiler/runtime diagnostics.
	go io.Copy(os.Stderr, stderr) //nolint:errcheck

	decoder := json.NewDecoder(stdout)

	failedTests := map[string]map[string]struct{}{}
	failedPackages := map[string]struct{}{}

	for {
		var ev testEvent
		if err := decoder.Decode(&ev); err != nil {
			if err == io.EOF {
				break
			}

			fmt.Fprintf(os.Stderr, "failed to decode test output: %v\n", err)
			_ = cmd.Process.Kill()
			os.Exit(1)
		}

		if ev.Action == "output" && ev.Output != "" {
			fmt.Print(ev.Output)
		}

		if ev.Action == "fail" {
			if ev.Test != "" {
				packageFailures, ok := failedTests[ev.Package]
				if !ok {
					packageFailures = map[string]struct{}{}
					failedTests[ev.Package] = packageFailures
				}

				packageFailures[ev.Test] = struct{}{}
			} else if ev.Package != "" {
				failedPackages[ev.Package] = struct{}{}
			}
		}
	}

	err = cmd.Wait()

	printSummary(failedTests, failedPackages)
	if err := persistLastFailures(failedTests, failedPackages); err != nil {
		fmt.Fprintf(os.Stderr, "failed to record failing tests: %v\n", err)
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}

		fmt.Fprintf(os.Stderr, "tests exited with error: %v\n", err)
		os.Exit(1)
	}
}

func buildGoTestArgs(lastFailures []lastFailure, useLastFailures bool) []string {
	args := []string{"test", "-json", "-v", "-race", "-coverprofile=coverage.out"}

	if !useLastFailures || len(lastFailures) == 0 {
		return append(args, "./...")
	}

	packages, runPattern := packagesAndPattern(lastFailures)

	if runPattern != "" {
		args = append(args, "-run", runPattern)
	}

	return append(args, packages...)
}

func printSummary(failedTests map[string]map[string]struct{}, failedPackages map[string]struct{}) {
	if len(failedTests) == 0 && len(failedPackages) == 0 {
		return
	}

	tests := formatFailedTests(failedTests)
	pkgs := keys(failedPackages)

	fmt.Println("\nFailed tests:")

	for _, t := range tests {
		fmt.Printf("  - %s\n", t)
	}

	for _, p := range pkgs {
		fmt.Printf("  - %s (package failure)\n", p)
	}
}

func persistLastFailures(failedTests map[string]map[string]struct{}, failedPackages map[string]struct{}) error {
	failures := make([]lastFailure, 0, len(failedTests)+len(failedPackages))

	for pkg, pkgTests := range failedTests {
		for test := range pkgTests {
			failures = append(failures, lastFailure{
				Package: pkg,
				Test:    test,
			})
		}
	}

	for pkg := range failedPackages {
		if _, hasTests := failedTests[pkg]; hasTests {
			continue
		}

		failures = append(failures, lastFailure{Package: pkg})
	}

	sort.Slice(failures, func(i, j int) bool {
		if failures[i].Package == failures[j].Package {
			return failures[i].Test < failures[j].Test
		}

		return failures[i].Package < failures[j].Package
	})

	if err := os.MkdirAll(filepath.Dir(lastFailedPath), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(failures, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(lastFailedPath, data, 0o644)
}

func formatFailedTests(failedTests map[string]map[string]struct{}) []string {
	out := []string{}

	for pkg, pkgTests := range failedTests {
		for test := range pkgTests {
			out = append(out, fmt.Sprintf("%s %s", pkg, test))
		}
	}

	sort.Strings(out)
	return out
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	sort.Strings(out)
	return out
}

func prepareLastFailures(lastFailed bool) ([]lastFailure, bool) {
	if !lastFailed {
		return nil, false
	}

	failures, err := loadLastFailures()
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(os.Stderr, "No recorded failing tests found; running full suite.")
		return nil, false
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load last failing tests (%v); running full suite.\n", err)
		return nil, false
	}

	if len(failures) == 0 {
		fmt.Fprintln(os.Stderr, "No recorded failing tests found; running full suite.")
		return nil, false
	}

	fmt.Fprintf(
		os.Stderr,
		"Re-running %d last failing test(s) across %d package(s).\n",
		countTests(failures),
		countPackages(failures),
	)

	return failures, true
}

func loadLastFailures() ([]lastFailure, error) {
	data, err := os.ReadFile(lastFailedPath)
	if err != nil {
		return nil, err
	}

	var failures []lastFailure
	if err := json.Unmarshal(data, &failures); err != nil {
		return nil, err
	}

	return failures, nil
}

func countTests(failures []lastFailure) int {
	total := 0

	for _, f := range failures {
		if f.Test != "" {
			total++
		}
	}

	return total
}

func countPackages(failures []lastFailure) int {
	pkgs := map[string]struct{}{}

	for _, f := range failures {
		if f.Package != "" {
			pkgs[f.Package] = struct{}{}
		}
	}

	return len(pkgs)
}

func packagesAndPattern(failures []lastFailure) ([]string, string) {
	pkgs := map[string]struct{}{}
	testPatterns := []string{}

	for _, failure := range failures {
		if failure.Package != "" {
			pkgs[failure.Package] = struct{}{}
		}

		if failure.Test != "" {
			testPatterns = append(testPatterns, regexp.QuoteMeta(failure.Test))
		}
	}

	pkgList := keys(pkgs)

	if len(testPatterns) == 0 {
		return pkgList, ""
	}

	return pkgList, strings.Join(testPatterns, "|")
}
