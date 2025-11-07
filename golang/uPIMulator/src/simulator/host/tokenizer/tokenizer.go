package tokenizer

import "strings"

// Tokenizer describes the minimal interface required by the chiplet runtime to
// convert textual input into token IDs and vice versa.
type Tokenizer interface {
	Encode(text string) []int
	Decode(tokens []int) string
}

// StaticTokenizer is a simple implementation backed by a lookup table. It is
// sufficient for Phase6 development and can later be replaced by bindings to an
// external tokenizer.
type StaticTokenizer struct {
	dictionary map[string]int
	reverse    map[int]string
}

// NewStaticTokenizer constructs a tokenizer using the provided dictionary. If
// dict is nil a small fallback vocabulary is created.
func NewStaticTokenizer(dict map[string]int) *StaticTokenizer {
	tokens := dict
	if tokens == nil {
		tokens = map[string]int{
			"<pad>":     0,
			"<bos>":     1,
			"<eos>":     2,
			"attention": 3,
			"moe":       4,
			"swiglu":    5,
		}
	}
	reverse := make(map[int]string, len(tokens))
	for k, v := range tokens {
		reverse[v] = k
	}
	return &StaticTokenizer{
		dictionary: tokens,
		reverse:    reverse,
	}
}

// Encode splits on whitespace and maps tokens to IDs (unknowns map to 0).
func (t *StaticTokenizer) Encode(text string) []int {
	parts := strings.Fields(text)
	ids := make([]int, 0, len(parts))
	for _, part := range parts {
		if id, ok := t.dictionary[part]; ok {
			ids = append(ids, id)
		} else {
			ids = append(ids, 0)
		}
	}
	return ids
}

// Decode maps IDs back into tokens; unknown IDs become "<unk>".
func (t *StaticTokenizer) Decode(tokens []int) string {
	parts := make([]string, 0, len(tokens))
	for _, id := range tokens {
		if token, ok := t.reverse[id]; ok {
			parts = append(parts, token)
		} else {
			parts = append(parts, "<unk>")
		}
	}
	return strings.Join(parts, " ")
}

// Ensure StaticTokenizer implements Tokenizer.
var _ Tokenizer = (*StaticTokenizer)(nil)
