package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"ub/internal/fetch"
	"ub/internal/formula"
	"ub/internal/lock"
	"ub/internal/scheduler"
)

type Installer struct {
	TapDir  string
	RootDir string
	CacheDir string
	Jobs    int
}

type installReceipt struct {
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	InstalledAt time.Time `json:"installed_at"`
	TapDir      string    `json:"tap_dir"`
}

type formulaJob struct {
	formula formula.Formula
	rootDir string
	tapDir  string
	fetcher *fetch.Cache
}

func (j formulaJob) ID() string {
	return j.formula.Name
}

func (j formulaJob) Requires() []string {
	return j.formula.Deps
}

func (j formulaJob) Run(ctx context.Context) error {
	if _, err := j.fetcher.Fetch(ctx, j.formula.Source.URL); err != nil {
		return err
	}
	if err := j.runBuildSteps(ctx); err != nil {
		return err
	}
	return j.writeReceipt()
}

func (j formulaJob) runBuildSteps(ctx context.Context) error {
	if len(j.formula.Build.Steps) == 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
			return nil
		}
	}

	workDir := filepath.Join(j.rootDir, ".work", j.formula.Name)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}

	for _, step := range j.formula.Build.Steps {
		cmd := exec.CommandContext(ctx, "sh", "-c", step)
		cmd.Dir = workDir
		cmd.Env = []string{
			"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
			"HOME=" + workDir,
			"UB_FORMULA_NAME=" + j.formula.Name,
			"UB_FORMULA_VERSION=" + j.formula.Version,
		}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("build step failed (%s): %w", step, err)
		}
	}

	return nil
}

func (j formulaJob) writeReceipt() error {
	installDir := filepath.Join(j.rootDir, j.formula.Name, j.formula.Version)
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return fmt.Errorf("create install dir: %w", err)
	}

	receipt := installReceipt{
		Name:        j.formula.Name,
		Version:     j.formula.Version,
		InstalledAt: time.Now().UTC(),
		TapDir:      j.tapDir,
	}

	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal receipt: %w", err)
	}

	path := filepath.Join(installDir, "INSTALL_RECEIPT.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write receipt: %w", err)
	}

	return nil
}

func (i Installer) Install(ctx context.Context, formulas map[string]formula.Formula) error {
	installLock, err := lock.Acquire(i.RootDir)
	if err != nil {
		return err
	}
	defer installLock.Release()

	fetcher := fetch.NewCache(i.CacheDir)
	jobs := make([]scheduler.Job, 0, len(formulas))
	for _, f := range formulas {
		jobs = append(jobs, formulaJob{formula: f, rootDir: i.RootDir, tapDir: i.TapDir, fetcher: fetcher})
	}

	executor := scheduler.Executor{Workers: i.Jobs}
	return executor.Run(ctx, jobs)
}
