package settings

import "testing"

func TestResolveMemoryReservePrefersAnExplicitSetting(t *testing.T) {
	const gib = 1 << 30
	bytes, auto := Snapshot{MemoryReserveBytes: 4 * gib}.ResolveMemoryReserve()
	if bytes != 4*gib || auto {
		t.Fatalf("explicit reserve resolved to %d auto=%v", bytes, auto)
	}
	bytes, auto = Snapshot{}.ResolveMemoryReserve()
	if bytes != 2*gib || !auto {
		t.Fatalf("default reserve resolved to %d auto=%v", bytes, auto)
	}
}
