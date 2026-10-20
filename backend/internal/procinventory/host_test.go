package procinventory

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Real vm_stat capture from an Apple Silicon machine (16384-byte pages,
// values with trailing periods).
const vmStatArmFixture = `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                                    25530.
Pages active:                                 320196.
Pages inactive:                               316011.
Pages speculative:                              3775.
Pages throttled:                                   0.
Pages wired down:                             228078.
Pages purgeable:                                  10.
"Translation faults":                    20537834513.
Pages copy-on-write:                       867671047.
Pages zero filled:                       10709637002.
Pages reactivated:                      2377910527.
Swapouts:                                     967976.
File-backed pages:                            987654.
Pages occupied by compressor:                 123456.
`

func TestParseVmStat_ArmCapture(t *testing.T) {
	vm, err := ParseVmStat(vmStatArmFixture)
	if err != nil {
		t.Fatalf("ParseVmStat: %v", err)
	}
	if vm.PageSize != 16384 {
		t.Fatalf("page size = %d, want 16384", vm.PageSize)
	}
	if vm.Free != 25530 || vm.Speculative != 3775 || vm.Wired != 228078 {
		t.Fatalf("page buckets = %+v", vm)
	}
	if vm.FileBacked != 987654 || vm.Compressor != 123456 {
		t.Fatalf("file/compressor = %d/%d", vm.FileBacked, vm.Compressor)
	}
}

func TestParseVmStat_IntelPageSizeAndMissingKey(t *testing.T) {
	intel := strings.Replace(vmStatArmFixture, "page size of 16384 bytes", "page size of 4096 bytes", 1)
	vm, err := ParseVmStat(intel)
	if err != nil {
		t.Fatalf("ParseVmStat: %v", err)
	}
	if vm.PageSize != 4096 {
		t.Fatalf("page size = %d, want 4096", vm.PageSize)
	}

	if _, err := ParseVmStat(strings.Replace(intel, "Pages wired down:", "Pages wired:", 1)); err == nil {
		t.Fatal("missing required bucket accepted")
	}
	if _, err := ParseVmStat("garbage"); err == nil {
		t.Fatal("header-less output accepted")
	}
}

func TestParseSwapUsage(t *testing.T) {
	// Real capture under ru_RU: decimal comma.
	swap, err := ParseSwapUsage("total = 8192,00M  used = 7031,62M  free = 1160,38M  (encrypted)")
	if err != nil {
		t.Fatalf("ParseSwapUsage: %v", err)
	}
	if swap.Total != 8192<<20 || swap.Used != 7031<<20+650117 || swap.Free != 1160<<20+398458 {
		t.Fatalf("swap = %+v", swap)
	}

	// C locale: decimal dot.
	swap, err = ParseSwapUsage("total = 8192.00M  used = 7023.62M  free = 1168.38M  (encrypted)")
	if err != nil {
		t.Fatalf("ParseSwapUsage: %v", err)
	}
	if swap.Total != 8192<<20 {
		t.Fatalf("total = %d", swap.Total)
	}

	// G suffix and missing component.
	swap, err = ParseSwapUsage("total = 8.00G  used = 7.00G  free = 1.00G")
	if err != nil {
		t.Fatalf("ParseSwapUsage: %v", err)
	}
	if swap.Total != 8<<30 {
		t.Fatalf("total = %d", swap.Total)
	}
	if _, err := ParseSwapUsage("total = 8192.00M  used = 7031.62M"); err == nil {
		t.Fatal("missing free component accepted")
	}
}

func TestParseMeminfo(t *testing.T) {
	info, err := ParseMeminfo(`MemTotal:       25190656 kB
MemFree:         2188900 kB
MemAvailable:    8123456 kB
Buffers:          123456 kB
Cached:          9876543 kB
SwapTotal:       8388608 kB
SwapFree:        1234567 kB
SReclaimable:     234567 kB
`)
	if err != nil {
		t.Fatalf("ParseMeminfo: %v", err)
	}
	if info.Total != 25190656*1024 || info.Available != 8123456*1024 {
		t.Fatalf("total/available = %d/%d", info.Total, info.Available)
	}
	if info.Cached+info.SReclaimable != (9876543+234567)*1024 {
		t.Fatalf("cached = %d", info.Cached)
	}
	linux := composeLinuxHostStats(info)
	if linux.UsedBytes != (25190656-8123456)*1024 || linux.SwapMaxBytes != 8388608*1024 {
		t.Fatalf("linux stats = %+v", linux)
	}
}

func TestComposeDarwinHostStats(t *testing.T) {
	vm := vmStat{PageSize: 16384, Free: 25530, Speculative: 3775, Wired: 228078, FileBacked: 987654, Compressor: 123456}
	swap := swapUsage{Total: 8 << 30, Used: 7 << 30, Free: 1 << 30}
	pressure := 44
	stats := composeDarwinHostStats(24<<30, swap, vm, 34<<30, &pressure)

	if stats.FreeBytes != (25530+3775)*16384 {
		t.Fatalf("free = %d", stats.FreeBytes)
	}
	if stats.WiredBytes != 228078*16384 || stats.CompressedBytes != 123456*16384 {
		t.Fatalf("wired/compressed = %d/%d", stats.WiredBytes, stats.CompressedBytes)
	}
	if stats.CachedBytes != 987654*16384 {
		t.Fatalf("cached = %d", stats.CachedBytes)
	}
	if stats.UsedBytes != 24<<30-stats.FreeBytes-stats.CachedBytes {
		t.Fatalf("used = %d", stats.UsedBytes)
	}
	if stats.SwapMaxBytes != 8<<30+34<<30 {
		t.Fatalf("swap max = %d", stats.SwapMaxBytes)
	}
	if stats.PressureFreePercent == nil || *stats.PressureFreePercent != 44 {
		t.Fatalf("pressure = %v", stats.PressureFreePercent)
	}
}

func TestParsePressureFreePercent(t *testing.T) {
	percent, ok := ParsePressureFreePercent("System-wide memory free percentage: 44%\n")
	if !ok || percent != 44 {
		t.Fatalf("percent = %d ok = %v", percent, ok)
	}
	if _, ok := ParsePressureFreePercent("nothing here"); ok {
		t.Fatal("absent line reported as present")
	}
}

func TestServiceInventory_HostAttachedAndTolerated(t *testing.T) {
	entries := []Entry{{PID: testDaemonPID, PPID: 1, PGID: testDaemonPID, RSSKB: 90000, Command: "/usr/local/bin/ao daemon"}}
	base := Deps{
		Scan:      func(context.Context) ([]Entry, error) { return ParseTable(psTable(entries)) },
		DaemonPID: testDaemonPID,
	}

	svc := New(Deps{
		Scan:      base.Scan,
		DaemonPID: testDaemonPID,
		HostStats: func(context.Context) (HostStats, error) {
			return HostStats{TotalBytes: 24 << 30, UsedBytes: 5 << 30}, nil
		},
	})
	inv, err := svc.Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	if inv.Host == nil || inv.Host.TotalBytes != 24<<30 {
		t.Fatalf("host = %+v", inv.Host)
	}

	svc = New(Deps{
		Scan:      base.Scan,
		DaemonPID: testDaemonPID,
		HostStats: func(context.Context) (HostStats, error) { return HostStats{}, errors.New("boom") },
	})
	inv, err = svc.Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory with host error: %v", err)
	}
	if inv.Host != nil {
		t.Fatalf("host should be nil on error, got %+v", inv.Host)
	}
}
