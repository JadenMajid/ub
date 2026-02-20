package native

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ub/internal/fetch"
	"ub/internal/homebrewapi"
	"ub/internal/lock"
	"ub/internal/scheduler"

	"golang.org/x/term"
)

type Paths struct {
	BaseDir      string
	Prefix       string
	Repo         string
	Cellar       string
	Caskroom     string
	Cache        string
	Bin          string
	Sbin         string
	Applications string
}

func DefaultPaths() Paths {
	base := os.Getenv("UB_BASE_DIR")
	if strings.TrimSpace(base) == "" {
		base = detectWritableBaseDir()
	}
	prefix := filepath.Join(base, "ub")
	return Paths{
		BaseDir:      base,
		Prefix:       prefix,
		Repo:         filepath.Join(base, "unbrew"),
		Cellar:       filepath.Join(prefix, "Cellar"),
		Caskroom:     filepath.Join(prefix, "Caskroom"),
		Cache:        filepath.Join(prefix, "cache"),
		Bin:          filepath.Join(prefix, "bin"),
		Sbin:         filepath.Join(prefix, "sbin"),
		Applications: filepath.Join(prefix, "Applications"),
	}
}

func detectWritableBaseDir() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "."
	}

	var candidates []string
	if runtime.GOOS == "darwin" {
		candidates = []string{"/opt", "/usr/local", home}
	} else {
		candidates = []string{filepath.Join(home, ".local"), home}
	}

	for _, base := range candidates {
		testDir := filepath.Join(base, "ub")
		if err := os.MkdirAll(testDir, 0o755); err == nil {
			return base
		}
	}

	return home
}

type Manager struct {
	API     *homebrewapi.Client
	Fetch   *fetch.Cache
	Paths   Paths
	Workers int
}

type UninstallRecord struct {
	Name      string
	Path      string
	Files     int
	SizeBytes int64
	SizeHuman string
}

type UninstallSummary struct {
	Removed    []UninstallRecord
	AutoRemove []UninstallRecord
}

type caskInstallReceipt struct {
	Token          string   `json:"token"`
	Version        string   `json:"version"`
	AppPath        string   `json:"app_path"`
	LinkedBinaries []string `json:"linked_binaries"`
}

type uninstallBatchJob struct {
	id  string
	run func(context.Context) error
}

func (j uninstallBatchJob) ID() string { return j.id }

func (j uninstallBatchJob) Requires() []string { return nil }

func (j uninstallBatchJob) Run(ctx context.Context) error { return j.run(ctx) }

func New(workers int) *Manager {
	paths := DefaultPaths()
	cache := fetch.NewCache(filepath.Join(paths.Cache, "bottles"))
	if workers <= 0 {
		workers = defaultWorkers()
	}
	return &Manager{
		API:     homebrewapi.New(paths.Cache, paths.Repo),
		Fetch:   cache,
		Paths:   paths,
		Workers: workers,
	}
}

func defaultWorkers() int {
	workers := runtime.NumCPU()
	if workers < 1 {
		workers = 1
	}
	if workers == 1 {
		return 1
	}
	if workers < 2 {
		return 2
	}
	return workers
}

func (m *Manager) EnsureLayout() error {
	dirs := []string{m.Paths.Prefix, m.Paths.Repo, m.Paths.Cellar, m.Paths.Caskroom, m.Paths.Cache, m.Paths.Bin, m.Paths.Sbin, m.Paths.Applications}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
		}
	}
	return nil
}

func (m *Manager) Search(ctx context.Context, query string) ([]homebrewapi.FormulaSummary, error) {
	list, err := m.API.FormulaList(ctx)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		if len(list) > 50 {
			return list[:50], nil
		}
		return list, nil
	}
	results := make([]homebrewapi.FormulaSummary, 0)
	for _, item := range list {
		if strings.Contains(strings.ToLower(item.Name), query) || strings.Contains(strings.ToLower(item.Desc), query) {
			results = append(results, item)
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })
	if len(results) > 100 {
		return results[:100], nil
	}
	return results, nil
}

func (m *Manager) Info(ctx context.Context, name string) (homebrewapi.Formula, error) {
	return m.API.FormulaByName(ctx, name)
}

func (m *Manager) ListInstalled() ([]string, error) {
	entries, err := os.ReadDir(m.Paths.Cellar)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]string, 0)
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

func (m *Manager) listInstalledCasks() ([]string, error) {
	entries, err := os.ReadDir(m.Paths.Caskroom)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]string, 0)
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

func (m *Manager) Uninstall(name string) error {
	_, err := m.UninstallWithAutoremove(context.Background(), []string{name})
	return err
}

