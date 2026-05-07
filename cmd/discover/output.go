package discover

import (
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sort"
	"strings"

	"gonum.org/v1/gonum/graph/simple"
	"jb.favre/mikrotik-fleet-autopilot/common/lldp"
)

const mfaNodeName = "mfa"

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

func outputTopology(out io.Writer, topo *topology, connectedTo string) error {
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

	graph, err := buildTopologyGraph(topo.results, topo.orderedHosts, connectedTo)
	if err != nil {
		return err
	}
	slog.Info("topology graph built", "vertices", len(graph.nodes))

	if _, werr := fmt.Fprintf(out, "LLDP Topology Graph\n"); werr != nil {
		return werr
	}
	if _, werr := fmt.Fprintf(out, "%s\n\n", strings.Repeat("═", 63)); werr != nil {
		return werr
	}

	plan := buildUpgradePlan(graph, topo.orderedHosts)

	if err := renderTopologyGraph(out, graph, topo.orderedHosts, plan); err != nil {
		return err
	}
	return nil
}

func buildTopologyGraph(results map[string]*lldp.ParseResult, orderedHosts []string, connectedTo string) (*topologyGraph, error) {
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

	if connectedTo == "" {
		return graph, nil
	}

	if _, ok := graph.nodes[connectedTo]; !ok {
		return nil, fmt.Errorf("connected-to target %q was not found in the topology; use a configured source host or a discovered device identity", connectedTo)
	}

	// mfa is a synthetic graph-only node representing the computer running the tool.
	// It must never be added to discovery inputs or any SSH connection path.
	mfaNode := graph.getOrCreateNode(mfaNodeName, false, -1)
	graph.addLink(mfaNode.name, connectedTo, &linkDetail{
		from: mfaNode.name,
		to:   connectedTo,
	})

	return graph, nil
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

type errorTrackingWriter struct {
	w   io.Writer
	err error
}

func (w *errorTrackingWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.w.Write(p)
	if err != nil {
		w.err = err
	}
	return n, err
}

func renderTopologyGraph(out io.Writer, graph *topologyGraph, orderedHosts []string, plan *upgradePlan) error {
	trackedOut := &errorTrackingWriter{w: out}
	components := connectedComponents(graph)
	roots := selectRoots(graph, components, orderedHosts, preferredRoot(graph))

	totalTreeEdges := 0
	for i, root := range roots {
		if i > 0 {
			if _, err := fmt.Fprintf(trackedOut, "\n"); err != nil {
				return err
			}
		}
		treeEdges := renderComponent(trackedOut, graph, components[i], root, orderedHosts)
		if trackedOut.err != nil {
			return trackedOut.err
		}
		totalTreeEdges += len(treeEdges)
	}

	if err := printUpgradePlan(trackedOut, plan); err != nil {
		return err
	}
	if trackedOut.err != nil {
		return trackedOut.err
	}

	printSummary(trackedOut, graph, totalTreeEdges, plan)
	if trackedOut.err != nil {
		return trackedOut.err
	}

	return nil
}

func preferredRoot(graph *topologyGraph) string {
	if _, ok := graph.nodes[mfaNodeName]; ok {
		return mfaNodeName
	}
	return ""
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

func selectRoots(graph *topologyGraph, components [][]string, orderedHosts []string, preferred string) []string {
	roots := make([]string, 0, len(components))
	for _, component := range components {
		if preferred != "" && componentContains(component, preferred) {
			roots = append(roots, preferred)
			continue
		}
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

func componentContains(component []string, target string) bool {
	return slices.Contains(component, target)
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

	parent, children := buildSpanningTree(graph, root, inComponent, orderedHosts)

	treeEdges := make(map[string]bool)
	for child, p := range parent {
		treeEdges[pairKey(p, child)] = true
	}

	_, _ = fmt.Fprintf(out, "[%s]\n", graph.nodes[root].name)
	renderChildren(out, graph, root, children, "  ")

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

		_, _ = fmt.Fprintf(out, "%s%s[%s]\n", indent, branch, childName)

		// Filter renderable edges (skip if both interfaces are empty)
		renderableEdges := make([]*linkDetail, 0)
		for _, edge := range edges {
			local := strings.TrimSpace(edge.localInterface)
			remote := strings.TrimSpace(edge.remoteIface)
			if local == "" && remote == "" {
				continue
			}
			renderableEdges = append(renderableEdges, edge)
		}

		if len(renderableEdges) == 0 {
			// No edge details (edge came from peer side only) — render descendants only.
			renderChildren(out, graph, child, children, indent+vertBar+"   ")
			continue
		}

		// Determine detail connector: vertical bar only if this node has child devices to follow
		hasChildren := len(children[child]) > 0
		detailConnector := " "
		if hasChildren {
			detailConnector = "│"
		}

		// Print via lines with detail connector
		for _, edge := range renderableEdges {
			local := strings.TrimSpace(edge.localInterface)
			remote := strings.TrimSpace(edge.remoteIface)
			if local == "" {
				local = "?"
			}
			if remote == "" {
				remote = "?"
			}
			_, _ = fmt.Fprintf(out, "%s%s   %s  via %s ↔ %s\n", indent, vertBar, detailConnector, local, remote)
		}
		_, _ = fmt.Fprintf(out, "%s%s   %s\n", indent, vertBar, detailConnector)

		nextIndent := indent + vertBar + "   "
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

// printUpgradePlan renders the upgrade wave schedule computed by buildUpgradePlan.
// It is the only upgrade-related function in output.go; all planning logic lives
// in planner.go.
func printUpgradePlan(out io.Writer, plan *upgradePlan) error {
	if plan == nil {
		return nil
	}

	if _, err := fmt.Fprintf(out, "\nUpgrade Plan\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "%s\n", strings.Repeat("═", 63)); err != nil {
		return err
	}

	if len(plan.waves) == 0 {
		if _, err := fmt.Fprintf(out, "No upgradeable devices found.\n"); err != nil {
			return err
		}
	} else {
		total := len(plan.waves)
		for _, wave := range plan.waves {
			label := "parallel"
			if wave.index == total {
				label = "final"
			}
			if _, err := fmt.Fprintf(out, "Wave %d (%s): %s\n", wave.index, label, strings.Join(wave.devices, ", ")); err != nil {
				return err
			}
		}
	}

	if len(plan.excluded) > 0 {
		if _, err := fmt.Fprintf(out, "\nExcluded (not upgradeable): %s\n", strings.Join(plan.excluded, ", ")); err != nil {
			return err
		}
	}

	return nil
}

func printSummary(out io.Writer, graph *topologyGraph, treeEdgeCount int, plan *upgradePlan) {
	totalLinks := 0
	for _, edges := range graph.undirected {
		totalLinks += len(edges)
	}

	_, _ = fmt.Fprintf(out, "%s\n", strings.Repeat("─", 63))
	_, _ = fmt.Fprintf(out, "Summary:\n")
	_, _ = fmt.Fprintf(out, "  Total devices        : %d\n", len(graph.nodes))
	_, _ = fmt.Fprintf(out, "  Rendered forest edges: %d\n", treeEdgeCount)
	_, _ = fmt.Fprintf(out, "  Stored LLDP records  : %d\n", totalLinks)

	if plan != nil {
		upgradeableCount := 0
		maxParallelism := 0
		for _, wave := range plan.waves {
			upgradeableCount += len(wave.devices)
			if len(wave.devices) > maxParallelism {
				maxParallelism = len(wave.devices)
			}
		}
		_, _ = fmt.Fprintf(out, "  Upgradeable devices  : %d\n", upgradeableCount)
		_, _ = fmt.Fprintf(out, "  Upgrade waves        : %d\n", len(plan.waves))
		_, _ = fmt.Fprintf(out, "  Max wave parallelism : %d\n", maxParallelism)
	}
}
