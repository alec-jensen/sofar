package categorize

import (
	"bytes"
	"compress/gzip"
	"encoding/gob"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"sort"
	"strings"
)

// Dim is the hashed feature space.
const Dim = 1 << 16

// FeatureInput is what the statistical layers see of a transaction (S18a).
type FeatureInput struct {
	Merchant       string // normalized key
	Description    string // cleaned, lower case
	Payee          string
	Memo           string
	Processor      string
	Account        string
	AccountKind    string
	MCC            string
	Amount         int64 // absolute cents
	Inflow         bool
	Weekday        int
	DayOfMonth     int
	Recurring      bool  // matches a recurring item
	MerchantMedian int64 // median amount of this merchant's history, 0 if unknown
}

func hash(kind, tok string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(kind))
	h.Write([]byte{0})
	h.Write([]byte(tok))
	return h.Sum32() % Dim
}

func grams(s string, lo, hi int) []string {
	s = "^" + s + "$"
	r := []rune(s)
	var out []string
	for n := lo; n <= hi; n++ {
		for i := 0; i+n <= len(r); i++ {
			out = append(out, string(r[i:i+n]))
		}
	}
	return out
}

func log2bucket(c int64) int {
	if c <= 0 {
		return 0
	}
	return int(math.Floor(math.Log2(float64(c))))
}

// Features extracts the sparse hashed features: character n-grams 2 to 5 over
// the merchant, words over description, memo and payee, the merchant and
// processor, amount shape and its relation to the merchant's history, direction,
// account, recurrence, calendar, and P2P memo words.
func Features(in FeatureInput) []uint32 {
	seen := map[uint32]bool{}
	var out []uint32
	add := func(kind, tok string) {
		if tok == "" {
			return
		}
		f := hash(kind, tok)
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	add("merchant", in.Merchant)
	for _, g := range grams(in.Merchant, 2, 5) {
		add("mg", g)
	}
	for _, kind := range []struct{ name, text string }{{"desc", in.Description}, {"payee", in.Payee}, {"memo", in.Memo}} {
		for _, w := range strings.Fields(strings.ToLower(kind.text)) {
			if len(w) > 2 {
				add(kind.name, w)
			}
		}
	}
	add("proc", in.Processor)
	add("mcc", in.MCC)
	add("acct", in.Account)
	add("acctkind", in.AccountKind)
	if in.Inflow {
		add("dir", "in")
	} else {
		add("dir", "out")
	}
	add("amtbucket", fmt.Sprint(log2bucket(in.Amount)))
	add("amtexact", fmt.Sprint(in.Amount))
	if in.Amount%100 == 0 {
		add("round", "dollar")
	}
	if in.Amount%500 == 0 {
		add("round", "five")
	}
	if in.Recurring {
		add("recurring", "yes")
	}
	add("weekday", fmt.Sprint(in.Weekday))
	add("dom", fmt.Sprint(in.DayOfMonth/8))
	if in.MerchantMedian > 0 && in.Amount > 0 {
		r := float64(in.Amount) / float64(in.MerchantMedian)
		switch {
		case r < 0.5:
			add("rel", "much_less")
		case r < 0.9:
			add("rel", "less")
		case r <= 1.1:
			add("rel", "same")
		case r <= 2:
			add("rel", "more")
		default:
			add("rel", "much_more")
		}
	}
	// merchant crossed with account and with the amount bucket: lets one merchant
	// split by account (personal versus business) or by size
	add("m_acct", in.Merchant+"|"+in.Account)
	add("m_amt", in.Merchant+"|"+fmt.Sprint(log2bucket(in.Amount)))
	add("m_rec", fmt.Sprintf("%s|%v", in.Merchant, in.Recurring))
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Model is a multinomial logistic regression over hashed features, trained by
// online SGD.
type Model struct {
	Classes []string
	W       []float32 // len(Classes) * Dim
	Bias    []float32
	Version int
	Trained int // number of weighted examples seen
}

// Example is one labeled training example.
type Example struct {
	Feats  []uint32
	Class  string
	Weight float64
}

func (m *Model) index() map[string]int {
	idx := map[string]int{}
	for i, c := range m.Classes {
		idx[c] = i
	}
	return idx
}

// Prediction is a class distribution.
type Prediction struct {
	Class string
	Prob  float64
	Top   []ClassProb // best first, up to 3
}

// ClassProb pairs a class with its probability.
type ClassProb struct {
	Class string
	Prob  float64
}

func softmax(z []float64) []float64 {
	max := z[0]
	for _, v := range z {
		if v > max {
			max = v
		}
	}
	var sum float64
	out := make([]float64, len(z))
	for i, v := range z {
		out[i] = math.Exp(v - max)
		sum += out[i]
	}
	for i := range out {
		out[i] /= sum
	}
	return out
}

func (m *Model) logits(f []uint32) []float64 {
	z := make([]float64, len(m.Classes))
	for c := range m.Classes {
		s := float64(m.Bias[c])
		base := c * Dim
		for _, x := range f {
			s += float64(m.W[base+int(x)])
		}
		z[c] = s
	}
	return z
}

// Predict returns the most likely class and the top three. A nil or empty
// model returns a zero Prediction.
func (m *Model) Predict(f []uint32) Prediction {
	if m == nil || len(m.Classes) == 0 {
		return Prediction{}
	}
	p := softmax(m.logits(f))
	order := make([]int, len(p))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return p[order[a]] > p[order[b]] })
	pred := Prediction{Class: m.Classes[order[0]], Prob: p[order[0]]}
	for i := 0; i < len(order) && i < 3; i++ {
		pred.Top = append(pred.Top, ClassProb{m.Classes[order[i]], p[order[i]]})
	}
	return pred
}

