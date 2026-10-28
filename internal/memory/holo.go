package memory

import (
	"math"
	"sort"
)

// The holographic part of recall. Every passage is kept as ONE short vector in which everything it
// says is superposed, and a memory is recalled by cueing with a fragment of it: the cue's vector is
// compared with every passage and the ones it resembles most come back. Nothing is looked up by
// name, so a partial or differently worded cue still finds the passage.
//
// It is a vector symbolic architecture of the multiply-add-permute kind:
//
//   - every word has a fixed random vector of +1 and -1 (its "index vector"), made from the word
//     itself, so the same word is the same vector in every run and nothing has to be stored;
//   - words that appear in the same passages learn from each other (random indexing): a word's
//     context vector is the sum of the index vectors of the words around it, so "salary" and
//     "payroll" end up close when the memory uses them together;
//   - a passage is the sum of its words' vectors, weighted by how rare each word is, plus the same
//     for its title and heading trail BOUND to a role vector with an element-wise product, which
//     keeps "what it is about" apart from "what it says" and can be undone by binding again;
//   - word order is kept by binding each word to the next one's vector shifted by one place.
//
// The cue goes through the same steps, and the passages are ranked by cosine similarity. No model
// and no network is needed; an embeddings endpoint, when the person has set one, adds to this and
// does not replace it.
const (
	dim         = 512
	maxUnique   = 96   // words of a passage that are kept: the rarest ones
	roleWeight  = 0.6  // how much the title and trail count against the text
	orderWeight = 0.35 // how much word order counts against words
	minCosine   = 0.12 // below this a passage does not resemble the cue
)

type vec []float32

// scale makes an index vector length one, so a word's own vector and its context vector count alike.
var scale = float32(1 / math.Sqrt(dim))

// roleContext binds the title and heading trail. Roles stay +1 and -1: binding with one changes no
// length.
var roleContext = bipolar("role:context")

// bipolar is the index vector of a word: dim values of +1 and -1 drawn from a generator seeded by
// the word.
func bipolar(word string) vec {
	state := uint64(14695981039346656037)
	for i := 0; i < len(word); i++ {
		state = (state ^ uint64(word[i])) * 1099511628211
	}
	v := make(vec, dim)
	for i := 0; i < dim; i += 64 {
		state += 0x9e3779b97f4a7c15
		z := state
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		z ^= z >> 31
		for j := 0; j < 64 && i+j < dim; j++ {
			v[i+j] = float32(int(z>>uint(j)&1)*2 - 1)
		}
	}
	return v
}

// wordVector is the index vector of a word, of length one.
func wordVector(w string) vec {
	v := bipolar(w)
	for i := range v {
		v[i] *= scale
	}
	return v
}

func (v vec) add(o vec, w float32) {
	for i := range v {
		v[i] += o[i] * w
	}
}

// bind is the element-wise product of two vectors; for +1/-1 vectors it is its own inverse.
func (v vec) bind(o vec) vec {
	out := make(vec, dim)
	for i := range v {
		out[i] = v[i] * o[i]
	}
	return out
}

// shifted is v moved one place along, which makes "A then B" differ from "B then A".
func (v vec) shifted() vec {
	out := make(vec, dim)
	copy(out[1:], v[:dim-1])
	out[0] = v[dim-1]
	return out
}

// unit scales v to length one and reports whether it had any length.
func (v vec) unit() bool {
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	if n == 0 {
		return false
	}
	inv := float32(1 / math.Sqrt(n))
	for i := range v {
		v[i] *= inv
	}
	return true
}

func dot(a, b vec) float64 {
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

// holo is the vector space of an index.
type holo struct {
	idf   map[string]float64
	index map[string]vec // index vectors of the words of the memory
	rep   map[string]vec // index vector plus context vector, for the words that have one
	vecs  []vec          // one per child
}

// weighted is a word with its weight in a passage.
type weighted struct {
	term string
	w    float64
}

// bag is the words of a text, the rarest maxUnique of them, weighted by (1 + log tf) * idf.
func (h *holo) bag(words []string) []weighted {
	tf := map[string]int{}
	for _, w := range words {
		tf[w]++
	}
	out := make([]weighted, 0, len(tf))
	for w, n := range tf {
		if idf, ok := h.idf[w]; ok {
			out = append(out, weighted{w, (1 + math.Log(float64(n))) * idf})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].w != out[j].w {
			return out[i].w > out[j].w
		}
		return out[i].term < out[j].term
	})
	if len(out) > maxUnique {
		out = out[:maxUnique]
	}
	return out
}

