package discover

import (
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"gonum.org/v1/gonum/graph/simple"
	"jb.favre/mikrotik-fleet-autopilot/common/lldp"
)

type topologyNode struct {
	canonical   string
	displayName string
	isSource    bool
	sourceOrder int
	firstSeen   int
	graphID     int64
}

type linkDetail struct {
	from           string
	to             string
	localInterface string
	remoteIface    string
	protocols      []string
}

type topologyGraph struct {
	g             *simple.UndirectedGraph
	nodes         map[string]*topologyNode
	idToName      map[int64]string
	aliases       map[string]string
	ambiguous     map[string]bool
	outgoing      map[string]map[string][]*linkDetail
	undirected    map[string][]*linkDetail
	nextNodeID    int64
	nextFirstSeen int
}

func outputTopology(topo *topology) error {
	out := os.Stdout

	if len(topo.errors) > 0 {
		fmt.Fprintf(out, "Discovery Errors:\n")
		for host, err := range topo.errors {
			fmt.Fprintf(out, "  %s: %v\n", host, err)
		}
		fmt.Fprintf(out, "\n")
	}

	if len(topo.results) == 0 {
		fmt.Fprintf(out, "No neighbors discovered.\n")
		return nil
	}

	graph := buildTopologyGraph(topo.results, topo.orderedHosts)
	slog.Info("topology graph built",
		"vertices", len(graph.nodes),
		"resolved_aliases", len(graph.aliases),
		"ambiguous_aliases", len(graph.ambiguous),
	)

	fmt.Fprintf(out, "LLDP Topology Graph\n")
	fmt.Fprintf(out, "%s\n\n", strings.Repeat("═", 63))

	renderTopologyGraph(out, graph, topo.orderedHosts)
	return nil
}

func buildTopologyGraph(results map[string]*lldp.ParseResult, orderedHosts []string) *topologyGraph {
	graph := &topologyGraph{
		g:          simple.NewUndirectedGraph(),
		nodes:      make(map[string]*topologyNode),
		idToName:   make(map[int64]string),
		aliases:    make(map[string]string),
		ambiguous:  make(map[string]bool),
		outgoing:   make(map[string]map[string][]*linkDetail),
		undirected: make(map[string][]*linkDetail),
		nextNodeID: 1,
	}

	for idx, host := range orderedHosts {
		result := results[host]
		node := graph.getOrCreateNode(host, host, true, idx)
		graph.addAlias(host, node.canonical)
		graph.addAlias(shortName(host), node.canonical)
		if result != nil {
			graph.addAlias(result.SourceIdentity, node.canonical)
			graph.addAlias(shortName(result.SourceIdentity), node.canonical)
		}
	}

	for sourceHost, result := range results {
		sourceNode := graph.getOrCreateNode(sourceHost, sourceHost, true, graph.sourceOrderOf(sourceHost, orderedHosts))
		for _, neighbor := range result.Neighbors {
			dstCanonical, dstDisplay, resolved := graph.resolveIdentity(neighbor.Identity)
			destination := graph.getOrCreateNode(dstCanonical, dstDisplay, resolved, -1)
			graph.addLink(sourceNode.canonical, destination.canonical, &linkDetail{
				from:           sourceNode.canonical,
				to:             destination.canonical,
				localInterface: neighbor.LocalInterface,
				remoteIface:    neighbor.RemoteInterface,
				protocols:      neighbor.DiscoveredBy,
			})
		}
	}

	return graph
}

func (g *topologyGraph) sourceOrderOf(host string, orderedHosts []string) int {
	for idx, h := range orderedHosts {
		if h == host {
			return idx
		}
	}
	return -1
}

func (g *topologyGraph) getOrCreateNode(canonical, display string, isSource bool, sourceOrder int) *topologyNode {
	node, ok := g.nodes[canonical]
	if ok {
		if isSource {
			node.isSource = true
			if sourceOrder >= 0 && (node.sourceOrder < 0 || sourceOrder < node.sourceOrder) {
				node.sourceOrder = sourceOrder
			}
			node.displayName = canonical
		}
		return node
	}

	n := g.g.NewNode()
	g.g.AddNode(n)
	node = &topologyNode{
		canonical:   canonical,
		displayName: display,
		isSource:    isSource,
		sourceOrder: sourceOrder,
		firstSeen:   g.nextFirstSeen,
		graphID:     n.ID(),
	}
	g.nextFirstSeen++
	g.nodes[canonical] = node
	g.idToName[n.ID()] = canonical
	if _, ok := g.outgoing[canonical]; !ok {
		g.outgoing[canonical] = make(map[string][]*linkDetail)
	}
	return node
}