func (m *Manager) UninstallWithAutoremove(ctx context.Context, names []string) (UninstallSummary, error) {
	if err := m.EnsureLayout(); err != nil {
		return UninstallSummary{}, err
	}
	lockHandle, err := lock.Acquire(m.Paths.Cellar)
	if err != nil {
		return UninstallSummary{}, err
	}
	defer lockHandle.Release()

	summary := UninstallSummary{}
	reporter := newUninstallReporter()
	trimmed := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			trimmed = append(trimmed, name)
		}
	}

	formulaTargets := make([]string, 0)
	caskTargets := make([]string, 0)
	for _, name := range trimmed {
		formulaDir := filepath.Join(m.Paths.Cellar, name)
		if info, err := os.Stat(formulaDir); err == nil && info.IsDir() {
			formulaTargets = append(formulaTargets, name)
			continue
		}
		caskDir := filepath.Join(m.Paths.Caskroom, name)
		if info, err := os.Stat(caskDir); err == nil && info.IsDir() {
			caskTargets = append(caskTargets, name)
			continue
		}
		return UninstallSummary{}, fmt.Errorf("package %q is not installed", name)
	}

	candidateDeps := map[string]bool{}
	rootSet := map[string]bool{}
	for _, name := range formulaTargets {
		rootSet[name] = true
		closure, err := m.resolveClosure(ctx, []string{name})
		if err != nil {
			return UninstallSummary{}, err
		}
		for dep := range closure {
			if dep != name {
				candidateDeps[dep] = true
			}
		}
	}

	formulaRemoved, err := m.uninstallFormulaBatch(ctx, formulaTargets, reporter)
	if err != nil {
		return UninstallSummary{}, err
	}
	summary.Removed = append(summary.Removed, formulaRemoved...)

	caskRemoved, err := m.uninstallCaskBatch(ctx, caskTargets, reporter)
	if err != nil {
		return UninstallSummary{}, err
	}
	summary.Removed = append(summary.Removed, caskRemoved...)

	remaining, err := m.ListInstalled()
	if err != nil {
		return UninstallSummary{}, err
	}

	remainingSet := make(map[string]bool, len(remaining))
	for _, name := range remaining {
		remainingSet[name] = true
	}

	nonCandidateRoots := make([]string, 0)
	for _, name := range remaining {
		if !candidateDeps[name] {
			nonCandidateRoots = append(nonCandidateRoots, name)
		}
	}

	requiredByNonCandidates := map[string]bool{}
	if len(nonCandidateRoots) > 0 {
		closure, err := m.resolveClosure(ctx, nonCandidateRoots)
		if err != nil {
			return UninstallSummary{}, err
		}
		for dep := range closure {
			if remainingSet[dep] {
				requiredByNonCandidates[dep] = true
			}
		}
	}

	autoRemoveNames := make([]string, 0)
	for _, name := range remaining {
		if rootSet[name] {
			continue
		}
		if !candidateDeps[name] {
			continue
		}
		if !requiredByNonCandidates[name] {
			autoRemoveNames = append(autoRemoveNames, name)
		}
	}
	sort.Strings(autoRemoveNames)

	autoRemoved, err := m.uninstallFormulaBatch(ctx, autoRemoveNames, reporter)
	if err != nil {
		return UninstallSummary{}, err
	}
	summary.AutoRemove = append(summary.AutoRemove, autoRemoved...)

	return summary, nil
}

func (m *Manager) uninstallFormulaBatch(ctx context.Context, names []string, reporter *uninstallReporter) ([]UninstallRecord, error) {
	if len(names) == 0 {
		return nil, nil
	}

	jobs := make([]scheduler.Job, 0, len(names))
	records := make([]UninstallRecord, len(names))
	var recordsMu sync.Mutex

	for idx, name := range names {
		idx := idx
		name := name
		jobs = append(jobs, uninstallBatchJob{
			id: fmt.Sprintf("formula:%s:%d", name, idx),
			run: func(context.Context) error {
				rec, err := m.uninstallFormulaLocked(name, reporter)
				if err != nil {
					return err
				}
				recordsMu.Lock()
				records[idx] = rec
				recordsMu.Unlock()
				return nil
			},
		})
	}

	exec := scheduler.Executor{Workers: m.Workers}
	if err := exec.Run(ctx, jobs); err != nil {
		return nil, err
	}

	return records, nil
}

func (m *Manager) uninstallCaskBatch(ctx context.Context, names []string, reporter *uninstallReporter) ([]UninstallRecord, error) {
	if len(names) == 0 {
		return nil, nil
	}

	jobs := make([]scheduler.Job, 0, len(names))
	records := make([]UninstallRecord, len(names))
	var recordsMu sync.Mutex

	for idx, name := range names {
		idx := idx
		name := name
		jobs = append(jobs, uninstallBatchJob{
			id: fmt.Sprintf("cask:%s:%d", name, idx),
			run: func(context.Context) error {
				rec, err := m.uninstallCaskLocked(name, reporter)
				if err != nil {
					return err
				}
				recordsMu.Lock()
				records[idx] = rec
				recordsMu.Unlock()
				return nil
			},
		})
	}

	exec := scheduler.Executor{Workers: m.Workers}
	if err := exec.Run(ctx, jobs); err != nil {
		return nil, err
	}

	return records, nil
}

