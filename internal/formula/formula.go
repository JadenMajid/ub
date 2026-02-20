package formula

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type Source struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type Build struct {
	Steps []string `json:"steps"`
}

type Formula struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Deps    []string `json:"deps"`
	Source  Source   `json:"source"`
	Build   Build    `json:"build"`
}

func (f Formula) Validate() error {
	if f.Name == "" {
		return fmt.Errorf("formula missing name")
	}
	if f.Version == "" {
		return fmt.Errorf("formula %q missing version", f.Name)
	}
	return nil
}

func LoadByName(tapDir, name string) (Formula, error) {
	file := filepath.Join(tapDir, name+".json")
	data, err := os.ReadFile(file)
	if err != nil {
		return Formula{}, fmt.Errorf("read formula %q: %w", name, err)
	}

	var f Formula
	if err := json.Unmarshal(data, &f); err != nil {
		return Formula{}, fmt.Errorf("parse formula %q: %w", name, err)
	}
	if f.Name == "" {
		f.Name = name
	}
	if err := f.Validate(); err != nil {
		return Formula{}, err
	}

	return f, nil
}

func ResolveClosure(tapDir string, roots []string) (map[string]Formula, error) {
	seen := map[string]Formula{}
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

		f, err := LoadByName(tapDir, name)
		if err != nil {
			return err
		}

		sort.Strings(f.Deps)
		for _, dep := range f.Deps {
			if dep == f.Name {
				return fmt.Errorf("formula %q cannot depend on itself", f.Name)
			}
			if err := dfs(dep); err != nil {
				return fmt.Errorf("resolve dep %q for %q: %w", dep, name, err)
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
