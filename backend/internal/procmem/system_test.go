package procmem

import "testing"

func TestParsePSISome10(t *testing.T) {
	some, ok := ParsePSISome10("some avg10=12.34 avg60=0.37 avg300=0.20 total=1068494611\nfull avg10=0.01 avg60=0.29 avg300=0.16 total=971321063\n")
	if !ok || some != 12.34 {
		t.Fatalf("some = %v ok=%v, want 12.34", some, ok)
	}
	if _, ok := ParsePSISome10("full avg10=0.01\n"); ok {
		t.Fatal("no some line must report not ok")
	}
}

func TestParseCPUTicksSkipsIdleAndIOWait(t *testing.T) {
	busy, total := ParseCPUTicks("cpu  100 20 50 800 30 5 5 0 0 0\ncpu0 1 2 3 4 5 6 7 8 9 10\n")
	if busy != 180 || total != 1010 {
		t.Fatalf("busy=%d total=%d, want 180/1010", busy, total)
	}
	if b, tt := ParseCPUTicks("intr 1 2 3\n"); b != 0 || tt != 0 {
		t.Fatal("missing cpu line must read as zero")
	}
}

func TestAvailablePressureIsUsedShare(t *testing.T) {
	if got := availablePressure(System{TotalBytes: 16 << 30, AvailableBytes: 4 << 30}); got != 75 {
		t.Fatalf("pressure = %v, want 75", got)
	}
}
