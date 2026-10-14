//go:build !noquotas

package runtime

import (
	"errors"
	"testing"
)

func TestCallContextFastEnforcesLimitsAndRecycles(t *testing.T) {
	r := New(nil)
	defer r.Close(nil)
	th := r.MainThread()
	def := RuntimeContextDef{HardLimits: RuntimeResources{Cpu: 100, Memory: 1000}}

	for i := 0; i < 3; i++ { // the cached copies and pools are reused, with the budget per call
		if err := th.CallContextFast(def, func() error { th.RequireCPU(50); th.RequireMem(500); return nil }); err != nil {
			t.Fatalf("call %d within the limits: %v", i, err)
		}
		err := th.CallContextFast(def, func() error { th.RequireCPU(101); return nil })
		var term ContextTerminationError
		if !errors.As(err, &term) {
			t.Fatalf("call %d over the CPU limit: got %v", i, err)
		}
		err = th.CallContextFast(def, func() error { th.RequireMem(1001); return nil })
		if !errors.As(err, &term) {
			t.Fatalf("call %d over the memory limit: got %v", i, err)
		}
	}
	if th.parent != nil {
		t.Fatal("contexts are left pushed")
	}
	if n := len(th.cache.parents); n != 1 {
		t.Fatalf("want one cached parent copy, got %d", n)
	}
}