// Fit trains a model from examples with deterministic shuffling. Class weights
// are inverse-frequency so rare categories are not drowned out. Training is a
// pure function of its inputs and seed.
func Fit(examples []Example, version int, epochs int, seed int64) *Model {
	set := map[string]bool{}
	for _, e := range examples {
		set[e.Class] = true
	}
	classes := make([]string, 0, len(set))
	for c := range set {
		classes = append(classes, c)
	}
	sort.Strings(classes)
	m := &Model{Classes: classes, W: make([]float32, len(classes)*Dim), Bias: make([]float32, len(classes)), Version: version}
	if len(examples) == 0 {
		return m
	}
	idx := m.index()
	freq := map[string]float64{}
	var total float64
	for _, e := range examples {
		freq[e.Class] += e.Weight
		total += e.Weight
	}
	rng := rand.New(rand.NewSource(seed + int64(version)))
	order := make([]int, len(examples))
	for i := range order {
		order[i] = i
	}
	const lr, l2 = 0.4, 1e-6
	for ep := 0; ep < epochs; ep++ {
		rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		for _, oi := range order {
			e := examples[oi]
			y := idx[e.Class]
			classWeight := total / (float64(len(classes)) * freq[e.Class])
			if classWeight > 5 {
				classWeight = 5
			}
			w := e.Weight * math.Sqrt(classWeight)
			p := softmax(m.logits(e.Feats))
			step := lr * w / float64(1+ep)
			for c := range classes {
				g := p[c]
				if c == y {
					g -= 1
				}
				g *= step
				m.Bias[c] -= float32(g)
				base := c * Dim
				for _, f := range e.Feats {
					m.W[base+int(f)] -= float32(g + l2*float64(m.W[base+int(f)]))
				}
			}
		}
	}
	m.Trained = len(examples)
	return m
}

