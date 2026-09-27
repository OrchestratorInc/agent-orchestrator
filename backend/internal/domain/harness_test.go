package domain

import "testing"

func TestPrimeAgentHarnessIsKnown(t *testing.T) {
	if HarnessPrimeAgent != AgentHarness("prime-agent") {
		t.Fatalf("HarnessPrimeAgent = %q, want prime-agent", HarnessPrimeAgent)
	}
	if !HarnessPrimeAgent.IsKnown() {
		t.Fatal("HarnessPrimeAgent.IsKnown() = false, want true")
	}
	found := false
	for _, harness := range AllHarnesses {
		if harness == HarnessPrimeAgent {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("AllHarnesses does not contain HarnessPrimeAgent")
	}
}
func TestOMPHarnessIsKnown(t *testing.T) {
	if HarnessOMP != AgentHarness("omp") {
		t.Fatalf("HarnessOMP = %q, want omp", HarnessOMP)
	}
	if !HarnessOMP.IsKnown() {
		t.Fatal("HarnessOMP.IsKnown() = false, want true")
	}
	found := false
	for _, harness := range AllHarnesses {
		if harness == HarnessOMP {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("AllHarnesses does not contain HarnessOMP")
	}
}

func TestReasonixHarnessIsKnownOnce(t *testing.T) {
	harness := HarnessReasonix
	if harness != AgentHarness("reasonix") {
		t.Fatalf("HarnessReasonix = %q, want reasonix", harness)
	}
	if !harness.IsKnown() {
		t.Fatal("reasonix is not a selectable harness")
	}
	count := 0
	for _, h := range AllHarnesses {
		if h == harness {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("reasonix appears %d times in AllHarnesses, want 1", count)
	}
}
