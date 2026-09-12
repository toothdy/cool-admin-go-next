package codegen

import "sort"

func findComponentCycle(nodes []graphComponent, dependencies []graphDependency) ([]string, Position) {
	adjacency := make([][]graphDependency, len(nodes))
	for _, dependency := range dependencies {
		if dependency.providerComponent < 0 {
			continue
		}
		adjacency[dependency.consumer] = append(adjacency[dependency.consumer], dependency)
	}
	for index := range adjacency {
		sort.Slice(adjacency[index], func(left, right int) bool {
			first := nodes[adjacency[index][left].providerComponent].component
			second := nodes[adjacency[index][right].providerComponent].component
			return compareComponents(first, second, nil) < 0
		})
	}

	states := make([]uint8, len(nodes))
	var stack []int
	var visit func(int) ([]string, Position)
	visit = func(current int) ([]string, Position) {
		states[current] = 1
		stack = append(stack, current)
		for _, dependency := range adjacency[current] {
			providerIndex := dependency.providerComponent
			if providerIndex < 0 {
				continue
			}
			switch states[providerIndex] {
			case 0:
				if cycle, position := visit(providerIndex); len(cycle) > 0 {
					return cycle, position
				}
			case 1:
				start := 0
				for stack[start] != providerIndex {
					start++
				}
				cycle := make([]string, 0, len(stack)-start+1)
				for _, nodeIndex := range stack[start:] {
					cycle = append(cycle, componentLabel(nodes[nodeIndex].component, nodes))
				}
				cycle = append(cycle, componentLabel(nodes[providerIndex].component, nodes))
				return cycle, dependency.position
			}
		}
		stack = stack[:len(stack)-1]
		states[current] = 2
		return nil, Position{}
	}

	order := stableNodeIndexes(nodes, nil)
	for _, current := range order {
		if states[current] == 0 {
			if cycle, position := visit(current); len(cycle) > 0 {
				return cycle, position
			}
		}
	}
	return nil, Position{}
}

func findModuleCycle(modules []ModuleNode, dependencies []ModuleDependency) ([]string, Position) {
	adjacency := make(map[string][]ModuleDependency, len(modules))
	for _, dependency := range dependencies {
		adjacency[dependency.consumer] = append(adjacency[dependency.consumer], dependency)
	}
	for key := range adjacency {
		sort.Slice(adjacency[key], func(left, right int) bool {
			return adjacency[key][left].provider < adjacency[key][right].provider
		})
	}
	states := make(map[string]uint8, len(modules))
	var stack []string
	var visit func(string) ([]string, Position)
	visit = func(current string) ([]string, Position) {
		states[current] = 1
		stack = append(stack, current)
		for _, dependency := range adjacency[current] {
			switch states[dependency.provider] {
			case 0:
				if cycle, position := visit(dependency.provider); len(cycle) > 0 {
					return cycle, position
				}
			case 1:
				start := 0
				for stack[start] != dependency.provider {
					start++
				}
				cycle := append([]string(nil), stack[start:]...)
				cycle = append(cycle, dependency.provider)
				return cycle, cyclePos(cycle, dependencies)
			}
		}
		stack = stack[:len(stack)-1]
		states[current] = 2
		return nil, Position{}
	}
	keys := make([]string, len(modules))
	for index, module := range modules {
		keys[index] = module.key
	}
	sort.Strings(keys)
	for _, key := range keys {
		if states[key] == 0 {
			if cycle, position := visit(key); len(cycle) > 0 {
				return cycle, position
			}
		}
	}
	return nil, Position{}
}

func topologicalOrder(nodes []graphComponent, dependencies []graphDependency, modules []ModuleNode) []Component {
	edges := make([][]int, len(nodes))
	indegree := make([]int, len(nodes))
	for _, dependency := range dependencies {
		provider := dependency.providerComponent
		if provider < 0 {
			continue
		}
		edges[provider] = append(edges[provider], dependency.consumer)
		indegree[dependency.consumer]++
	}
	ready := make([]int, 0, len(nodes))
	for index, degree := range indegree {
		if degree == 0 {
			ready = append(ready, index)
		}
	}
	var result []Component
	for len(ready) > 0 {
		sort.Slice(ready, func(left, right int) bool {
			return compareComponents(nodes[ready[left]].component, nodes[ready[right]].component, modules) < 0
		})
		current := ready[0]
		ready = ready[1:]
		result = append(result, nodes[current].component)
		for _, next := range edges[current] {
			indegree[next]--
			if indegree[next] == 0 {
				ready = append(ready, next)
			}
		}
	}
	return result
}

func stableNodeIndexes(nodes []graphComponent, modules []ModuleNode) []int {
	indexes := make([]int, len(nodes))
	for index := range nodes {
		indexes[index] = index
	}
	sort.Slice(indexes, func(left, right int) bool {
		return compareComponents(nodes[indexes[left]].component, nodes[indexes[right]].component, modules) < 0
	})
	return indexes
}

func cyclePos(cycle []string, dependencies []ModuleDependency) Position {
	if len(cycle) < 2 {
		return Position{}
	}
	for _, dependency := range dependencies {
		if dependency.consumer == cycle[0] && dependency.provider == cycle[1] {
			return dependency.position
		}
	}
	return Position{}
}
