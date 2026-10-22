package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Full print path: res in daemon order (heaviest NOT first) must come out
// heaviest-first from writeProcessInventory.
func TestWriteProcessInventory_SortsSessionsDesc(t *testing.T) {
	res := processInventoryResponse{
		Totals: processTotalsDTO{SessionsCount: 3, SessionsRSSBytes: 660 * 1024 * 1024},
		Trees: []processTreeDTO{
			{SessionID: "small", RootPID: 3, RSSBytes: 31 * 1024 * 1024, State: "owned", Attached: true},
			{SessionID: "huge", RootPID: 1, RSSBytes: 374 * 1024 * 1024, State: "owned", Attached: true},
			{SessionID: "mid", RootPID: 2, RSSBytes: 306 * 1024 * 1024, State: "owned", Attached: true},
		},
	}
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := writeProcessInventory(cmd, res); err != nil {
		t.Fatalf("writeProcessInventory: %v", err)
	}
	text := out.String()
	iHuge := strings.Index(text, "huge")
	iMid := strings.Index(text, "mid")
	iSmall := strings.Index(text, "small")
	if iHuge < 0 || iHuge >= iMid || iMid >= iSmall {
		t.Fatalf("row order wrong (huge=%d mid=%d small=%d):\n%s", iHuge, iMid, iSmall, text)
	}
}

// The disk-free segment is the swap growth ceiling minus current swap: on
// linux SwapMaxBytes equals SwapTotalBytes, so the segment would always read
// 0 B and must be hidden instead of lying (review comment on #96).
func TestWriteProcessInventory_HidesDiskFreeWhenSwapCannotGrow(t *testing.T) {
	for _, tt := range []struct {
		name           string
		swapTotalBytes int64
		swapMaxBytes   int64
		wantDiskFree   bool
	}{
		{name: "linux-shaped host hides the segment", swapTotalBytes: 8 << 30, swapMaxBytes: 8 << 30, wantDiskFree: false},
		{name: "darwin-shaped host keeps the segment", swapTotalBytes: 8 << 30, swapMaxBytes: 40 << 30, wantDiskFree: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := processInventoryResponse{
				Host: &processHostMemoryDTO{
					TotalBytes:     24 << 30,
					UsedBytes:      20 << 30,
					SwapTotalBytes: tt.swapTotalBytes,
					SwapMaxBytes:   tt.swapMaxBytes,
				},
			}
			var out bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetOut(&out)
			if err := writeProcessInventory(cmd, res); err != nil {
				t.Fatalf("writeProcessInventory: %v", err)
			}
			got := out.String()
			if has := strings.Contains(got, "disk free"); has != tt.wantDiskFree {
				t.Fatalf("disk free segment present = %v, want %v:\n%s", has, tt.wantDiskFree, got)
			}
		})
	}
}