// Encode serializes the model compactly.
func (m *Model) Encode() ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := gob.NewEncoder(zw).Encode(m); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeModel reads a model written by Encode.
func DecodeModel(b []byte) (*Model, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	var m Model
	if err = gob.NewDecoder(zr).Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Calibrator maps a raw top probability to an honest confidence by Platt
// scaling: p' = sigmoid(a * logit(p) + b).
type Calibrator struct{ A, B float64 }

// Identity leaves probabilities unchanged.
var Identity = Calibrator{A: 1, B: 0}

func logit(p float64) float64 {
	p = math.Min(math.Max(p, 1e-6), 1-1e-6)
	return math.Log(p / (1 - p))
}

// Apply returns the calibrated confidence.
func (c Calibrator) Apply(p float64) float64 {
	return 1 / (1 + math.Exp(-(c.A*logit(p) + c.B)))
}

// FitCalibrator fits Platt scaling from (raw probability, was it correct) pairs
// by gradient descent on log loss. With too little data it returns Identity.
func FitCalibrator(probs []float64, correct []bool) Calibrator {
	if len(probs) < 30 {
		return Identity
	}
	a, b := 1.0, 0.0
	for it := 0; it < 400; it++ {
		var ga, gb float64
		for i, p := range probs {
			x := logit(p)
			q := 1 / (1 + math.Exp(-(a*x + b)))
			y := 0.0
			if correct[i] {
				y = 1
			}
			ga += (q - y) * x
			gb += q - y
		}
		n := float64(len(probs))
		a -= 0.5 * ga / n
		b -= 0.5 * gb / n
	}
	return Calibrator{A: a, B: b}
}

// ---- case memory ----

// MemoryItem is a labeled example kept for nearest-neighbor explanations.
type MemoryItem struct {
	Feats    map[uint32]bool
	Class    string
	Amount   int64
	Merchant string
}

// Memory is case-based reasoning: the closest past labeled cases.
type Memory struct{ Items []MemoryItem }

// Add records a labeled case.
func (m *Memory) Add(f []uint32, class string, amount int64, merchant string) {
	set := make(map[uint32]bool, len(f))
	for _, x := range f {
		set[x] = true
	}
	m.Items = append(m.Items, MemoryItem{set, class, amount, merchant})
}

// Neighbor is a close past case.
type Neighbor struct {
	Item       MemoryItem
	Similarity float64
}

// Nearest returns the k most similar cases, best first.
func (m *Memory) Nearest(f []uint32, k int) []Neighbor {
	if len(m.Items) == 0 {
		return nil
	}
	q := make(map[uint32]bool, len(f))
	for _, x := range f {
		q[x] = true
	}
	var ns []Neighbor
	for _, it := range m.Items {
		inter := 0
		for x := range q {
			if it.Feats[x] {
				inter++
			}
		}
		union := len(q) + len(it.Feats) - inter
		if union == 0 {
			continue
		}
		ns = append(ns, Neighbor{it, float64(inter) / float64(union)})
	}
	sort.SliceStable(ns, func(i, j int) bool { return ns[i].Similarity > ns[j].Similarity })
	if len(ns) > k {
		ns = ns[:k]
	}
	return ns
}

// Vote summarizes neighbors: the winning class, its share of the similarity
// weight, and a sentence such as "similar: chevron, 6 times, all fuel".
func Vote(ns []Neighbor) (class string, share float64, why string) {
	if len(ns) == 0 {
		return "", 0, ""
	}
	weight := map[string]float64{}
	count := map[string]int{}
	var total float64
	for _, n := range ns {
		weight[n.Item.Class] += n.Similarity
		count[n.Item.Class]++
		total += n.Similarity
	}
	best := ""
	for c, w := range weight {
		if best == "" || w > weight[best] || (w == weight[best] && c < best) {
			best = c
		}
	}
	if total == 0 {
		return "", 0, ""
	}
	merchant := ns[0].Item.Merchant
	all := "all"
	if count[best] < len(ns) {
		all = fmt.Sprintf("%d of %d", count[best], len(ns))
	}
	return best, weight[best] / total, fmt.Sprintf("similar to %s, %s %s", merchant, all, best)
}