func (m *Manager) uninstallFormulaLocked(name string, reporters ...*uninstallReporter) (UninstallRecord, error) {
	var reporter *uninstallReporter
	if len(reporters) > 0 {
		reporter = reporters[0]
	}
	formulaDir := filepath.Join(m.Paths.Cellar, name)
	if _, err := os.Stat(formulaDir); err != nil {
		if os.IsNotExist(err) {
			return UninstallRecord{}, fmt.Errorf("formula %q is not installed", name)
		}
		return UninstallRecord{}, err
	}

	versions, err := os.ReadDir(formulaDir)
	if err != nil {
		return UninstallRecord{}, err
	}
	displayPath := formulaDir
	latest := ""
	for _, version := range versions {
		if version.IsDir() && version.Name() > latest {
			latest = version.Name()
		}
	}
	if latest != "" {
		displayPath = filepath.Join(formulaDir, latest)
	}

	files, size, err := dirStats(displayPath)
	if err != nil {
		return UninstallRecord{}, err
	}

	if err := m.unlinkTree(filepath.Join(formulaDir), m.Paths.Bin, "bin"); err != nil {
		return UninstallRecord{}, err
	}
	if err := m.unlinkTree(filepath.Join(formulaDir), m.Paths.Sbin, "sbin"); err != nil {
		return UninstallRecord{}, err
	}

	var onProgress func(removed, total int, done bool)
	if reporter != nil {
		onProgress = reporter.progressCallback("Uninstall " + name)
	}
	if err := removeTreeWithProgress(formulaDir, onProgress); err != nil {
		return UninstallRecord{}, err
	}

	return UninstallRecord{
		Name:      name,
		Path:      displayPath,
		Files:     files,
		SizeBytes: size,
		SizeHuman: formatSize(size),
	}, nil
}

func (m *Manager) uninstallCaskLocked(name string, reporters ...*uninstallReporter) (UninstallRecord, error) {
	var reporter *uninstallReporter
	if len(reporters) > 0 {
		reporter = reporters[0]
	}
	caskRoot := filepath.Join(m.Paths.Caskroom, name)
	entries, err := os.ReadDir(caskRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return UninstallRecord{}, fmt.Errorf("cask %q is not installed", name)
		}
		return UninstallRecord{}, err
	}
	latest := ""
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() > latest {
			latest = entry.Name()
		}
	}
	if latest == "" {
		return UninstallRecord{}, fmt.Errorf("cask %q has no installed versions", name)
	}
	versionDir := filepath.Join(caskRoot, latest)

	receiptPath := filepath.Join(versionDir, "INSTALL_RECEIPT.json")
	receiptData, err := os.ReadFile(receiptPath)
	if err == nil {
		var receipt caskInstallReceipt
		if err := json.Unmarshal(receiptData, &receipt); err == nil {
			for _, appPath := range caskAppRemovalCandidates(receipt.AppPath, m.Paths.Applications) {
				_ = os.RemoveAll(appPath)
			}
			for _, bin := range receipt.LinkedBinaries {
				_ = os.Remove(bin)
			}
		}
	}

	files, size, statErr := dirStats(versionDir)
	if statErr != nil {
		return UninstallRecord{}, statErr
	}

	var onProgress func(removed, total int, done bool)
	if reporter != nil {
		onProgress = reporter.progressCallback("Uninstall cask " + name)
	}
	if err := removeTreeWithProgress(caskRoot, onProgress); err != nil {
		return UninstallRecord{}, err
	}

	return UninstallRecord{
		Name:      name,
		Path:      versionDir,
		Files:     files,
		SizeBytes: size,
		SizeHuman: formatSize(size),
	}, nil
}

func (m *Manager) Reset() error {
	installedFormulae, err := m.ListInstalled()
	if err != nil {
		return err
	}
	installedCasks, err := m.listInstalledCasks()
	if err != nil {
		return err
	}
	targets := append(append([]string{}, installedFormulae...), installedCasks...)
	if _, err := m.UninstallWithAutoremove(context.Background(), targets); err != nil {
		return err
	}
	if err := os.RemoveAll(m.Paths.Cache); err != nil {
		return err
	}
	return m.EnsureLayout()
}

func (m *Manager) Install(ctx context.Context, names []string) error {
	formulaRoots := make([]string, 0, len(names))
	casks := make([]homebrewapi.Cask, 0)
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if _, err := m.API.FormulaByName(ctx, name); err == nil {
			formulaRoots = append(formulaRoots, name)
			continue
		} else if isNotFoundError(err) {
			cask, caskErr := m.API.CaskByName(ctx, name)
			if caskErr != nil {
				return caskErr
			}
			casks = append(casks, cask)
			continue
		} else {
			return err
		}
	}

	if len(formulaRoots) > 0 {
		if err := m.installFormulas(ctx, formulaRoots); err != nil {
			return err
		}
	}

	for _, cask := range casks {
		if err := m.installCask(ctx, cask); err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) installFormulas(ctx context.Context, names []string) error {
	if err := m.EnsureLayout(); err != nil {
		return err
	}
	lockHandle, err := lock.Acquire(m.Paths.Cellar)
	if err != nil {
		return err
	}
	defer lockHandle.Release()

	closure, err := m.resolveClosure(ctx, names)
	if err != nil {
		return err
	}
	reporter := newInstallReporter(m.Paths, names, closure)
	reporter.workers = m.Workers
	reporter.printPlan()

	jobs := make([]scheduler.Job, 0, len(closure))
	rootSet := make(map[string]bool, len(names))
	for _, name := range names {
		rootSet[name] = true
	}
	for _, f := range closure {
		jobs = append(jobs, installJob{manager: m, formula: f, reporter: reporter, rootSet: rootSet})
	}

	exec := scheduler.Executor{Workers: m.Workers}
	if err := exec.Run(ctx, jobs); err != nil {
		return err
	}
	reporter.printSummary()
	return nil
}