// encode is the vector of a text: its words, and the word pairs bound in order.
func (h *holo) encode(words []string) vec {
	v := make(vec, dim)
	for _, b := range h.bag(words) {
		v.add(h.rep[b.term], float32(b.w))
	}
	var mean float64
	for i := 1; i < len(words); i++ {
		a, ok1 := h.index[words[i-1]]
		b, ok2 := h.index[words[i]]
		if ok1 && ok2 {
			mean = (h.idf[words[i-1]] + h.idf[words[i]]) / 2
			v.add(a.bind(b.shifted()), float32(orderWeight*mean)*float32(dim)*scale)
		}
	}
	return v
}

// buildHolo learns the vector space of the children of an index and encodes each of them.
func buildHolo(words [][]string, ctxWords [][]string) *holo {
	h := &holo{idf: map[string]float64{}, index: map[string]vec{}, rep: map[string]vec{}}
	df := map[string]int{}
	for i, ws := range words {
		seen := map[string]bool{}
		for _, w := range append(append([]string{}, ws...), ctxWords[i]...) {
			if !seen[w] {
				seen[w] = true
				df[w]++
			}
		}
	}
	n := float64(len(words))
	for w, d := range df {
		h.idf[w] = math.Log(1 + n/float64(d))
		h.index[w] = wordVector(w)
	}
	h.learn(words, df)
	h.vecs = make([]vec, len(words))
	for i := range words {
		h.vecs[i] = h.passage(words[i], ctxWords[i])
	}
	return h
}

// eligible says whether a word is common enough to learn from and rare enough to say something.
func eligible(df, n int) bool { return df >= 2 && df*2 <= n }

// learn gives each eligible word its context vector: the sum, over the passages it is in, of the
// other words of the passage.
func (h *holo) learn(words [][]string, df map[string]int) {
	ctx := map[string]vec{}
	for _, ws := range words {
		bag := h.bag(ws)
		sum := make(vec, dim)
		for _, b := range bag {
			sum.add(h.index[b.term], float32(b.w))
		}
		for _, b := range bag {
			if !eligible(df[b.term], len(words)) {
				continue
			}
			c := ctx[b.term]
			if c == nil {
				c = make(vec, dim)
				ctx[b.term] = c
			}
			c.add(sum, 1)
			c.add(h.index[b.term], -float32(b.w))
		}
	}
	for w, iv := range h.index {
		r := make(vec, dim)
		r.add(iv, 1)
		if c, ok := ctx[w]; ok && c.unit() {
			r.add(c, 1)
		}
		h.rep[w] = r
	}
}

// passage is the vector of a child: its text, and its context line bound to the context role.
func (h *holo) passage(text, ctx []string) vec {
	v := h.encode(text)
	if c := h.encode(ctx); c.unit() {
		v.add(c.bind(roleContext), roleWeight)
	}
	v.unit()
	return v
}

// cue is the vector a query is compared with: its words, and the same words bound to the context
// role, so a query can match what a passage says and what it is about.
func (h *holo) cue(words []string) (vec, bool) {
	q := h.encode(words)
	if !q.unit() {
		return nil, false
	}
	probe := make(vec, dim)
	probe.add(q, 1)
	probe.add(q.bind(roleContext), roleWeight)
	probe.unit()
	return probe, true
}

// recall ranks the children allowed by keep against a cue, best first.
func (h *holo) recall(q vec, keep func(int) bool) []scored {
	var out []scored
	for i, v := range h.vecs {
		if !keep(i) {
			continue
		}
		if s := dot(v, q); s >= minCosine {
			out = append(out, scored{i, s})
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].score != out[b].score {
			return out[a].score > out[b].score
		}
		return out[a].at < out[b].at
	})
	return out
}

// scored is a child and how well it matched.
type scored struct {
	at    int
	score float64
}
