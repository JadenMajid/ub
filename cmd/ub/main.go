package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"ub/internal/engine"
	"ub/internal/formula"
	"ub/internal/graph"
	"ub/internal/native"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	manager := native.New(0)
	if err := manager.EnsureLayout(); err != nil {
		return err
	}

	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "install", "i":
		return runNativeInstall(manager, args[1:])
	case "reset":
		return runNativeReset(manager)
	case "uninstall", "remove", "rm":
		return runNativeUninstall(manager, args[1:])
	case "list", "ls":
		return runNativeList(manager)
	case "search":
		return runNativeSearch(manager, args[1:])
	case "info":
		return runNativeInfo(manager, args[1:])
	case "update":
		return runNativeUpdate(manager)
	case "prefix":
		return runNativePrefix(manager, args[1:])
	case "config":
		return runNativeConfig(manager)
	case "mvp-plan":
		return runPlan(args[1:])
	case "mvp-install":
		return runInstall(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	case "--version", "-v", "version":
		fmt.Println("ub 0.1.0")
		return nil
	default:
		return fmt.Errorf("command %q is not implemented yet", args[0])
	}
}

func runNativeInstall(manager *native.Manager, args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	jobs := fs.Int("jobs", manager.Workers, "maximum parallel jobs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	names := fs.Args()
	if len(names) == 0 {
		return fmt.Errorf("install requires at least one formula")
	}
	manager.Workers = *jobs
	return manager.Install(context.Background(), names)
}

func runNativeUninstall(manager *native.Manager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("uninstall requires at least one formula")
	}
	summary, err := manager.UninstallWithAutoremove(context.Background(), args)
	if err != nil {
		return err
	}
	for _, line := range uninstallSummaryLines(summary) {
		fmt.Println(line)
	}
	return nil
}

func uninstallSummaryLines(summary native.UninstallSummary) []string {
	lines := make([]string, 0, len(summary.Removed)+len(summary.AutoRemove)*2+1)
	for _, rec := range summary.Removed {
		lines = append(lines, fmt.Sprintf("Uninstalling %s... (%d files, %s)", rec.Path, rec.Files, rec.SizeHuman))
	}
	if len(summary.AutoRemove) == 0 {
		return lines
	}
	lines = append(lines, fmt.Sprintf("==> Autoremoving %d unneeded formulae:", len(summary.AutoRemove)))
	for _, rec := range summary.AutoRemove {
		lines = append(lines, rec.Name)
	}
	for _, rec := range summary.AutoRemove {
		lines = append(lines, fmt.Sprintf("Uninstalling %s... (%d files, %s)", rec.Path, rec.Files, rec.SizeHuman))
	}
	return lines
}

func runNativeReset(manager *native.Manager) error {
	if err := manager.Reset(); err != nil {
		return err
	}
	fmt.Println("Reset complete")
	return nil
}

func runNativeList(manager *native.Manager) error {
	list, err := manager.ListInstalled()
	if err != nil {
		return err
	}
	for _, name := range list {
		fmt.Println(name)
	}
	return nil
}

func runNativeSearch(manager *native.Manager, args []string) error {
	query := ""
	if len(args) > 0 {
		query = strings.Join(args, " ")
	}
	results, err := manager.Search(context.Background(), query)
	if err != nil {
		return err
	}
	for _, r := range results {
		fmt.Printf("%s\t%s\n", r.Name, r.Desc)
	}
	return nil
}

func runNativeInfo(manager *native.Manager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("info requires a formula name")
	}
	for _, name := range args {
		f, err := manager.Info(context.Background(), name)
		if err != nil {
			return err
		}
		fmt.Printf("%s (%s)\n", f.Name, f.Versions.Stable)
		fmt.Println(f.Desc)
		if f.Homepage != "" {
			fmt.Println("Homepage:", f.Homepage)
		}
		if len(f.Dependencies) > 0 {
			fmt.Println("Dependencies:", strings.Join(f.Dependencies, ", "))
		}
	}
	return nil
}

func runNativeUpdate(manager *native.Manager) error {
	_, err := manager.Search(context.Background(), "")
	if err != nil {
		return err
	}
	fmt.Println("Updated Homebrew formula metadata cache")
	return nil
}

