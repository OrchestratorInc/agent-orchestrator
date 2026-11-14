//go:build linux

package procmem

import "testing"

func TestReadSystemDerivesSwapPageBytesFromTheHostsRealPageSize(t *testing.T) {
	orig := pageSize
	defer func() { pageSize = orig }()
	pageSize = func() int { return 16384 }

	sys, err := ReadSystem()
	if err != nil {
		t.Fatal(err)
	}
	if sys.SwapPageBytes != 16384 {
		t.Fatalf("SwapPageBytes = %d, want 16384 (a non-4 KiB page size)", sys.SwapPageBytes)
	}
}
