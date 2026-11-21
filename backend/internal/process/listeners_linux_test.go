package process

import (
	"context"
	"errors"
	"maps"
	"testing"
)

func TestListenersWithEnvStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ListenersWithEnv(ctx, "PATH", "", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestParseProcNetListenersKeepsLoopbackAndUnspecifiedListeners(t *testing.T) {
	table := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1435 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 101 1 0 100 0 0 10 0
   1: 00000000:1538 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 102 1 0 100 0 0 10 0
   2: 2A01A8C0:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 103 1 0 100 0 0 10 0
   3: 0100007F:1435 0100007F:C350 01 00000000:00000000 00:00000000 00000000  1000        0 104 1 0 100 0 0 10 0
   0: 00000000000000000000000001000000:1F90 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 105 1 0 100 0 0 10 0
`
	got := map[string]int{}
	parseProcNetListeners(table, got)
	want := map[string]int{"101": 5173, "102": 5432, "105": 8080}
	if !maps.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
