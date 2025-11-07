package digital

import "testing"

func tickUntilIdle(t *testing.T, chiplet *Chiplet, limit int) {
	t.Helper()
	for i := 0; i < limit; i++ {
		if !chiplet.Busy() && chiplet.PendingTasks == 0 {
			return
		}
		chiplet.Tick()
	}
	t.Fatalf("chiplet still busy after %d cycles (pending=%d)", limit, chiplet.PendingTasks)
}

func TestChipletExecutesSpuTask(t *testing.T) {
	chiplet := NewChiplet(0, 4, 128, 128, 4, 0, 0, DefaultParameters())

	desc := &TaskDescriptor{
		Kind:        TaskKindSpuOp,
		Description: "spu_op_unit_test",
		ExecUnit:    ExecUnitSpu,
		ScalarOps:   1024,
		VectorOps:   256,
		SpecialOps:  64,
		RequiresSpu: true,
	}
	if !chiplet.SubmitDescriptor(desc) {
		t.Fatalf("SubmitDescriptor failed")
	}

	tickUntilIdle(t, chiplet, 2048)

	if chiplet.ExecutedTasks != 1 {
		t.Fatalf("expected 1 executed task, got %d", chiplet.ExecutedTasks)
	}
	if chiplet.SpuScalarOps != int64(desc.ScalarOps) {
		t.Fatalf("expected scalar ops %d, got %d", desc.ScalarOps, chiplet.SpuScalarOps)
	}
	if chiplet.SpuVectorOps != int64(desc.VectorOps) {
		t.Fatalf("expected vector ops %d, got %d", desc.VectorOps, chiplet.SpuVectorOps)
	}
	if chiplet.SpuSpecialOps != int64(desc.SpecialOps) {
		t.Fatalf("expected special ops %d, got %d", desc.SpecialOps, chiplet.SpuSpecialOps)
	}
	if chiplet.SpuEnergyPJ <= 0 {
		t.Fatalf("expected SPU energy to be recorded, got %.6f", chiplet.SpuEnergyPJ)
	}
	if chiplet.DynamicEnergyPJ <= 0 {
		t.Fatalf("expected dynamic energy to accumulate")
	}
}

func TestChipletExecutesVpuTask(t *testing.T) {
	chiplet := NewChiplet(0, 4, 128, 128, 4, 0, 0, DefaultParameters())

	desc := &TaskDescriptor{
		Kind:        TaskKindVpuOp,
		Description: "vpu_op_unit_test",
		ExecUnit:    ExecUnitVpu,
		VectorOps:   2048,
		RequiresVpu: true,
	}
	if !chiplet.SubmitDescriptor(desc) {
		t.Fatalf("SubmitDescriptor failed")
	}

	tickUntilIdle(t, chiplet, 2048)

	if chiplet.ExecutedTasks != 1 {
		t.Fatalf("expected 1 executed task, got %d", chiplet.ExecutedTasks)
	}
	if chiplet.VpuVectorOps != int64(desc.VectorOps) {
		t.Fatalf("expected vpu ops %d, got %d", desc.VectorOps, chiplet.VpuVectorOps)
	}
	if chiplet.SpuScalarOps != 0 {
		t.Fatalf("expected zero SPU scalar ops, got %d", chiplet.SpuScalarOps)
	}
	if chiplet.VpuEnergyPJ <= 0 {
		t.Fatalf("expected VPU energy to be recorded, got %.6f", chiplet.VpuEnergyPJ)
	}
	if chiplet.SpuEnergyPJ != 0 || chiplet.ReduceEnergyPJ != 0 {
		t.Fatalf("expected SPU/reduce energy to remain zero, got spu=%.6f reduce=%.6f", chiplet.SpuEnergyPJ, chiplet.ReduceEnergyPJ)
	}
}

func TestChipletExecutesReduceTaskViaSpu(t *testing.T) {
	chiplet := NewChiplet(0, 4, 128, 128, 4, 0, 0, DefaultParameters())

	desc := &TaskDescriptor{
		Kind:        TaskKindReduction,
		Description: "reduce_op_unit_test",
		ExecUnit:    ExecUnitSpu,
		ScalarOps:   512,
		RequiresSpu: true,
	}
	if !chiplet.SubmitDescriptor(desc) {
		t.Fatalf("SubmitDescriptor failed")
	}

	tickUntilIdle(t, chiplet, 2048)

	if chiplet.ExecutedTasks != 1 {
		t.Fatalf("expected 1 executed task, got %d", chiplet.ExecutedTasks)
	}
	if chiplet.SpuScalarOps != int64(desc.ScalarOps) {
		t.Fatalf("expected scalar ops %d, got %d", desc.ScalarOps, chiplet.SpuScalarOps)
	}
	if chiplet.VpuVectorOps != 0 {
		t.Fatalf("expected zero VPU ops, got %d", chiplet.VpuVectorOps)
	}
	if chiplet.ReduceEnergyPJ <= 0 {
		t.Fatalf("expected reduce energy to be recorded, got %.6f", chiplet.ReduceEnergyPJ)
	}
	if chiplet.SpuEnergyPJ != 0 {
		t.Fatalf("expected SPU energy to remain zero for reduce task, got %.6f", chiplet.SpuEnergyPJ)
	}
}
