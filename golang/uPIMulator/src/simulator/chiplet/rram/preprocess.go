package rram

import "math"

// Preprocessor captures the signal conditioning that occurs before activation
// values are presented to the DAC drivers. The detailed behaviour (alignment,
// slicing、归一化等) 会在后续 Phase 中实现，此处仅保留配置占位。
type Preprocessor struct {
	activationBitWidth int
	sliceBits          int
}

// NewPreprocessor constructs a pre-dac processing pipeline placeholder.
func NewPreprocessor(activationBitWidth int, sliceBits int) *Preprocessor {
	if activationBitWidth <= 0 {
		activationBitWidth = 12
	}
	if sliceBits <= 0 {
		sliceBits = 2
	}
	return &Preprocessor{
		activationBitWidth: activationBitWidth,
		sliceBits:          sliceBits,
	}
}

// ActivationBitWidth exposes the configured activation precision used for
// splitting mantissas into DAC slices.
func (p *Preprocessor) ActivationBitWidth() int {
	return p.activationBitWidth
}

// SliceBits denotes how many bits are consumed per CIM cycle.
func (p *Preprocessor) SliceBits() int {
	return p.sliceBits
}

// Prepare is a placeholder for the pre-DAC conditioning logic (对齐、补码、
// 片段裁剪等)。后续 Phase 将在这里实现实际处理，目前直接返回输入。
func (p *Preprocessor) Prepare(signs []int, exponents []int, mantissas []int) (aligned []int, maxExponent int, pSum int, aSum float64) {
	count := len(mantissas)
	if count == 0 || len(signs) != count || len(exponents) != count {
		return nil, 0, 0, 0
	}

	maxExponent = exponents[0]
	for _, e := range exponents {
		if e > maxExponent {
			maxExponent = e
		}
	}

	aligned = make([]int, count)
	pSum = 0
	aSum = 0

	for i := 0; i < count; i++ {
		sign := 1
		if signs[i] != 0 {
			sign = -1
		}
		fullMantissa := (1 << 10) | (mantissas[i] & 0x3FF)
		shift := maxExponent - exponents[i]
		value := fullMantissa
		if shift > 0 {
			if shift >= 16 {
				value = 0
			} else {
				value >>= shift
			}
		}
		value *= sign
		aligned[i] = value
		pSum += value
		aSum += float64(value) * math.Pow(2.0, float64(exponents[i]-maxExponent))
	}

	return aligned, maxExponent, pSum, aSum
}
