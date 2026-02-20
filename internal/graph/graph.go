package graph

import (
	"fmt"
	"sort"

	"ub/internal/formula"
)

type Plan struct {
	Order  []string
	Layers [][]string
}

func BuildPlan(formulas map[string]formula.Formula) (Plan, error) {
	inDegree := map[string]int{}
	dependents := map[string][]string{}

	for name := range formulas {
		inDegree[name] = 0
	}

	for name, f := range formulas {
		for _, dep := range f.Deps {
			if _, ok := formulas[dep]; !ok {
				return Plan{}, fmt.Errorf("formula %q depends on unknown formula %q", name, dep)
			}
			inDegree[name]++
			dependents[dep] = append(dependents[dep], name)
		}
	}

	level := []string{}
	for name, degree := range inDegree {
		if degree == 0 {
			level = append(level, name)
		}
	}
	sort.Strings(level)

	processed := 0
	order := make([]string, 0, len(formulas))
	layers := make([][]string, 0)

	for len(level) > 0 {
		current := append([]string(nil), level...)
		layers = append(layers, current)

		nextMap := map[string]struct{}{}
		for _, node := range current {
			order = append(order, node)
			processed++
			for _, dependent := range dependents[node] {
				inDegree[dependent]--
				if inDegree[dependent] == 0 {
					nextMap[dependent] = struct{}{}
				}
			}
		}

		next := make([]string, 0, len(nextMap))
		for node := range nextMap {
			next = append(next, node)
		}
		sort.Strings(next)
		level = next
	}

	if processed != len(formulas) {
		return Plan{}, fmt.Errorf("dependency graph contains a cycle")
	}

	return Plan{Order: order, Layers: layers}, nil
}