func (g *topologyGraph) addAlias(alias, canonical string) {
	if alias == "" {
		return
	}
	if g.ambiguous[alias] {
		return
	}
	if existing, ok := g.aliases[alias]; ok && existing != canonical {
		delete(g.aliases, alias)
		g.ambiguous[alias] = true
		return
	}
	g.aliases[alias] = canonical
}

func (g *topologyGraph) resolveIdentity(identity string) (canonical string, display string, resolved bool) {
	if identity == "" {
		return "unknown", "unknown", false
	}
	if c, ok := g.aliases[identity]; ok {
		return c, c, true
	}
	short := shortName(identity)
	if c, ok := g.aliases[short]; ok {
		return c, c, true
	}
	return identity, identity, false
}

func (g *topologyGraph) addLink(from, to string, detail *linkDetail) {
	if from == "" || to == "" {
		return
	}
	if _, ok := g.outgoing[from]; !ok {
		g.outgoing[from] = make(map[string][]*linkDetail)
	}
	g.outgoing[from][to] = append(g.outgoing[from][to], detail)

	pair := pairKey(from, to)
	g.undirected[pair] = append(g.undirected[pair], detail)

	fromNode := g.nodes[from]
	toNode := g.nodes[to]
	if fromNode == nil || toNode == nil {
		return
	}
	if g.g.Edge(fromNode.graphID, toNode.graphID) == nil {
		g.g.SetEdge(simple.Edge{F: g.g.Node(fromNode.graphID), T: g.g.Node(toNode.graphID)})
	}
}

func renderTopologyGraph(out *os.File, graph *topologyGraph, orderedHosts []string) {
	components := connectedComponents(graph)
	roots := selectRoots(graph, components, orderedHosts)

	totalTreeEdges := 0
	for i, root := range roots {
		if i > 0 {
			fmt.Fprintf(out, "\n")
		}
		treeEdges := renderComponent(out, graph, components[i], root)
		totalTreeEdges += len(treeEdges)
	}

	printSummary(out, graph, totalTreeEdges)
}