func (m *Manager) installCask(ctx context.Context, cask homebrewapi.Cask) error {
	if err := m.EnsureLayout(); err != nil {
		return err
	}
	lockHandle, err := lock.Acquire(m.Paths.Caskroom)
	if err != nil {
		return err
	}
	defer lockHandle.Release()

	version := strings.TrimSpace(cask.Version)
	if version == "" {
		version = "latest"
	}
	caskDir := filepath.Join(m.Paths.Caskroom, cask.Token, version)
	appName := cask.AppArtifact()
	if strings.TrimSpace(appName) == "" {
		return fmt.Errorf("cask %q has no app artifact", cask.Token)
	}

	reporter := &installReporter{}
	fmt.Printf("==> Downloading Cask %s\n", cask.Token)
	archive, err := m.Fetch.FetchWithProgress(ctx, cask.URL, reporter.progressCallback("Cask "+cask.Token))
	if err != nil {
		return err
	}
	if err := verifySHA256(archive, cask.SHA256); err != nil {
		return fmt.Errorf("verify cask checksum: %w", err)
	}

	if err := os.RemoveAll(caskDir); err != nil {
		return err
	}
	if err := os.MkdirAll(caskDir, 0o755); err != nil {
		return err
	}

	isZip, err := isZipArchive(archive)
	if err != nil {
		return err
	}
	if isZip {
		if err := extractZip(archive, caskDir); err != nil {
			return err
		}
	} else if err := extractTarGz(archive, caskDir); err != nil {
		return err
	}

	appSource, err := findFileInTree(caskDir, filepath.Base(appName))
	if err != nil {
		return err
	}
	appDest := filepath.Join(m.Paths.Applications, filepath.Base(appName))

	fmt.Printf("==> Installing Cask %s\n", cask.Token)
	if err := os.RemoveAll(appDest); err != nil {
		return err
	}
	if err := os.Rename(appSource, appDest); err != nil {
		return err
	}
	fmt.Printf("==> Moving App '%s' to '%s'\n", filepath.Base(appName), appDest)

	linked := make([]string, 0)
	for _, bin := range cask.BinaryArtifacts() {
		src := strings.ReplaceAll(bin.Source, "$APPDIR", m.Paths.Applications)
		target := strings.TrimSpace(bin.Target)
		if target == "" {
			target = filepath.Base(src)
		}
		dst := filepath.Join(m.Paths.Bin, target)
		if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Symlink(src, dst); err != nil {
			return err
		}
		fmt.Printf("==> Linking Binary '%s' to '%s'\n", filepath.Base(src), dst)
		linked = append(linked, dst)
	}

	if err := writeCaskReceipt(caskDir, cask.Token, version, appDest, linked); err != nil {
		return err
	}

	fmt.Printf("🍺  %s was successfully installed!\n", cask.Token)
	return nil
}

func (m *Manager) resolveClosure(ctx context.Context, roots []string) (map[string]homebrewapi.Formula, error) {
	seen := map[string]homebrewapi.Formula{}
	visiting := map[string]bool{}

	var dfs func(string) error
	dfs = func(name string) error {
		if _, ok := seen[name]; ok {
			return nil
		}
		if visiting[name] {
			return fmt.Errorf("dependency cycle detected at %q", name)
		}
		visiting[name] = true

		f, err := m.API.FormulaByName(ctx, name)
		if err != nil {
			return err
		}
		for _, dep := range f.Dependencies {
			if err := dfs(dep); err != nil {
				return fmt.Errorf("resolve dependency %q for %q: %w", dep, name, err)
			}
		}

		visiting[name] = false
		seen[name] = f
		return nil
	}

	for _, root := range roots {
		if err := dfs(root); err != nil {
			return nil, err
		}
	}
	return seen, nil
}

type installJob struct {
	manager  *Manager
	formula  homebrewapi.Formula
	reporter *installReporter
	rootSet  map[string]bool
}

func (j installJob) ID() string { return j.formula.Name }

func (j installJob) Requires() []string { return j.formula.Dependencies }

func (j installJob) Run(ctx context.Context) error {
	if j.manager.isInstalled(j.formula.Name, j.formula.Versions.Stable) {
		j.reporter.printAlreadyInstalled(j.formula.Name, j.formula.Versions.Stable)
		return nil
	}
	bottle, tag, err := selectBottle(j.formula)
	if err != nil {
		return err
	}
	label := fmt.Sprintf("Bottle %s (%s)", j.formula.Name, j.formula.Versions.Stable)
	archive, err := j.manager.Fetch.FetchWithProgress(ctx, bottle.URL, j.reporter.progressCallback(label))
	if err != nil {
		return err
	}
	workerID, _ := scheduler.WorkerID(ctx)
	j.reporter.printInstalling(j.formula.Name, j.formula.Versions.Stable, tag, j.rootSet[j.formula.Name], bottle.URL, workerID)
	if err := verifySHA256(archive, bottle.SHA256); err != nil {
		return fmt.Errorf("verify bottle checksum (%s): %w", tag, err)
	}
	installDir := filepath.Join(j.manager.Paths.Cellar, j.formula.Name, j.formula.Versions.Stable)
	if err := os.RemoveAll(installDir); err != nil {
		return fmt.Errorf("clear existing install dir: %w", err)
	}
	if err := extractTarGz(archive, j.manager.Paths.Cellar); err != nil {
		return err
	}
	linkedVersion, err := j.manager.linkFormula(j.formula.Name, j.formula.Versions.Stable)
	if err != nil {
		return err
	}
	j.reporter.printPoured(j.formula.Name, linkedVersion)
	return nil
}

