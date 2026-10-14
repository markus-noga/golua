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

// The CPU ticks are granted in advance (fuel), but a limit still stops the context at the tick that reaches it, and what was used is exact.
func TestCPUFuelIsExact(t *testing.T) {
	r := New(nil)
	defer r.Close(nil)
	th := r.MainThread()
	for _, limit := range []uint64{1, 2, 3, 100, 2047, 2048, 2049, 5000, 100000} {
		ticks := uint64(0)
		def := RuntimeContextDef{HardLimits: RuntimeResources{Cpu: limit}}
		err := th.CallContextFast(def, func() error {
			for {
				th.RequireCPU(1)
				ticks++
			}
		})
		var term ContextTerminationError
		if !errors.As(err, &term) {
			t.Fatalf("limit %d: got %v", limit, err)
		}
		if ticks != limit-1 { // the limit-th tick is the one that is refused
			t.Errorf("limit %d: ran %d ticks, want %d", limit, ticks, limit-1)
		}
	}

	// what was used, read inside and after, with ticks of different sizes and a context nested in one with a limit
	def := RuntimeContextDef{HardLimits: RuntimeResources{Cpu: 10000}}
	err := th.CallContextFast(def, func() error {
		th.RequireCPU(1)
		th.RequireCPU(7)
		th.RequireCPU(3000) // more than a grant
		if got := th.UsedResources().Cpu; got != 3008 {
			t.Errorf("used %d, want 3008", got)
		}
		inner := RuntimeContextDef{HardLimits: RuntimeResources{Cpu: 500}}
		if err := th.CallContextFast(inner, func() error {
			th.RequireCPU(200)
			return nil
		}); err != nil {
			t.Errorf("inner: %v", err)
		}
		if got := th.UsedResources().Cpu; got != 3208 { // the inner context's ticks are charged to the one around it
			t.Errorf("used %d after the inner context, want 3208", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// a charge of the whole limit at once is refused, one tick under it is not
	if err := th.CallContextFast(RuntimeContextDef{HardLimits: RuntimeResources{Cpu: 5000}}, func() error { th.RequireCPU(4999); return nil }); err != nil {
		t.Errorf("4999 of 5000: %v", err)
	}
	if err := th.CallContextFast(RuntimeContextDef{HardLimits: RuntimeResources{Cpu: 5000}}, func() error { th.RequireCPU(5000); return nil }); err == nil {
		t.Error("5000 of 5000 was not refused")
	}
}