func connectedComponents(graph *topologyGraph) [][]string {
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

func selectRoots(graph *topologyGraph, components [][]string, orderedHosts []string) []string {
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

func betterNode(graph *topologyGraph, left, right string, orderedHosts []string) bool {
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

func tieOrder(node *topologyNode, orderedHosts []string) int {
	if node.sourceOrder >= 0 {
		return node.sourceOrder
	}
	return len(orderedHosts) + node.firstSeen + 1
}

func (g *topologyGraph) degree(name string) int {
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

func (g *topologyGraph) neighbors(name string) []string {
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

func renderComponent(out *os.File, graph *topologyGraph, component []string, root string) map[string]bool {
	inComponent := make(map[string]bool)
	for _, name := range component {
		inComponent[name] = true
	}

	parent := make(map[string]string)
	level := make(map[string]int)
	queue := []string{root}
	visited := map[string]bool{root: true}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		neighbors := graph.neighbors(current)
		sort.Slice(neighbors, func(i, j int) bool {
			return betterNode(graph, neighbors[i], neighbors[j], nil)
		})
		for _, next := range neighbors {
			if !inComponent[next] || visited[next] {
				continue
			}
			visited[next] = true
			parent[next] = current
			level[next] = level[current] + 1
			queue = append(queue, next)
		}
	}

	children := make(map[string][]string)
	for child, p := range parent {
		children[p] = append(children[p], child)
	}
	for p := range children {
		sort.Slice(children[p], func(i, j int) bool {
			return betterNode(graph, children[p][i], children[p][j], nil)
		})
	}

	treeEdges := make(map[string]bool)
	for child, p := range parent {
		treeEdges[pairKey(p, child)] = true
	}

	fmt.Fprintf(out, "[%s]\n", graph.nodes[root].displayName)
	renderChildren(out, graph, root, children, "")

	crossLinks := make([]string, 0)
	for pair, links := range graph.undirected {
		a, b := splitPair(pair)
		if !inComponent[a] || !inComponent[b] {
			continue
		}
		if treeEdges[pair] {
			continue
		}
		crossLinks = append(crossLinks, formatCrossLink(graph, a, b, len(links)))
	}

	if len(crossLinks) > 0 {
		fmt.Fprintf(out, "  Cross-links:\n")
		for _, line := range crossLinks {
			fmt.Fprintf(out, "    %s\n", line)
		}
	}

	return treeEdges
}

func renderChildren(out *os.File, graph *topologyGraph, parent string, children map[string][]string, indent string) {
	kids := children[parent]
	for i, child := range kids {
		isLast := i == len(kids)-1
		branch := "├─ "
		nextIndent := indent + "│  "
		if isLast {
			branch = "└─ "
			nextIndent = indent + "   "
		}

		edges := edgeDetailsBetween(graph, parent, child)
		redundant := ""
		if len(edges) > 1 {
			redundant = fmt.Sprintf(" [%dx]", len(edges))
		}

		fmt.Fprintf(out, "%s%s[%s]%s\n", indent, branch, graph.nodes[child].displayName, redundant)

		for j, edge := range edges {
			if j >= 2 {
				if len(edges) > 2 {
					fmt.Fprintf(out, "%s└─ (+%d more)\n", nextIndent, len(edges)-2)
				}
				break
			}
			detailPrefix := "├─"
			if j == len(edges)-1 || (j == 1 && len(edges) > 2) {
				detailPrefix = "└─"
			}
			fmt.Fprintf(out, "%s%s %s → %s\n", nextIndent, detailPrefix, edge.localInterface, edge.remoteIface)
		}

		renderChildren(out, graph, child, children, nextIndent)
	}
}

func edgeDetailsBetween(graph *topologyGraph, parent, child string) []*linkDetail {
	if edges, ok := graph.outgoing[parent][child]; ok {
		return edges
	}
	reverse, ok := graph.outgoing[child][parent]
	if !ok {
		return nil
	}
	converted := make([]*linkDetail, 0, len(reverse))
	for _, r := range reverse {
		converted = append(converted, &linkDetail{
			from:           parent,
			to:             child,
			localInterface: r.remoteIface,
			remoteIface:    r.localInterface,
			protocols:      r.protocols,
		})
	}
	return converted
}

func formatCrossLink(graph *topologyGraph, left, right string, count int) string {
	leftLabel := left
	rightLabel := right
	if node := graph.nodes[left]; node != nil {
		leftLabel = node.displayName
	}
	if node := graph.nodes[right]; node != nil {
		rightLabel = node.displayName
	}
	extra := ""
	if count > 1 {
		extra = fmt.Sprintf(" [%dx]", count)
	}
	return fmt.Sprintf("%s <-> %s%s", leftLabel, rightLabel, extra)
}

func pairKey(a, b string) string {
	if a <= b {
		return a + "|" + b
	}
	return b + "|" + a
}

func splitPair(pair string) (string, string) {
	parts := strings.SplitN(pair, "|", 2)
	if len(parts) != 2 {
		return pair, ""
	}
	return parts[0], parts[1]
}

func shortName(fqdn string) string {
	if fqdn == "" {
		return ""
	}
	parts := strings.Split(fqdn, ".")
	return parts[0]
}

func printSummary(out *os.File, graph *topologyGraph, treeEdgeCount int) {
	totalLinks := 0
	for _, edges := range graph.undirected {
		totalLinks += len(edges)
	}

	fmt.Fprintf(out, "\n%s\n", strings.Repeat("─", 63))
	fmt.Fprintf(out, "Summary:\n")
	fmt.Fprintf(out, "  Devices: %d\n", len(graph.nodes))
	fmt.Fprintf(out, "  Total links: %d\n", totalLinks)
	fmt.Fprintf(out, "  Tree links: %d\n", treeEdgeCount)
	fmt.Fprintf(out, "  Cross-links: %d\n", len(graph.undirected)-treeEdgeCount)
}