type installReporter struct {
	paths         Paths
	roots         []string
	rootSet       map[string]bool
	deps          []string
	mu            sync.Mutex
	installed     []string
	showHeader    bool
	workers       int
	spinnerPos    int
	showProgress  bool
	progressSeen  map[string]int
	progressStart map[string]time.Time
}

func newInstallReporter(paths Paths, roots []string, closure map[string]homebrewapi.Formula) *installReporter {
	rootSet := make(map[string]bool, len(roots))
	for _, name := range roots {
		rootSet[name] = true
	}
	deps := make([]string, 0)
	for name := range closure {
		if !rootSet[name] {
			deps = append(deps, name)
		}
	}
	sort.Strings(deps)
	return &installReporter{
		paths:         paths,
		roots:         append([]string(nil), roots...),
		rootSet:       rootSet,
		deps:          deps,
		showHeader:    len(roots) > 0,
		progressSeen:  map[string]int{},
		progressStart: map[string]time.Time{},
	}
}

func (r *installReporter) printPlan() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.showHeader {
		return
	}
	r.clearProgressLocked()
	fmt.Printf("==> Fetching downloads for: %s\n", strings.Join(r.roots, ", "))
	fmt.Printf("==> Using %d worker(s)\n", r.workers)
	if len(r.deps) > 0 {
		fmt.Printf("==> Installing dependencies for %s: %s\n", strings.Join(r.roots, ", "), joinWithAnd(r.deps))
	}
}

func (r *installReporter) progressCallback(label string) func(fetch.Progress) {
	return func(p fetch.Progress) {
		r.printDownloadProgress(label, p)
	}
}

func (r *installReporter) printDownloadProgress(label string, p fetch.Progress) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.progressSeen == nil {
		r.progressSeen = map[string]int{}
	}
	if r.progressStart == nil {
		r.progressStart = map[string]time.Time{}
	}
	if _, ok := r.progressStart[label]; !ok {
		r.progressStart[label] = time.Now()
	}
	r.progressSeen[label]++
	elapsed := time.Since(r.progressStart[label])

	if p.Cached {
		r.clearProgressLocked()
		fmt.Printf("✔︎ %-64s Using cached file\n", label)
		return
	}

	if p.Done && p.TotalBytes > 0 {
		shouldSmooth := r.progressSeen[label] <= 2 || elapsed < 250*time.Millisecond
		if shouldSmooth {
			for _, fraction := range []float64{0.2, 0.45, 0.7, 0.9} {
				step := int64(float64(p.TotalBytes) * fraction)
				if step <= 0 || step >= p.DownloadedBytes {
					continue
				}
				r.renderDownloadProgressLine(label, step, p.TotalBytes, p.SpeedBytesPerSec, elapsed)
				time.Sleep(28 * time.Millisecond)
			}
		}
	}

	r.renderDownloadProgressLine(label, p.DownloadedBytes, p.TotalBytes, p.SpeedBytesPerSec, elapsed)

	if p.Done {
		fmt.Print("\n")
		r.showProgress = false
		delete(r.progressSeen, label)
		delete(r.progressStart, label)
	}
}

func (r *installReporter) renderDownloadProgressLine(label string, downloaded, total int64, speedBytesPerSec float64, elapsed time.Duration) {
	termWidth := terminalWidth()
	labelWidth, barWidth := progressLayout(termWidth, true)
	bar := renderProgressBar(downloaded, total, r.spinnerPos, barWidth)
	displayLabel := truncateText(label, labelWidth)
	percent := " --.-%"
	if total > 0 {
		value := (float64(downloaded) / float64(total)) * 100
		if value > 100 {
			value = 100
		}
		percent = fmt.Sprintf(" %5.1f%%", value)
	}
	speed := formatTransferRate(speedBytesPerSec)
	eta := "--:--"
	if remaining, ok := estimateRemaining(downloaded, total, speedBytesPerSec); ok {
		eta = formatClockDuration(remaining)
	}

	line := fmt.Sprintf("⬇ %-*s %s%s %8s elapsed %s eta %s", labelWidth, displayLabel, bar, percent, speed, formatClockDuration(elapsed), eta)
	printProgressLine(line, termWidth)
	r.showProgress = true
	r.spinnerPos++
}

func (r *installReporter) clearProgressLocked() {
	if !r.showProgress {
		return
	}
	fmt.Print("\r\033[2K")
	r.showProgress = false
}

