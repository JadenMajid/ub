package lock

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

type FileLock struct {
	path string
	held bool
}

func Acquire(rootDir string) (*FileLock, error) {
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("create root dir for lock: %w", err)
	}

	path := filepath.Join(rootDir, ".ub.lock")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("install root is already locked: %s", path)
		}
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	if _, err := f.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write lock pid: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("close lock file: %w", err)
	}

	return &FileLock{path: path, held: true}, nil
}

func (l *FileLock) Release() error {
	if l == nil || !l.held {
		return nil
	}
	l.held = false
	if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("release lock: %w", err)
	}
	return nil
}
