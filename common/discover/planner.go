package discover

import (
	"fmt"
	"sort"
	"strings"

	"gonum.org/v1/gonum/graph/simple"
)

// UpgradePlan holds the computed wave schedule for upgradeable devices.
type UpgradePlan struct {
	Waves    []UpgradeWave
	Excluded []string // discovered neighbors that are not source hosts
}

// UpgradeWave is one batch of devices that can be updated in parallel.
type UpgradeWave struct {
	Index   int
	Devices []string
}

type plannerNode struct {
	name        string
	isSource    bool
	sourceOrder int
	firstSeen   int
	graphID     int64
}

type plannerGraph struct {
	g             *simple.UndirectedGraph
	nodes         map[string]*plannerNode
	idToName      map[int64]string
	undirected    map[string]struct{}
	nextFirstSeen int
}

// BuildUpgradePlan computes wave-ordered update execution for source hosts.
func BuildUpgradePlan(topo *Topology) (*UpgradePlan, error) {
	if topo == nil {
		return nil, fmt.Errorf("topology is nil")
	}

	graph := buildPlannerGraph(topo)
	return buildUpgradePlan(graph, topo.OrderedHosts), nil
}

func buildPlannerGraph(topo *Topology) *plannerGraph {
	graph := &plannerGraph{
		g:          simple.NewUndirectedGraph(),
		nodes:      make(map[string]*plannerNode),
		idToName:   make(map[int64]string),
		undirected: make(map[string]struct{}),
	}

	hostOrder := make(map[string]int, len(topo.OrderedHosts))
	for idx, host := range topo.OrderedHosts {
		hostOrder[host] = idx
		graph.getOrCreateNode(host, true, idx)
	}

	for _, sourceHost := range topo.OrderedHosts {
		result := topo.Results[sourceHost]
		if result == nil {
			continue
		}
		sourceNode := graph.getOrCreateNode(sourceHost, true, hostOrder[sourceHost])
		for _, neighbor := range result.Neighbors {
			identity := strings.TrimSpace(neighbor.Identity)
			if identity == "" {
				continue
			}
			destination := graph.getOrCreateNode(identity, false, -1)
			graph.addLink(sourceNode.name, destination.name)
		}
	}

	return graph
}

func (g *plannerGraph) getOrCreateNode(name string, isSource bool, sourceOrder int) *plannerNode {
	node, ok := g.nodes[name]
	if ok {
		if isSource {
			node.isSource = true
			if sourceOrder >= 0 && (node.sourceOrder < 0 || sourceOrder < node.sourceOrder) {
				node.sourceOrder = sourceOrder
			}
		}
		return node
	}

	n := g.g.NewNode()
	g.g.AddNode(n)
	node = &plannerNode{
		name:        name,
		isSource:    isSource,
		sourceOrder: sourceOrder,
		firstSeen:   g.nextFirstSeen,
		graphID:     n.ID(),
	}
	g.nextFirstSeen++
	g.nodes[name] = node
	g.idToName[n.ID()] = name
	return node
}

func (g *plannerGraph) addLink(from, to string) {
	if from == "" || to == "" {
		return
	}
	fromNode := g.nodes[from]
	toNode := g.nodes[to]
	if fromNode == nil || toNode == nil {
		return
	}
	g.undirected[pairKey(from, to)] = struct{}{}
	if g.g.Edge(fromNode.graphID, toNode.graphID) == nil {
		g.g.SetEdge(simple.Edge{F: g.g.Node(fromNode.graphID), T: g.g.Node(toNode.graphID)})
	}
}