func runNativePrefix(manager *native.Manager, args []string) error {
	if len(args) == 0 {
		fmt.Println(manager.Paths.Prefix)
		return nil
	}
	name := args[0]
	formulaDir := filepath.Join(manager.Paths.Cellar, name)
	versions, err := os.ReadDir(formulaDir)
	if err != nil {
		return fmt.Errorf("formula %q is not installed", name)
	}
	latest := ""
	for _, v := range versions {
		if v.IsDir() && v.Name() > latest {
			latest = v.Name()
		}
	}
	if latest == "" {
		return fmt.Errorf("formula %q has no installed versions", name)
	}
	fmt.Println(filepath.Join(formulaDir, latest))
	return nil
}

func runNativeConfig(manager *native.Manager) error {
	fmt.Println("UB_BASE_DIR:", manager.Paths.BaseDir)
	fmt.Println("UB_PREFIX:", manager.Paths.Prefix)
	fmt.Println("UB_REPOSITORY:", manager.Paths.Repo)
	fmt.Println("UB_CELLAR:", manager.Paths.Cellar)
	fmt.Println("UB_CACHE:", manager.Paths.Cache)
	return nil
}

func runPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	tapDir := fs.String("tap", "./taps/core", "formula tap directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	roots := fs.Args()
	if len(roots) == 0 {
		return fmt.Errorf("plan requires at least one formula")
	}

	formulas, plan, err := resolveAndPlan(*tapDir, roots)
	if err != nil {
		return err
	}

	fmt.Println("Plan")
	fmt.Println("- roots:", strings.Join(roots, ", "))
	fmt.Println("- total formulas:", len(formulas))
	fmt.Println("- layers:")
	for idx, layer := range plan.Layers {
		fmt.Printf("  %d: %s\n", idx, strings.Join(layer, ", "))
	}

	return nil
}

func runInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	tapDir := fs.String("tap", "./taps/core", "formula tap directory")
	rootDir := fs.String("root", "./cellar", "installation root")
	cacheDir := fs.String("cache", "./cache", "download cache directory")
	jobs := fs.Int("jobs", native.New(0).Workers, "maximum parallel jobs")
	if err := fs.Parse(args); err != nil {
		return err
	}

	roots := fs.Args()
	if len(roots) == 0 {
		return fmt.Errorf("install requires at least one formula")
	}

	formulas, plan, err := resolveAndPlan(*tapDir, roots)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(*rootDir, 0o755); err != nil {
		return fmt.Errorf("create root dir: %w", err)
	}

	installer := engine.Installer{
		TapDir:   mustAbs(*tapDir),
		RootDir:  mustAbs(*rootDir),
		CacheDir: mustAbs(*cacheDir),
		Jobs:     *jobs,
	}

	fmt.Printf("Installing %d formula(s) with %d job(s)\n", len(formulas), *jobs)
	fmt.Printf("Execution layers: %d\n", len(plan.Layers))

	if err := installer.Install(context.Background(), formulas); err != nil {
		return err
	}

	names := make([]string, 0, len(formulas))
	for name := range formulas {
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Println("Installed:", strings.Join(names, ", "))
	return nil
}

func resolveAndPlan(tapDir string, roots []string) (map[string]formula.Formula, graph.Plan, error) {
	formulas, err := formula.ResolveClosure(tapDir, roots)
	if err != nil {
		return nil, graph.Plan{}, err
	}

	plan, err := graph.BuildPlan(formulas)
	if err != nil {
		return nil, graph.Plan{}, err
	}

	return formulas, plan, nil
}

func mustAbs(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

func printUsage() {
	fmt.Println("ub: native Homebrew-compatible package manager")
	fmt.Println("")
	fmt.Println("Usage:")
	fmt.Println("  ub install <formula...> [--jobs N]")
	fmt.Println("  ub reset")
	fmt.Println("  ub uninstall <formula...>")
	fmt.Println("  ub list")
	fmt.Println("  ub info <formula...>")
	fmt.Println("  ub search [query]")
	fmt.Println("  ub update")
	fmt.Println("  ub prefix [formula]")
	fmt.Println("  ub config")
	fmt.Println("")
	fmt.Println("Defaults:")
	fmt.Println("  prefix: .../ub")
	fmt.Println("  repository: .../unbrew")
	fmt.Println("")
	fmt.Println("Prototype engine commands:")
	fmt.Println("  ub mvp-plan <formula...> [--tap DIR]")
	fmt.Println("  ub mvp-install <formula...> [--tap DIR] [--root DIR] [--cache DIR] [--jobs N]")
}