func (r *installReporter) printInstalling(name, version, tag string, isRoot bool, bottleURL string, workerID int) {
	bottleName := homebrewBottleFilename(name, version, tag, bottleURL)
	prefix := "==>"
	if workerID > 0 {
		prefix = fmt.Sprintf("==> [w%d]", workerID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearProgressLocked()
	if isRoot {
		fmt.Printf("%s Installing %s\n", prefix, name)
	} else {
		fmt.Printf("%s Installing dependency: %s\n", prefix, name)
	}
	if bottleName != "" {
		fmt.Printf("%s Pouring %s\n", prefix, bottleName)
	}
}

func (r *installReporter) printPoured(name, version string) {
	installDir := filepath.Join(r.paths.Cellar, name, version)
	files, size, err := dirStats(installDir)
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearProgressLocked()
	fmt.Printf("🍺  %s: %d files, %s\n", installDir, files, formatSize(size))
	r.installed = append(r.installed, name)
}

func (r *installReporter) printAlreadyInstalled(name, version string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearProgressLocked()
	fmt.Printf("==> %s (%s) already installed\n", name, version)
}

func (r *installReporter) printSummary() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearProgressLocked()
	if len(r.installed) == 0 {
		return
	}
	sort.Strings(r.installed)
	fmt.Println("==> Summary")
	for _, name := range r.installed {
		fmt.Printf("- %s\n", name)
	}
}

func joinWithAnd(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	if len(parts) == 2 {
		return parts[0] + " and " + parts[1]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

func bottleFilename(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return filepath.Base(raw)
	}
	return filepath.Base(u.Path)
}

func homebrewBottleFilename(name, version, tag, fallbackURL string) string {
	name = strings.TrimSpace(name)
	version = strings.TrimSpace(version)
	tag = strings.TrimSpace(tag)
	if name != "" && version != "" && tag != "" {
		return fmt.Sprintf("%s--%s.%s.bottle.tar.gz", name, version, tag)
	}
	return bottleFilename(fallbackURL)
}

func formatSize(bytes int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	if bytes >= gb {
		return fmt.Sprintf("%.1fGB", float64(bytes)/float64(gb))
	}
	if bytes >= mb {
		return fmt.Sprintf("%.1fMB", float64(bytes)/float64(mb))
	}
	if bytes >= kb {
		return fmt.Sprintf("%.1fKB", float64(bytes)/float64(kb))
	}
	return fmt.Sprintf("%dB", bytes)
}

func renderProgressBar(downloaded, total int64, tick, width int) string {
	if width <= 0 {
		width = 24
	}
	if total <= 0 {
		pos := tick % width
		cells := make([]byte, width)
		for i := range cells {
			cells[i] = '-'
		}
		cells[pos] = '>'
		return "[" + string(cells) + "]"
	}
	if downloaded < 0 {
		downloaded = 0
	}
	if downloaded > total {
		downloaded = total
	}
	filled := int((float64(downloaded) / float64(total)) * float64(width))
	if filled > width {
		filled = width
	}
	return "[" + strings.Repeat("=", filled) + strings.Repeat("-", width-filled) + "]"
}

func formatTransferRate(bytesPerSec float64) string {
	if bytesPerSec <= 0 {
		return "--"
	}
	return formatSize(int64(bytesPerSec)) + "/s"
}

type uninstallReporter struct {
	mu            sync.Mutex
	spinnerPos    int
	showProgress  bool
	progressSeen  map[string]int
	progressStart map[string]time.Time
}

func newUninstallReporter() *uninstallReporter {
	return &uninstallReporter{progressSeen: map[string]int{}, progressStart: map[string]time.Time{}}
}

func (r *uninstallReporter) progressCallback(label string) func(removed, total int, done bool) {
	return func(removed, total int, done bool) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if _, ok := r.progressStart[label]; !ok {
			r.progressStart[label] = time.Now()
		}
		r.progressSeen[label]++
		elapsed := time.Since(r.progressStart[label])

		if done && total > 0 {
			shouldSmooth := r.progressSeen[label] <= 2 || elapsed < 250*time.Millisecond
			if shouldSmooth {
				for _, fraction := range []float64{0.25, 0.5, 0.75} {
					step := int(float64(total) * fraction)
					if step <= 0 || step >= removed {
						continue
					}
					r.renderUninstallProgressLine(label, step, total, elapsed)
					time.Sleep(24 * time.Millisecond)
				}
			}
		}

		r.renderUninstallProgressLine(label, removed, total, elapsed)

		if done {
			fmt.Print("\n")
			r.showProgress = false
			delete(r.progressSeen, label)
			delete(r.progressStart, label)
		}
	}
}

func (r *uninstallReporter) renderUninstallProgressLine(label string, removed, total int, elapsed time.Duration) {
	termWidth := terminalWidth()
	labelWidth, barWidth := progressLayout(termWidth, false)
	bar := renderProgressBar(int64(removed), int64(total), r.spinnerPos, barWidth)
	displayLabel := truncateText(label, labelWidth)
	percent := "100.0%"
	if total > 0 {
		percent = fmt.Sprintf("%5.1f%%", (float64(removed)/float64(total))*100)
	}
	eta := "--:--"
	if elapsed > 0 && total > 0 && removed < total {
		remainingUnits := float64(total - removed)
		unitsPerSecond := float64(removed) / elapsed.Seconds()
		if unitsPerSecond > 0 {
			eta = formatClockDuration(time.Duration(remainingUnits/unitsPerSecond) * time.Second)
		}
	}
	line := fmt.Sprintf("🗑 %-*s %s %s elapsed %s eta %s", labelWidth, displayLabel, bar, percent, formatClockDuration(elapsed), eta)
	printProgressLine(line, termWidth)
	r.showProgress = true
	r.spinnerPos++
}

func printProgressLine(line string, width int) {
	if width < 20 {
		width = 20
	}
	runes := []rune(line)
	if len(runes) > width {
		runes = runes[:width]
	}
	fmt.Printf("\r%-*s", width, string(runes))
}

func terminalWidth() int {
	if width, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && width > 0 {
		return width
	}
	if raw := strings.TrimSpace(os.Getenv("COLUMNS")); raw != "" {
		if width, err := strconv.Atoi(raw); err == nil && width > 0 {
			return width
		}
	}
	return 100
}

func progressLayout(termWidth int, includeSpeed bool) (labelWidth, barWidth int) {
	if termWidth < 60 {
		termWidth = 60
	}
	if includeSpeed {
		barWidth = clampInt(termWidth/3, 16, 48)
		labelWidth = clampInt(termWidth-barWidth-44, 12, 38)
		return labelWidth, barWidth
	}
	barWidth = clampInt(termWidth/2, 16, 56)
	labelWidth = clampInt(termWidth-barWidth-31, 12, 42)
	return labelWidth, barWidth
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func truncateText(value string, maxLen int) string {
	if maxLen <= 3 || len(value) <= maxLen {
		return value
	}
	return value[:maxLen-3] + "..."
}

func formatClockDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	seconds := int(d.Round(time.Second).Seconds())
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	secs := seconds % 60
	if hours > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, secs)
	}
	return fmt.Sprintf("%02d:%02d", minutes, secs)
}