func buildSpanningTree(graph *plannerGraph, root string, inComponent map[string]bool, orderedHosts []string) (parent map[string]string, children map[string][]string) {
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

func greedyIndependentSet(candidates []string, graph *plannerGraph, orderedHosts []string) []string {
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

func buildUpgradePlan(graph *plannerGraph, orderedHosts []string) *UpgradePlan {
	plan := &UpgradePlan{}

	upgradeable := make(map[string]bool)
	for name, node := range graph.nodes {
		if node.isSource {
			upgradeable[name] = true
		}
	}

	excluded := make([]string, 0)
	for name, node := range graph.nodes {
		if !node.isSource {
			excluded = append(excluded, name)
		}
	}
	sort.Strings(excluded)
	plan.Excluded = excluded

	if len(upgradeable) == 0 {
		return plan
	}

	selectUpgradeableComponentRoot := func(component []string, fallback string) string {
		inComponent := make(map[string]bool, len(component))
		hasUpgradeable := false
		for _, name := range component {
			inComponent[name] = true
			if upgradeable[name] {
				hasUpgradeable = true
			}
		}
		if !hasUpgradeable {
			return fallback
		}
		for _, name := range orderedHosts {
			if inComponent[name] && upgradeable[name] {
				return name
			}
		}

		candidates := make([]string, 0, len(component))
		for _, name := range component {
			if upgradeable[name] {
				candidates = append(candidates, name)
			}
		}
		sort.Strings(candidates)
		return candidates[0]
	}

	linkUpgradeableDescendants := func(root string, children map[string][]string, parentOf map[string]string, upgradeableChildCount map[string]int) {
		var visit func(string, string)
		visit = func(name string, nearestUpgradeableAncestor string) {
			currentUpgradeableAncestor := nearestUpgradeableAncestor
			if upgradeable[name] {
				if nearestUpgradeableAncestor != "" {
					parentOf[name] = nearestUpgradeableAncestor
					upgradeableChildCount[nearestUpgradeableAncestor]++
				}
				currentUpgradeableAncestor = name
			}
			for _, child := range children[name] {
				visit(child, currentUpgradeableAncestor)
			}
		}
		visit(root, "")
	}

	components := connectedComponents(graph)
	roots := selectRoots(graph, components, orderedHosts)

	parentOf := make(map[string]string)
	upgradeableChildCount := make(map[string]int)

	for i, component := range components {
		root := selectUpgradeableComponentRoot(component, roots[i])
		inComponent := make(map[string]bool)
		for _, name := range component {
			inComponent[name] = true
		}

		_, children := buildSpanningTree(graph, root, inComponent, orderedHosts)
		linkUpgradeableDescendants(root, children, parentOf, upgradeableChildCount)
	}

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
			for name := range remaining {
				ready = append(ready, name)
			}
			sort.Strings(ready)
		}

		wave := greedyIndependentSet(ready, graph, orderedHosts)
		plan.Waves = append(plan.Waves, UpgradeWave{Index: waveIndex, Devices: wave})
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

func connectedComponents(graph *plannerGraph) [][]string {
	visited := make(map[string]bool)
	components := make([][]string, 0)

	names := make([]string, 0, len(graph.nodes))
	for name := range graph.nodes {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return graph.nodes[names[i]].firstSeen < graph.nodes[names[j]].firstSeen
	})

	for _, start := range names {
		if visited[start] {
			continue
		}
		queue := []string{start}
		component := make([]string, 0)
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			if visited[current] {
				continue
			}
			visited[current] = true
			component = append(component, current)
			for _, neighbor := range graph.neighbors(current) {
				if !visited[neighbor] {
					queue = append(queue, neighbor)
				}
			}
		}
		components = append(components, component)
	}

	return components
}

func selectRoots(graph *plannerGraph, components [][]string, orderedHosts []string) []string {
	roots := make([]string, 0, len(components))
	for _, component := range components {
		best := component[0]
		for _, candidate := range component[1:] {
			if betterNode(graph, candidate, best, orderedHosts) {
				best = candidate
			}
		}
		roots = append(roots, best)
	}
	return roots
}

func betterNode(graph *plannerGraph, left, right string, orderedHosts []string) bool {
	l := graph.nodes[left]
	r := graph.nodes[right]
	if l == nil || r == nil {
		return false
	}
	lDegree := graph.degree(left)
	rDegree := graph.degree(right)
	if lDegree != rDegree {
		return lDegree > rDegree
	}
	lOrder := tieOrder(l, orderedHosts)
	rOrder := tieOrder(r, orderedHosts)
	if lOrder != rOrder {
		return lOrder < rOrder
	}
	return l.firstSeen < r.firstSeen
}

func tieOrder(node *plannerNode, orderedHosts []string) int {
	if node.sourceOrder >= 0 {
		return node.sourceOrder
	}
	return len(orderedHosts) + node.firstSeen + 1
}

func (g *plannerGraph) degree(name string) int {
	node := g.nodes[name]
	if node == nil {
		return 0
	}
	it := g.g.From(node.graphID)
	count := 0
	for it.Next() {
		count++
	}
	return count
}

func (g *plannerGraph) neighbors(name string) []string {
	node := g.nodes[name]
	if node == nil {
		return nil
	}
	neighbors := make([]string, 0)
	it := g.g.From(node.graphID)
	for it.Next() {
		id := it.Node().ID()
		if neighborName, ok := g.idToName[id]; ok {
			neighbors = append(neighbors, neighborName)
		}
	}
	sort.Slice(neighbors, func(i, j int) bool {
		return g.nodes[neighbors[i]].firstSeen < g.nodes[neighbors[j]].firstSeen
	})
	return neighbors
}

func pairKey(a, b string) string {
	if a <= b {
		return a + "|" + b
	}
	return b + "|" + a
}
