package discover

import "maps"

import "sort"

// upgradePlan holds the computed wave schedule for all upgradeable devices.
type upgradePlan struct {
	waves    []upgradeWave
	excluded []string // non-source discovered nodes, not scheduled
}

// upgradeWave is a single batch of devices that can be upgraded in parallel.
type upgradeWave struct {
	index   int
	devices []string
}

// buildSpanningTree performs a BFS from root over nodes in inComponent and
// returns the spanning-tree parent map (child → parent) and a sorted children
// map (parent → []child). The level map from the previous inline BFS is dropped
// because it was never read after construction.
func buildSpanningTree(graph *topologyGraph, root string, inComponent map[string]bool, orderedHosts []string) (parent map[string]string, children map[string][]string) {
	parent = make(map[string]string)
	queue := []string{root}
	visited := map[string]bool{root: true}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		neighbors := graph.neighbors(current)
		sort.Slice(neighbors, func(i, j int) bool {
			return betterNode(graph, neighbors[i], neighbors[j], orderedHosts)
		})
		for _, next := range neighbors {
			if !inComponent[next] || visited[next] {
				continue
			}
			visited[next] = true
			parent[next] = current
			queue = append(queue, next)
		}
	}

	children = make(map[string][]string)
	for child, p := range parent {
		children[p] = append(children[p], child)
	}
	for p := range children {
		sort.Slice(children[p], func(i, j int) bool {
			return betterNode(graph, children[p][i], children[p][j], orderedHosts)
		})
	}

	return parent, children
}

// greedyIndependentSet returns a maximal independent set from candidates: no
// two selected nodes share a direct link in the full undirected graph.
// Candidates are sorted deterministically by tieOrder before selection,
// ensuring the first candidate is always accepted (termination guarantee).
func greedyIndependentSet(candidates []string, graph *topologyGraph, orderedHosts []string) []string {
	sort.Slice(candidates, func(i, j int) bool {
		ni := graph.nodes[candidates[i]]
		nj := graph.nodes[candidates[j]]
		if ni == nil || nj == nil {
			return candidates[i] < candidates[j]
		}
		oi := tieOrder(ni, orderedHosts)
		oj := tieOrder(nj, orderedHosts)
		if oi != oj {
			return oi < oj
		}
		if ni.firstSeen != nj.firstSeen {
			return ni.firstSeen < nj.firstSeen
		}
		return candidates[i] < candidates[j]
	})

	selected := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		adjacent := false
		for _, s := range selected {
			if _, linked := graph.undirected[pairKey(candidate, s)]; linked {
				adjacent = true
				break
			}
		}
		if !adjacent {
			selected = append(selected, candidate)
		}
	}

	return selected
}

// buildUpgradePlan computes upgrade waves for all source nodes in the graph.
// The algorithm is leaf-first BFS scheduling with an adjacency safety guard:
//
//  1. Only isSource nodes (excluding the synthetic mfa node) are scheduled.
//  2. A BFS spanning tree is computed per component using buildSpanningTree.
//  3. Nodes are scheduled in waves from leaves toward roots. Within each wave,
//     greedyIndependentSet ensures no two adjacent nodes share the same wave.
//
// Non-source discovered devices are reported in the excluded list.
func buildUpgradePlan(graph *topologyGraph, orderedHosts []string) *upgradePlan {
	plan := &upgradePlan{}

	// Phase 1: identify upgradeable and excluded nodes.
	upgradeable := make(map[string]bool)
	for name, node := range graph.nodes {
		if node.isSource && name != mfaNodeName {
			upgradeable[name] = true
		}
	}

	excluded := make([]string, 0)
	for name, node := range graph.nodes {
		if !node.isSource && name != mfaNodeName {
			excluded = append(excluded, name)
		}
	}
	sort.Strings(excluded)
	plan.excluded = excluded

	if len(upgradeable) == 0 {
		return plan
	}

	// Phase 2: build spanning trees per component, compute child counts.
	components := connectedComponents(graph)
	roots := selectRoots(graph, components, orderedHosts, preferredRoot(graph))

	parentOf := make(map[string]string)           // child → spanning-tree parent
	upgradeableChildCount := make(map[string]int) // upgradeable children remaining per node

	for i, component := range components {
		root := roots[i]
		inComponent := make(map[string]bool)
		for _, name := range component {
			inComponent[name] = true
		}

		parent, children := buildSpanningTree(graph, root, inComponent, orderedHosts)

		maps.Copy(parentOf, parent)
		for p, kids := range children {
			for _, kid := range kids {
				if upgradeable[kid] {
					upgradeableChildCount[p]++
				}
			}
		}
	}

	// Phase 3: iterative ready-set scheduling.
	// A node is "ready" when all its upgradeable spanning-tree children have
	// been scheduled. The root of each component is naturally scheduled last
	// because its child count only reaches zero after all descendants are done.
	remaining := make(map[string]bool, len(upgradeable))
	for name := range upgradeable {
		remaining[name] = true
	}

	waveIndex := 1
	for len(remaining) > 0 {
		ready := make([]string, 0, len(remaining))
		for name := range remaining {
			if upgradeableChildCount[name] == 0 {
				ready = append(ready, name)
			}
		}

		if len(ready) == 0 {
			// Defensive fallback: should never occur in a valid spanning tree.
			// Schedule all remaining nodes in deterministic order.
			for name := range remaining {
				ready = append(ready, name)
			}
			sort.Strings(ready)
		}

		wave := greedyIndependentSet(ready, graph, orderedHosts)
		plan.waves = append(plan.waves, upgradeWave{index: waveIndex, devices: wave})
		waveIndex++

		for _, name := range wave {
			delete(remaining, name)
			if p, ok := parentOf[name]; ok && upgradeable[p] {
				upgradeableChildCount[p]--
			}
		}
	}

	return plan
}
