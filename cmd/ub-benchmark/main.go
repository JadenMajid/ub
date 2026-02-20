package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

type commandSpec struct {
	bin  string
	args []string
}

type benchmarkCase struct {
	name    string
	variant string
	prepare []commandSpec
	run     commandSpec
}

type benchmarkResult struct {
	Case      string          `json:"case"`
	Variant   string          `json:"variant"`
	Runs      int             `json:"runs"`
	Successes int             `json:"successes"`
	Failures  int             `json:"failures"`
	Average   time.Duration   `json:"average"`
	Samples   []time.Duration `json:"samples"`
}

type speedupResult struct {
	Case          string  `json:"case"`
	ColdAvgMillis float64 `json:"cold_avg_ms"`
	WarmAvgMillis float64 `json:"warm_avg_ms"`
	WarmupSpeedup float64 `json:"warmup_speedup"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("ub-benchmark", flag.ContinueOnError)
	iterations := fs.Int("iterations", 3, "number of measured runs per benchmark case")
	warmup := fs.Bool("warmup", true, "run one unmeasured warmup per benchmark case")
	includeCursor := fs.Bool("cursor", true, "include cursor cask install/uninstall benchmarks")
	includeFFmpeg := fs.Bool("ffmpeg", true, "include ffmpeg formula install/uninstall benchmarks")
	jsonOut := fs.Bool("json", false, "emit machine-readable JSON output")
	ubArgsRaw := fs.String("ub-args", "run ./cmd/ub/main.go", "arguments used with --ub-bin")
	ubBin := fs.String("ub-bin", "go", "ub command binary")
	timeout := fs.Duration("timeout", 45*time.Minute, "timeout per command")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *iterations <= 0 {
		return fmt.Errorf("iterations must be greater than 0")
	}
	if !*includeCursor && !*includeFFmpeg {
		return fmt.Errorf("at least one benchmark target must be enabled")
	}

	ubBase := commandSpec{bin: *ubBin, args: strings.Fields(*ubArgsRaw)}
	if len(ubBase.args) == 0 {
		return fmt.Errorf("ub args must not be empty")
	}

	cases := buildCases(ubBase, *includeFFmpeg, *includeCursor)
	results := make([]benchmarkResult, 0, len(cases))

	for _, bc := range cases {
		fmt.Printf("\n==> Benchmarking %s (%s)\n", bc.name, bc.variant)
		if *warmup {
			fmt.Println("- warmup run")
			_, _ = runScenario(context.Background(), bc, *timeout, false)
		}
		result := benchmarkResult{Case: bc.name, Variant: bc.variant, Runs: *iterations, Samples: make([]time.Duration, 0, *iterations)}
		for i := 0; i < *iterations; i++ {
			dur, err := runScenario(context.Background(), bc, *timeout, true)
			if err != nil {
				result.Failures++
				fmt.Printf("- run %d failed: %v\n", i+1, err)
				continue
			}
			result.Successes++
			result.Samples = append(result.Samples, dur)
			fmt.Printf("- run %d: %s\n", i+1, dur.Round(time.Millisecond))
		}
		result.Average = averageDuration(result.Samples)
		results = append(results, result)
	}

	speedups := computeSpeedups(results)
	if *jsonOut {
		payload := struct {
			Results  []benchmarkResult `json:"results"`
			Speedups []speedupResult   `json:"speedups"`
		}{Results: results, Speedups: speedups}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(payload)
	}

	printSummary(results, speedups)
	return nil
}

func buildCases(ubBase commandSpec, includeFFmpeg, includeCursor bool) []benchmarkCase {
	cases := make([]benchmarkCase, 0)
	if includeFFmpeg {
		cases = append(cases,
			newInstallCase("ffmpeg", "cold", ubBase),
			newInstallCase("ffmpeg", "warm", ubBase),
			newUninstallCase("ffmpeg", "cold", ubBase),
			newUninstallCase("ffmpeg", "warm", ubBase),
		)
	}
	if includeCursor {
		cases = append(cases,
			newInstallCase("cursor", "cold", ubBase),
			newInstallCase("cursor", "warm", ubBase),
			newUninstallCase("cursor", "cold", ubBase),
			newUninstallCase("cursor", "warm", ubBase),
		)
	}
	return cases
}

func newInstallCase(target, variant string, base commandSpec) benchmarkCase {
	name := fmt.Sprintf("install:%s", target)
	prepare := []commandSpec{ubCmd(base, "uninstall", target)}
	if variant == "cold" {
		prepare = []commandSpec{ubCmd(base, "reset")}
	}
	return benchmarkCase{name: name, variant: variant, prepare: prepare, run: ubCmd(base, "install", target)}
}

func newUninstallCase(target, variant string, base commandSpec) benchmarkCase {
	name := fmt.Sprintf("uninstall:%s", target)
	prepare := []commandSpec{ubCmd(base, "install", target)}
	if variant == "cold" {
		prepare = []commandSpec{ubCmd(base, "reset"), ubCmd(base, "install", target)}
	}
	return benchmarkCase{name: name, variant: variant, prepare: prepare, run: ubCmd(base, "uninstall", target)}
}

func ubCmd(base commandSpec, op string, args ...string) commandSpec {
	full := append([]string{}, base.args...)
	full = append(full, op)
	full = append(full, args...)
	return commandSpec{bin: base.bin, args: full}
}

func runScenario(parent context.Context, bc benchmarkCase, timeout time.Duration, strict bool) (time.Duration, error) {
	for _, prep := range bc.prepare {
		if err := runCommand(parent, prep, timeout); err != nil && strict {
			return 0, fmt.Errorf("prepare step failed for %s (%s): %w", bc.name, bc.variant, err)
		}
	}
	start := time.Now()
	err := runCommand(parent, bc.run, timeout)
	elapsed := time.Since(start)
	if err != nil && strict {
		return 0, err
	}
	return elapsed, err
}

func runCommand(parent context.Context, spec commandSpec, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, spec.bin, spec.args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err == nil {
		return nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("command timed out: %s %s", spec.bin, strings.Join(spec.args, " "))
	}
	output := strings.TrimSpace(out.String())
	if len(output) > 500 {
		output = output[len(output)-500:]
	}
	return fmt.Errorf("command failed (%s %s): %w\n%s", spec.bin, strings.Join(spec.args, " "), err, output)
}

func averageDuration(samples []time.Duration) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	var total time.Duration
	for _, sample := range samples {
		total += sample
	}
	return total / time.Duration(len(samples))
}

func computeSpeedups(results []benchmarkResult) []speedupResult {
	byCase := map[string]map[string]benchmarkResult{}
	for _, result := range results {
		if _, ok := byCase[result.Case]; !ok {
			byCase[result.Case] = map[string]benchmarkResult{}
		}
		byCase[result.Case][result.Variant] = result
	}

	keys := make([]string, 0, len(byCase))
	for key := range byCase {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]speedupResult, 0)
	for _, key := range keys {
		pair := byCase[key]
		warm, warmOK := pair["warm"]
		cold, coldOK := pair["cold"]
		if !warmOK || !coldOK || warm.Average <= 0 || cold.Average <= 0 {
			continue
		}
		out = append(out, speedupResult{
			Case:          key,
			ColdAvgMillis: float64(cold.Average.Milliseconds()),
			WarmAvgMillis: float64(warm.Average.Milliseconds()),
			WarmupSpeedup: float64(cold.Average) / float64(warm.Average),
		})
	}
	return out
}

func printSummary(results []benchmarkResult, speedups []speedupResult) {
	fmt.Println("\n==> Raw results")
	for _, result := range results {
		fmt.Printf("- %-20s %-5s avg=%-9s success=%d/%d failures=%d\n",
			result.Case,
			result.Variant,
			result.Average.Round(time.Millisecond),
			result.Successes,
			result.Runs,
			result.Failures,
		)
	}

	fmt.Println("\n==> Speedups (cold_time / warm_time)")
	if len(speedups) == 0 {
		fmt.Println("- no comparable successful pairs")
		return
	}
	for _, row := range speedups {
		fmt.Printf("- %-20s %.2fx (cold %.0fms vs warm %.0fms)\n", row.Case, row.WarmupSpeedup, row.ColdAvgMillis, row.WarmAvgMillis)
	}
}
