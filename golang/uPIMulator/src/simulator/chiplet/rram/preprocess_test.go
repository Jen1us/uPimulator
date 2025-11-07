package rram

import "testing"

func TestPreprocessorPrepare(t *testing.T) {
	pre := NewPreprocessor(12, 2)
	signs := []int{0, 1}
	exps := []int{15, 14}
	mant := []int{0, 0}
	_, maxExp, pSum, aSum := pre.Prepare(signs, exps, mant)
	if maxExp != 15 {
		t.Fatalf("max exponent mismatch: want 15, got %d", maxExp)
	}
	if pSum != 512 {
		t.Fatalf("pSum mismatch: want 512, got %d", pSum)
	}
	if aSum != 768 {
		t.Fatalf("aSum mismatch: want 768, got %f", aSum)
	}
}

func TestPostprocessorFinalize(t *testing.T) {
	pre := NewPreprocessor(12, 2)
	signs := []int{0, 1}
	exps := []int{15, 14}
	mant := []int{0, 0}
	_, maxExp, pSum, aSum := pre.Prepare(signs, exps, mant)
	spec := &TaskSpec{
		Scale:       0.1,
		ZeroPoint:   0,
		PSum:        int64(pSum),
		ASum:        aSum,
		MaxExponent: maxExp,
		HasExpected: true,
		Expected:    0.4,
		ISum:        int64(pSum*8 + 4096),
	}
	post := NewPostprocessor(24)
	summary := post.FinalizeResult(spec.ISum, spec.PSum, spec.MaxExponent, spec, spec.ASum)
	if !summary.HasReference {
		t.Fatalf("expected reference flag true")
	}
	if summary.Reference != 0.4 {
		t.Fatalf("reference mismatch: want 0.4, got %f", summary.Reference)
	}
}
