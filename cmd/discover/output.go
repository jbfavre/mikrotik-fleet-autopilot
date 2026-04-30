package discover

import (
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"

	"gonum.org/v1/gonum/graph/simple"
	"jb.favre/mikrotik-fleet-autopilot/common/lldp"
)

type topologyNode struct {
	name        string
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
	outgoing      map[string]map[string][]*linkDetail
	undirected    map[string][]*linkDetail
	nextFirstSeen int
}

func outputTopology(out io.Writer, topo *topology) error {
	if len(topo.errors) > 0 {
		if _, werr := fmt.Fprintf(out, "Discovery Errors:\n"); werr != nil {
			return werr
		}

		printed := make(map[string]struct{}, len(topo.errors))
		for _, host := range topo.orderedHosts {
			err, ok := topo.errors[host]
			if !ok {
				continue
			}
			if _, werr := fmt.Fprintf(out, "  %s: %v\n", host, err); werr != nil {
				return werr
			}
			printed[host] = struct{}{}
		}

		var remainingHosts []string
		for host := range topo.errors {
			if _, ok := printed[host]; ok {
				continue
			}
			remainingHosts = append(remainingHosts, host)
		}
		sort.Strings(remainingHosts)
		for _, host := range remainingHosts {
			if _, werr := fmt.Fprintf(out, "  %s: %v\n", host, topo.errors[host]); werr != nil {
				return werr
			}
		}
		if _, werr := fmt.Fprintf(out, "\n"); werr != nil {
			return werr
		}
	}

	if len(topo.results) == 0 {
		if _, werr := fmt.Fprintf(out, "No neighbors discovered.\n"); werr != nil {
			return werr
		}
		return nil
	}

	graph := buildTopologyGraph(topo.results, topo.orderedHosts)
	slog.Info("topology graph built", "vertices", len(graph.nodes))

	if _, werr := fmt.Fprintf(out, "LLDP Topology Graph\n"); werr != nil {
		return werr
	}
	if _, werr := fmt.Fprintf(out, "%s\n\n", strings.Repeat("═", 63)); werr != nil {
		return werr
	}

	renderTopologyGraph(out, graph, topo.orderedHosts)
	return nil
}

func buildTopologyGraph(results map[string]*lldp.ParseResult, orderedHosts []string) *topologyGraph {
	graph := &topologyGraph{
		g:          simple.NewUndirectedGraph(),
		nodes:      make(map[string]*topologyNode),
		idToName:   make(map[int64]string),
		outgoing:   make(map[string]map[string][]*linkDetail),
		undirected: make(map[string][]*linkDetail),
	}

	hostOrder := make(map[string]int, len(orderedHosts))
	for idx, host := range orderedHosts {
		hostOrder[host] = idx
		graph.getOrCreateNode(host, true, idx)
	}

	for _, sourceHost := range orderedHosts {
		result := results[sourceHost]
		if result == nil {
			continue
		}
		sourceNode := graph.getOrCreateNode(sourceHost, true, hostOrder[sourceHost])
		for neighborIdx, neighbor := range result.Neighbors {
			identity := strings.TrimSpace(neighbor.Identity)
			localInterface := strings.TrimSpace(neighbor.LocalInterface)
			// Avoid collapsing distinct neighbors with missing identity into a
			// single shared "unknown" node by generating a scoped placeholder.
			if identity == "" {
				identity = fmt.Sprintf("unknown (%s:%s#%d)", sourceHost, localInterface, neighborIdx)
			}
			destination := graph.getOrCreateNode(identity, false, -1)
			graph.addLink(sourceNode.name, destination.name, &linkDetail{
				from:           sourceNode.name,
				to:             destination.name,
				localInterface: neighbor.LocalInterface,
				remoteIface:    neighbor.RemoteInterface,
				protocols:      neighbor.DiscoveredBy,
			})
		}
	}

	return graph
}