func estimateRemaining(downloaded, total int64, speedBytesPerSec float64) (time.Duration, bool) {
	if total <= 0 || downloaded >= total || speedBytesPerSec <= 0 {
		return 0, false
	}
	remainingBytes := float64(total - downloaded)
	seconds := remainingBytes / speedBytesPerSec
	if seconds <= 0 {
		return 0, false
	}
	return time.Duration(seconds * float64(time.Second)), true
}

func removeTreeWithProgress(root string, onProgress func(removed, total int, done bool)) error {
	files := make([]string, 0)
	dirs := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			dirs = append(dirs, path)
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return err
	}

	total := len(files)
	removed := 0
	if onProgress != nil {
		onProgress(removed, total, false)
	}

	for _, file := range files {
		if err := os.Remove(file); err != nil {
			return err
		}
		removed++
		if onProgress != nil {
			onProgress(removed, total, false)
		}
	}

	for idx := len(dirs) - 1; idx >= 0; idx-- {
		if err := os.Remove(dirs[idx]); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	if onProgress != nil {
		onProgress(removed, total, true)
	}
	return nil
}

func dirStats(root string) (files int, size int64, err error) {
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		files++
		size += info.Size()
		return nil
	})
	return files, size, err
}

func (m *Manager) isInstalled(name, version string) bool {
	if strings.TrimSpace(version) == "" {
		return false
	}
	path := filepath.Join(m.Paths.Cellar, name, version)
	_, err := os.Stat(path)
	return err == nil
}

func selectBottle(f homebrewapi.Formula) (homebrewapi.BottleFile, string, error) {
	files := f.Bottle.Stable.Files
	if len(files) == 0 {
		return homebrewapi.BottleFile{}, "", fmt.Errorf("formula %q has no stable bottle", f.Name)
	}

	for _, tag := range preferredTags() {
		if bottle, ok := files[tag]; ok {
			return bottle, tag, nil
		}
	}

	for tag, bottle := range files {
		return bottle, tag, nil
	}

	return homebrewapi.BottleFile{}, "", fmt.Errorf("no bottle files available for %q", f.Name)
}

func preferredTags() []string {
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		return []string{"arm64_sequoia", "arm64_sonoma", "arm64_ventura", "sonoma", "ventura"}
	}
	if runtime.GOOS == "darwin" && runtime.GOARCH == "amd64" {
		return []string{"sonoma", "ventura", "monterey"}
	}
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		return []string{"arm64_linux", "x86_64_linux"}
	}
	return []string{"x86_64_linux", "arm64_linux", "sonoma", "arm64_sonoma"}
}

func verifySHA256(path, expected string) error {
	if strings.TrimSpace(expected) == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("sha256 mismatch: expected %s, got %s", expected, got)
	}
	return nil
}

func extractTarGz(archivePath, dst string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(dst, hdr.Name)
		cleanDst := filepath.Clean(dst)
		cleanTarget := filepath.Clean(target)
		if !strings.HasPrefix(cleanTarget, cleanDst+string(os.PathSeparator)) && cleanTarget != cleanDst {
			return fmt.Errorf("tar entry escapes destination: %q", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(cleanTarget, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(cleanTarget), 0o755); err != nil {
				return err
			}
			_ = os.Remove(cleanTarget)
			out, err := os.OpenFile(cleanTarget, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		case tar.TypeLink:
			if err := os.MkdirAll(filepath.Dir(cleanTarget), 0o755); err != nil {
				return err
			}
			_ = os.Remove(cleanTarget)
			linkTarget := hdr.Linkname
			if !filepath.IsAbs(linkTarget) {
				linkTarget = filepath.Join(filepath.Dir(cleanTarget), linkTarget)
			}
			if err := os.Link(linkTarget, cleanTarget); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(cleanTarget), 0o755); err != nil {
				return err
			}
			_ = os.Remove(cleanTarget)
			if err := os.Symlink(hdr.Linkname, cleanTarget); err != nil {
				return err
			}
		}
	}

	return nil
}

