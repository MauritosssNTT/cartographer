package kb

import (
	"math"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// Bounds of the cluster view. A response holds at most MaxClusterElements
// clusters and nodes together and MaxClusterEdges edges, whatever the KB size.
const (
	// ClusterThreshold is the number of visible concepts in scope above which
	// the Atlas shows communities instead of concepts: where the flat view
	// starts truncating.
	ClusterThreshold = DefaultGraphNodeLimit
	// MaxClusters is how many communities become clusters; the rest share one
	// synthetic tail cluster.
	MaxClusters         = 150
	MaxClusterElements  = 400
	MaxClusterEdges     = 1500
	MinUnfoldMembers    = 50
	TailClusterID       = "c:~tail"
	clusterIDPrefix     = "c:"
	ClusterModeNodes    = "nodes"
	ClusterModeClusters = "clusters"
)

// GraphCluster is one folded community of the caller's visible graph. Every
// number is computed over visible concepts only (D226, D244).
type GraphCluster struct {
	// ID is "c:" plus the id of the member with the highest PageRank in the
	// whole community, scope-independent. A "c:" id is never a concept id.
	ID    string `json:"id"`
	Label string `json:"label"`
	// Collection is the dominant top-level collection among the in-scope members.
	Collection string `json:"collection,omitempty"`
	// Size counts the in-scope members.
	Size int `json:"size"`
	// Slot is the colour slot of the community, as in the flat view.
	Slot int `json:"slot"`
	// InternalEdges counts the links between two members, not drawn.
	InternalEdges int `json:"internal_edges"`
	// Communities is set on the tail cluster: how many communities it holds.
	Communities int `json:"communities,omitempty"`
}

// ClusterEdge is one undirected edge between two elements, each a concept id
// or a cluster id; Weight is the number of underlying links.
type ClusterEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Weight int    `json:"weight"`
}

// ClusterUnfold reports one unfolded cluster: how many of its Total members
// are drawn.
type ClusterUnfold struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Shown int    `json:"shown"`
	Total int    `json:"total"`
}

// ClusterGraph is the cluster projection of the link graph. In Mode "nodes"
// (few enough visible concepts in scope) Nodes and Edges are the flat snapshot
// and Clusters is empty.
type ClusterGraph struct {
	Mode           string              `json:"mode"`
	Threshold      int                 `json:"threshold"`
	Clusters       []GraphCluster      `json:"clusters"`
	Nodes          []GraphNode         `json:"nodes"`
	Edges          []ClusterEdge       `json:"edges"`
	Unfolded       []ClusterUnfold     `json:"unfolded"`
	DroppedUnfolds []string            `json:"dropped_unfolds"`
	Communities    []SnapshotCommunity `json:"communities"`
	TotalNodes     int                 `json:"total_nodes"`
	TotalEdges     int                 `json:"total_edges"`
	Truncated      bool                `json:"truncated,omitempty"`
}

// GraphClusterOptions selects a cluster view.
type GraphClusterOptions struct {
	// Scope restricts the members to one top-level collection, after the
	// communities are computed on the whole visible graph (D244).
	Scope string
	// Unfold lists the cluster ids to show as their members.
	Unfold []string
	// Focus is a concept id whose cluster is unfolded first and which is
	// guaranteed to be drawn. An unknown, invisible or out-of-scope id is
	// ignored, so the answer discloses nothing.
	Focus string
	// Include is the visibility predicate, as in GraphSnapshotOptions.
	Include func(id string) bool
}

// clusterGroup is a cluster before it is folded or unfolded.
type clusterGroup struct {
	GraphCluster
	members []int // in-scope member indexes, by PageRank
}

