package memory

// GraphNode is one entry of the link graph, with how many entries link to it and how many it links to.
type GraphNode struct {
	Path  string `json:"path"`
	Title string `json:"title"`
	In    int    `json:"in"`
	Out   int    `json:"out"`
	// Type is the kind of memory the entry is, "" when it says none.
	Type string `json:"type,omitempty"`
}

// GraphEdge is a link from one entry to another that exists.
type GraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Kind is the association the link states (replaces, related...), "" for a plain link.
	Kind string `json:"kind,omitempty"`
}

// Graph is the memory as the entries and the links between them. Links to entries that do not exist
// yet are counted in Missing, not drawn.
type Graph struct {
	Nodes   []GraphNode `json:"nodes"`
	Edges   []GraphEdge `json:"edges"`
	Missing int         `json:"missing"`
}

// Graph reads every entry once and resolves its links. An entry linking to itself or twice to the
// same entry is one edge at most, and a self link is none.
func (v *Store) Graph() Graph {
	all := v.List()
	g := Graph{Nodes: make([]GraphNode, len(all)), Edges: []GraphEdge{}}
	at := map[string]int{}
	for i, n := range all {
		g.Nodes[i] = GraphNode{Path: n.Path, Title: n.Title}
		at[n.Path] = i
	}
	for i, n := range all {
		body, _, err := v.Read(n.Path)
		if err != nil {
			continue
		}
		meta, _, _ := ParseMeta(body, n.Modified)
		g.Nodes[i].Type = meta.Type
		kinds := relationsByPath(all, meta.Relations)
		seen := map[string]bool{}
		for _, l := range ParseLinks(body) {
			to, ok := Resolve(all, l.Target)
			switch {
			case !ok:
				g.Missing++
			case to != n.Path && !seen[to]:
				seen[to] = true
				g.Edges = append(g.Edges, GraphEdge{From: n.Path, To: to, Kind: kinds[to]})
				g.Nodes[i].Out++
				g.Nodes[at[to]].In++
			default:
				// a link to itself, or a second link to the same entry, adds no edge
			}
		}
	}
	return g
}

// relationsByPath is the association that leads to each entry, by the entry's path. The first one
// stated wins when an entry names another twice.
func relationsByPath(all []Entry, rels []Relation) map[string]string {
	out := map[string]string{}
	for _, r := range rels {
		if to, ok := Resolve(all, r.Target); ok && out[to] == "" {
			out[to] = r.Kind
		}
	}
	return out
}