func extractZip(archivePath, dst string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()

	cleanDst := filepath.Clean(dst)
	for _, file := range reader.File {
		target := filepath.Join(dst, file.Name)
		cleanTarget := filepath.Clean(target)
		if !strings.HasPrefix(cleanTarget, cleanDst+string(os.PathSeparator)) && cleanTarget != cleanDst {
			return fmt.Errorf("zip entry escapes destination: %q", file.Name)
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(cleanTarget, 0o755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(cleanTarget), 0o755); err != nil {
			return err
		}
		rc, err := file.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(cleanTarget, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.Mode())
		if err != nil {
			_ = rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			_ = out.Close()
			_ = rc.Close()
			return err
		}
		if err := out.Close(); err != nil {
			_ = rc.Close()
			return err
		}
		if err := rc.Close(); err != nil {
			return err
		}
	}

	return nil
}

func findFileInTree(root, baseName string) (string, error) {
	baseName = strings.TrimSpace(baseName)
	if baseName == "" {
		return "", fmt.Errorf("file name is required")
	}
	candidate := filepath.Join(root, baseName)
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}

	found := ""
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Name() == baseName {
			found = path
			return io.EOF
		}
		return nil
	})
	if err == io.EOF && found != "" {
		return found, nil
	}
	if err != nil {
		return "", err
	}
	return "", fmt.Errorf("could not find %q in %s", baseName, root)
}

func writeCaskReceipt(caskDir, token, version, appPath string, linkedBinaries []string) error {
	receipt := caskInstallReceipt{
		Token:          token,
		Version:        version,
		AppPath:        appPath,
		LinkedBinaries: linkedBinaries,
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(caskDir, "INSTALL_RECEIPT.json")
	return os.WriteFile(path, data, 0o644)
}

func caskAppRemovalCandidates(appPath, managedApplications string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 4)
	add := func(path string) {
		cleaned := filepath.Clean(strings.TrimSpace(path))
		if cleaned == "" || cleaned == "." {
			return
		}
		if seen[cleaned] {
			return
		}
		seen[cleaned] = true
		out = append(out, cleaned)
	}

	add(appPath)

	base := filepath.Base(strings.TrimSpace(appPath))
	if base == "" || base == "." {
		return out
	}
	if !strings.EqualFold(filepath.Ext(base), ".app") {
		return out
	}

	add(filepath.Join(managedApplications, base))
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		add(filepath.Join(home, "Applications", base))
	}
	if runtime.GOOS == "darwin" {
		add(filepath.Join(string(filepath.Separator), "Applications", base))
	}

	return out
}

func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "status 404")
}

func isZipArchive(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	header := make([]byte, 4)
	n, err := f.Read(header)
	if err != nil && err != io.EOF {
		return false, err
	}
	if n < 4 {
		return false, nil
	}
	return header[0] == 'P' && header[1] == 'K' && header[2] == 0x03 && header[3] == 0x04, nil
}

func (m *Manager) linkFormula(name, version string) (string, error) {
	installDir, linkedVersion, err := resolveInstalledFormulaDir(m.Paths.Cellar, name, version)
	if err != nil {
		return "", err
	}
	if err := m.linkTree(installDir, m.Paths.Bin, "bin"); err != nil {
		return "", err
	}
	if err := m.linkTree(installDir, m.Paths.Sbin, "sbin"); err != nil {
		return "", err
	}
	return linkedVersion, nil
}

func resolveInstalledFormulaDir(cellar, name, version string) (string, string, error) {
	formulaDir := filepath.Join(cellar, name)
	exact := filepath.Join(formulaDir, version)
	if info, err := os.Stat(exact); err == nil && info.IsDir() {
		return exact, version, nil
	}

	entries, err := os.ReadDir(formulaDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("formula %q is not installed", name)
		}
		return "", "", err
	}

	matches := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		entryName := entry.Name()
		if entryName == version || strings.HasPrefix(entryName, version+"_") {
			matches = append(matches, entryName)
		}
	}

	if len(matches) == 0 {
		for _, entry := range entries {
			if entry.IsDir() {
				matches = append(matches, entry.Name())
			}
		}
	}

	if len(matches) == 0 {
		return "", "", fmt.Errorf("formula %q has no installed versions", name)
	}

	sort.Strings(matches)
	resolvedVersion := matches[len(matches)-1]
	return filepath.Join(formulaDir, resolvedVersion), resolvedVersion, nil
}

func (m *Manager) linkTree(installDir, linkRoot, leaf string) error {
	srcDir := filepath.Join(installDir, leaf)
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		src := filepath.Join(srcDir, entry.Name())
		dst := filepath.Join(linkRoot, entry.Name())
		_ = os.Remove(dst)
		if err := os.Symlink(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) unlinkTree(formulaDir, linkRoot, leaf string) error {
	versions, err := os.ReadDir(formulaDir)
	if err != nil {
		return err
	}
	for _, version := range versions {
		if !version.IsDir() {
			continue
		}
		srcDir := filepath.Join(formulaDir, version.Name(), leaf)
		entries, err := os.ReadDir(srcDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		for _, entry := range entries {
			dst := filepath.Join(linkRoot, entry.Name())
			info, err := os.Lstat(dst)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return err
			}
			if info.Mode()&os.ModeSymlink == 0 {
				continue
			}
			target, err := os.Readlink(dst)
			if err != nil {
				return err
			}
			if strings.Contains(target, formulaDir+string(os.PathSeparator)) {
				if err := os.Remove(dst); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
