package rram

import "math"

// Postprocessor 封装 ADC 之后的数字后处理（反量化、零点修正等). 当前实现对应
// sa_dequant_simulator.py 中的 O_m/O_e/scale/zero_point 流程。
type Postprocessor struct {
	accumulatorBitWidth int
}

// ResultSummary 汇总一次 CIM 任务的后处理结果。
type ResultSummary struct {
	RawOM        int64
	O            float64
	Final        float64
	Reference    float64
	HasReference bool
	ASum         float64
	Scale        float64
	ZeroPt       int
	Valid        bool
}

// NewPostprocessor 返回一个默认配置的后处理模块。
func NewPostprocessor(accumulatorBitWidth int) *Postprocessor {
	if accumulatorBitWidth <= 0 {
		accumulatorBitWidth = 24
	}
	return &Postprocessor{
		accumulatorBitWidth: accumulatorBitWidth,
	}
}

func (p *Postprocessor) AccumulatorBitWidth() int {
	return p.accumulatorBitWidth
}

// FinalizeResult 按照 Python 模型计算：
// O_m = I_Sum - P_Sum * 8
// 实际指数 actualExp = (maxExponent - 10) - 15
// O = O_m * 2^(actualExp)
// final = O * scale - A_Sum * zero_point * scale
func (p *Postprocessor) FinalizeResult(iSum int64, pSum int64, maxExponent int, spec *TaskSpec, aSum float64) ResultSummary {
	result := ResultSummary{
		Scale:  1.0,
		ZeroPt: 0,
		ASum:   aSum,
		Valid:  true,
	}

	if spec != nil {
		if spec.Scale != 0 {
			result.Scale = spec.Scale
		}
		result.ZeroPt = spec.ZeroPoint
	}

	oM := iSum - pSum*8
	oE := maxExponent - 10
	actualExp := oE - 15

	result.RawOM = oM

	factor := math.Pow(2.0, float64(actualExp))
	o := float64(oM) * factor
	result.O = o

	final := o*result.Scale - result.ASum*float64(result.ZeroPt)*result.Scale
	result.Final = final

	if spec != nil && spec.HasExpected {
		result.Reference = spec.Expected
		result.HasReference = true
	}
	return result
}
