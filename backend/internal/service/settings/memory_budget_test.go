package settings

import "testing"

func TestAutoMemoryBudgetIsAQuarterOfRAMClampedToTwoThroughSixteenGiB(t *testing.T) {
	const gib = 1 << 30
	cases := map[uint64]uint64{
		4 * gib:   2 * gib,  // laptop: floor keeps one real session possible
		8 * gib:   2 * gib,  // exactly the floor
		16 * gib:  4 * gib,  // a quarter
		32 * gib:  8 * gib,  // a quarter
		128 * gib: 16 * gib, // ceiling
	}
	for total, want := range cases {
		if got := AutoMemoryBudget(total); got != want {
			t.Errorf("AutoMemoryBudget(%d GiB) = %d GiB, want %d GiB", total/gib, got/gib, want/gib)
		}
	}
}

func TestResolveMemoryBudgetPrefersAnExplicitSetting(t *testing.T) {
	const gib = 1 << 30
	bytes, auto := Snapshot{MemoryBudgetBytes: 6 * gib}.ResolveMemoryBudget(32 * gib)
	if bytes != 6*gib || auto {
		t.Fatalf("explicit budget resolved to %d auto=%v", bytes, auto)
	}
	bytes, auto = Snapshot{}.ResolveMemoryBudget(32 * gib)
	if bytes != 8*gib || !auto {
		t.Fatalf("auto budget resolved to %d auto=%v", bytes, auto)
	}
}
