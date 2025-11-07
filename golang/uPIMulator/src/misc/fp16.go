package misc

import "math"

func Float16ToFloat32(value uint16) float32 {
	sign := uint32(value>>15) & 0x1
	exponent := uint32(value>>10) & 0x1F
	mantissa := uint32(value & 0x3FF)

	var bits uint32
	if exponent == 0 {
		if mantissa == 0 {
			bits = sign << 31
		} else {
			for (mantissa & 0x400) == 0 {
				mantissa <<= 1
				exponent++
			}
			mantissa &= 0x3FF
			exponent = exponent + (127 - 15)
			bits = (sign << 31) | (exponent << 23) | (mantissa << 13)
		}
	} else if exponent == 0x1F {
		if mantissa == 0 {
			bits = (sign << 31) | 0x7F800000
		} else {
			bits = (sign << 31) | 0x7F800000 | (mantissa << 13)
		}
	} else {
		exponent = exponent + (127 - 15)
		bits = (sign << 31) | (exponent << 23) | (mantissa << 13)
	}

	return math.Float32frombits(bits)
}

func Float32ToFloat16(value float32) uint16 {
	bits := math.Float32bits(value)

	sign := uint16((bits >> 31) & 0x1)
	exponent := int((bits >> 23) & 0xFF)
	mantissa := uint32(bits & 0x7FFFFF)

	var half uint16
	if exponent == 0xFF {
		if mantissa == 0 {
			half = (sign << 15) | 0x7C00
		} else {
			half = (sign << 15) | 0x7C00 | uint16(mantissa>>13)
		}
	} else if exponent > 142 {
		half = (sign << 15) | 0x7C00
	} else if exponent < 113 {
		if exponent < 103 {
			half = sign << 15
		} else {
			mantissa |= 0x800000
			shift := uint(113 - exponent)
			halfMantissa := uint16(mantissa >> (shift + 13))
			half = (sign << 15) | halfMantissa
		}
	} else {
		halfExponent := uint16(exponent-112) << 10
		halfMantissa := uint16(mantissa >> 13)
		half = (sign << 15) | halfExponent | halfMantissa
	}

	return half
}