func (g *topologyGraph) getOrCreateNode(name string, isSource bool, sourceOrder int) *topologyNode {
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
	node = &topologyNode{
		name:        name,
		isSource:    isSource,
		sourceOrder: sourceOrder,
		firstSeen:   g.nextFirstSeen,
		graphID:     n.ID(),
	}
	g.nextFirstSeen++
	g.nodes[name] = node
	g.idToName[n.ID()] = name
	if _, ok := g.outgoing[name]; !ok {
		g.outgoing[name] = make(map[string][]*linkDetail)
	}
	return node
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

func renderTopologyGraph(out io.Writer, graph *topologyGraph, orderedHosts []string) {
	components := connectedComponents(graph)
	roots := selectRoots(graph, components, orderedHosts)

	totalTreeEdges := 0
	for i, root := range roots {
		if i > 0 {
			_, _ = fmt.Fprintf(out, "\n")
		}
		treeEdges := renderComponent(out, graph, components[i], root, orderedHosts)
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

func renderComponent(out io.Writer, graph *topologyGraph, component []string, root string, orderedHosts []string) map[string]bool {
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
			return betterNode(graph, neighbors[i], neighbors[j], orderedHosts)
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
			return betterNode(graph, children[p][i], children[p][j], orderedHosts)
		})
	}

	treeEdges := make(map[string]bool)
	for child, p := range parent {
		treeEdges[pairKey(p, child)] = true
	}

	_, _ = fmt.Fprintf(out, "[%s]\n", graph.nodes[root].name)
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
		sort.Strings(crossLinks)
		_, _ = fmt.Fprintf(out, "  Cross-links:\n")
		for _, line := range crossLinks {
			_, _ = fmt.Fprintf(out, "    %s\n", line)
		}
	}

	return treeEdges
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func renderChildren(out io.Writer, graph *topologyGraph, parent string, children map[string][]string, indent string) {
	kids := children[parent]
	for i, child := range kids {
		isLast := i == len(kids)-1
		branch := "├─ "
		vertBar := "│"
		if isLast {
			branch = "└─ "
			vertBar = " "
		}

		childName := graph.nodes[child].name
		edges := edgeDetailsBetween(graph, parent, child)

		if len(edges) == 0 {
			// No edge details (edge came from peer side only) — print node label only
			_, _ = fmt.Fprintf(out, "%s%s[%s]\n", indent, branch, childName)
			renderChildren(out, graph, child, children, indent+vertBar+"   ")
			continue
		}

		maxLocalLen := 0
		for _, e := range edges {
			if len(e.localInterface) > maxLocalLen {
				maxLocalLen = len(e.localInterface)
			}
		}

		// First edge: branch + localIface → [child] ← remoteIface
		_, _ = fmt.Fprintf(out, "%s%s%s → [%s] ← %s\n",
			indent, branch,
			padRight(edges[0].localInterface, maxLocalLen),
			childName,
			edges[0].remoteIface,
		)

		// Additional parallel edges: aligned continuation lines
		// "←" must be at the same column as on the first edge line.
		// First-line "←" col  = len(indent) + 3 + maxLocalLen + 4 + len(childName) + 2
		// Continuation prefix = len(indent) + 1(vertBar) + 2("  ") + maxLocalLen
		// Fill to align       = len(childName) + 6
		for _, edge := range edges[1:] {
			_, _ = fmt.Fprintf(out, "%s%s  %s%s← %s\n",
				indent, vertBar,
				padRight(edge.localInterface, maxLocalLen),
				strings.Repeat(" ", len(childName)+6),
				edge.remoteIface,
			)
		}

		// nextIndent positions grandchildren's branch char under "[" of [child]
		// "[" col = len(indent) + 3(branch) + maxLocalLen + 3(" → ") = len(indent) + maxLocalLen + 6
		// nextIndent = indent + vertBar(1) + spaces(maxLocalLen+5)  →  branch lands at col maxLocalLen+6 ✓
		nextIndent := indent + vertBar + strings.Repeat(" ", maxLocalLen+5)
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
		leftLabel = node.name
	}
	if node := graph.nodes[right]; node != nil {
		rightLabel = node.name
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

func printSummary(out io.Writer, graph *topologyGraph, treeEdgeCount int) {
	totalLinks := 0
	for _, edges := range graph.undirected {
		totalLinks += len(edges)
	}

	_, _ = fmt.Fprintf(out, "\n%s\n", strings.Repeat("─", 63))
	_, _ = fmt.Fprintf(out, "Summary:\n")
	_, _ = fmt.Fprintf(out, "  Devices: %d\n", len(graph.nodes))
	_, _ = fmt.Fprintf(out, "  Total link entries: %d\n", totalLinks)
	_, _ = fmt.Fprintf(out, "  Tree edges: %d\n", treeEdgeCount)
	_, _ = fmt.Fprintf(out, "  Cross-link pairs: %d\n", len(graph.undirected)-treeEdgeCount)
}