// GraphClusters projects the visible link graph onto its communities.
//
// Trap: visibility is applied before any grouping, by the same builder the
// flat snapshot uses; a size, label or edge weight is never computed from a
// concept the caller cannot see (D226, D244). Unfold and the unfold of Focus
// are naming an element the response may not hold: an id that does not exist
// lands in DroppedUnfolds, which tells the client nothing about hidden
// concepts since cluster ids derive from visible members only.
func (kb *KB) GraphClusters(opts GraphClusterOptions) (ClusterGraph, error) {
	vg, err := kb.buildVisibleGraph(opts.Include)
	if err != nil {
		return ClusterGraph{}, err
	}
	out := ClusterGraph{Threshold: ClusterThreshold, Clusters: []GraphCluster{}, Nodes: []GraphNode{}, Edges: []ClusterEdge{},
		Unfolded: []ClusterUnfold{}, DroppedUnfolds: []string{}}

	inScope := func(i int) bool { return opts.Scope == "" || vg.metas[vg.ids[i]].collection == opts.Scope }
	scopedCount := 0
	for i := range vg.ids {
		if inScope(i) {
			scopedCount++
		}
	}
	if scopedCount <= ClusterThreshold {
		snap := vg.snapshot(opts.Scope, DefaultGraphNodeLimit)
		out.Mode = ClusterModeNodes
		out.Nodes, out.Communities = snap.Nodes, snap.Communities
		out.TotalNodes, out.TotalEdges, out.Truncated = snap.TotalNodes, snap.TotalEdges, snap.Truncated
		for _, e := range snap.Edges {
			out.Edges = append(out.Edges, ClusterEdge{Source: string(e.Source), Target: string(e.Target), Weight: 1})
		}
		return out, nil
	}
	out.Mode = ClusterModeClusters
	out.TotalNodes = scopedCount
	out.Communities = vg.communityList()

	groups := vg.clusterGroups(inScope)
	byID := make(map[string]*clusterGroup, len(groups))
	for _, g := range groups {
		byID[g.ID] = g
	}

	clusterOf := make([]int, len(vg.ids)) // group index, or -1 when out of scope
	for i := range clusterOf {
		clusterOf[i] = -1
	}
	for gi, g := range groups {
		for _, u := range g.members {
			clusterOf[u] = gi
		}
	}
	// Unfold order: Focus's cluster first, then the request's order.
	var order []string
	focus, focusID := -1, ""
	if i, ok := vg.index[okf.ConceptID(opts.Focus)]; ok && opts.Focus != "" && inScope(i) {
		focus, focusID = i, groups[clusterOf[i]].ID
		order = append(order, focusID)
	}
	order = append(order, opts.Unfold...)

	unfolded := map[string]*clusterGroup{}
	shown := map[int]struct{}{} // node index -> drawn
	elements := len(groups)
	for _, id := range order {
		g, ok := byID[id]
		if !ok {
			if !contains(out.DroppedUnfolds, id) {
				out.DroppedUnfolds = append(out.DroppedUnfolds, id)
			}
			continue
		}
		if _, dup := unfolded[id]; dup {
			continue
		}
		// The fold gives its own element back; it needs room for at least
		// MinUnfoldMembers members or the cluster stays folded and is reported
		// as dropped, so the budget is never exceeded.
		room := MaxClusterElements - elements + 1
		if room < min(len(g.members), MinUnfoldMembers) {
			out.DroppedUnfolds = append(out.DroppedUnfolds, id)
			continue
		}
		pick := g.members
		if len(pick) > room {
			pick = pick[:room]
		}
		if id == focusID {
			pick = withFocus(pick, focus)
		}
		for _, u := range pick {
			shown[u] = struct{}{}
		}
		elements += len(pick) - 1
		unfolded[id] = g
		out.Unfolded = append(out.Unfolded, ClusterUnfold{ID: g.ID, Label: g.Label, Shown: len(pick), Total: len(g.members)})
		if len(pick) < len(g.members) {
			out.Truncated = true
		}
	}

	for _, g := range groups {
		if _, open := unfolded[g.ID]; !open {
			out.Clusters = append(out.Clusters, g.GraphCluster)
		}
	}
	shownIdx := make([]int, 0, len(shown))
	for u := range shown {
		shownIdx = append(shownIdx, u)
	}
	sort.Ints(shownIdx)
	for _, u := range shownIdx {
		id := vg.ids[u]
		m := vg.metas[id]
		out.Nodes = append(out.Nodes, GraphNode{ID: id, Collection: m.collection, Title: m.title, Type: m.typ, Status: m.status,
			Expanded: m.expanded, SelfLink: vg.selfLink[id], InDegree: vg.inDegree[id], OutDegree: vg.outDegree[id],
			PageRank: math.Round(vg.pagerank[u]*1e6) / 1e6, Community: vg.communityOf[u]})
	}

	// element names the drawn thing a node stands for: itself, its folded
	// cluster, or nothing (a member of an unfolded cluster that did not fit).
	element := func(u int) (string, bool) {
		gi := clusterOf[u]
		if gi < 0 {
			return "", false
		}
		if _, open := unfolded[groups[gi].ID]; open {
			if _, ok := shown[u]; ok {
				return string(vg.ids[u]), true
			}
			return "", false
		}
		return groups[gi].ID, true
	}
	weights := map[[2]string]int{}
	internal := map[string]int{}
	for key := range vg.edgeSet {
		u, v := vg.index[key.source], vg.index[key.target]
		if clusterOf[u] < 0 || clusterOf[v] < 0 {
			continue
		}
		out.TotalEdges++
		a, okA := element(u)
		b, okB := element(v)
		if !okA || !okB {
			continue
		}
		if a == b {
			internal[a]++
			continue
		}
		if a > b {
			a, b = b, a
		}
		pair := [2]string{a, b}
		if !strings.HasPrefix(a, clusterIDPrefix) && !strings.HasPrefix(b, clusterIDPrefix) {
			weights[pair] = 1 // a link and its reverse are one drawn edge
			continue
		}
		weights[pair]++
	}
	for i := range out.Clusters {
		out.Clusters[i].InternalEdges = internal[out.Clusters[i].ID]
	}
	for pair, w := range weights {
		out.Edges = append(out.Edges, ClusterEdge{Source: pair[0], Target: pair[1], Weight: w})
	}
	sort.Slice(out.Edges, func(i, j int) bool {
		a, b := out.Edges[i], out.Edges[j]
		if a.Weight != b.Weight {
			return a.Weight > b.Weight
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.Target < b.Target
	})
	if len(out.Edges) > MaxClusterEdges {
		out.Edges = out.Edges[:MaxClusterEdges]
		out.Truncated = true
	}
	return out, nil
}

// communityList is SnapshotCommunity for every community of the visible graph.
func (vg *visibleGraph) communityList() []SnapshotCommunity {
	list := make([]SnapshotCommunity, len(vg.communities))
	for i, c := range vg.communities {
		anchor := vg.ids[c.Anchor]
		list[i] = SnapshotCommunity{Rank: c.Rank, Size: c.Size, Anchor: anchor, AnchorTitle: vg.metas[anchor].title, Slot: c.Slot}
	}
	return list
}

// clusterGroups builds the clusters of the in-scope members: the largest
// MaxClusters communities and one tail for the rest. A community with no
// in-scope member yields none.
func (vg *visibleGraph) clusterGroups(inScope func(i int) bool) []*clusterGroup {
	type community struct {
		rank    int
		members []int
	}
	var cs []community
	for r, all := range vg.members {
		var in []int
		for _, u := range all {
			if inScope(u) {
				in = append(in, u)
			}
		}
		if len(in) > 0 {
			cs = append(cs, community{r, in})
		}
	}
	sort.SliceStable(cs, func(i, j int) bool { return len(cs[i].members) > len(cs[j].members) })
	named := func(id, label string, slot int, members []int) *clusterGroup {
		counts := map[string]int{}
		for _, u := range members {
			counts[vg.metas[vg.ids[u]].collection]++
		}
		dominant, best := "", 0
		for c, n := range counts {
			if n > best || n == best && c < dominant {
				dominant, best = c, n
			}
		}
		return &clusterGroup{GraphCluster: GraphCluster{ID: id, Label: label, Collection: dominant, Size: len(members), Slot: slot}, members: members}
	}
	var groups []*clusterGroup
	var tail []int
	tailCommunities := 0
	for k, c := range cs {
		if k >= MaxClusters {
			tail = append(tail, c.members...)
			tailCommunities++
			continue
		}
		// Named by the top member of the whole community, so the id does not
		// depend on the scope.
		top := vg.ids[vg.members[c.rank][0]]
		groups = append(groups, named(clusterIDPrefix+string(top), vg.memberLabel(top), vg.communities[c.rank].Slot, c.members))
	}
	if len(tail) > 0 {
		sort.Slice(tail, func(a, b int) bool {
			if vg.pagerank[tail[a]] != vg.pagerank[tail[b]] {
				return vg.pagerank[tail[a]] > vg.pagerank[tail[b]]
			}
			return tail[a] < tail[b]
		})
		g := named(TailClusterID, "Other", 0, tail)
		g.Communities = tailCommunities
		groups = append(groups, g)
	}
	return groups
}

// memberLabel is the title of a visible concept, or the last segment of its id.
func (vg *visibleGraph) memberLabel(id okf.ConceptID) string {
	if t := vg.metas[id].title; t != "" {
		return t
	}
	s := string(id)
	return s[strings.LastIndex(s, "/")+1:]
}

// withFocus makes sure the focus node is among the picked members, replacing
// the last one when the pick was cut short of it.
func withFocus(pick []int, focus int) []int {
	for _, u := range pick {
		if u == focus {
			return pick
		}
	}
	out := append([]int(nil), pick...)
	out[len(out)-1] = focus
	return out
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
