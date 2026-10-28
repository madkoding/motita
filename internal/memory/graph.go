package memory

// GraphNode is one entry of the link graph, with how many entries link to it and how many it links to.
type GraphNode struct {
	Path  string `json:"path"`
	Title string `json:"title"`
	In    int    `json:"in"`
	Out   int    `json:"out"`
}

// GraphEdge is a link from one entry to another that exists.
type GraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
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
		seen := map[string]bool{}
		for _, l := range ParseLinks(body) {
			to, ok := Resolve(all, l.Target)
			switch {
			case !ok:
				g.Missing++
			case to != n.Path && !seen[to]:
				seen[to] = true
				g.Edges = append(g.Edges, GraphEdge{From: n.Path, To: to})
				g.Nodes[i].Out++
				g.Nodes[at[to]].In++
			default:
				// a link to itself, or a second link to the same entry, adds no edge
			}
		}
	}
	return g
}
