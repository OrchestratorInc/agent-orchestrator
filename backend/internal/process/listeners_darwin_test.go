package process

import (
	"slices"
	"testing"
)

func TestParseLsofListenersKeepsLoopbackAndUnspecifiedListeners(t *testing.T) {
	out := "p100\ncnode\nf21\nn127.0.0.1:5173\nf22\nn[::1]:5173\np200\ncpostgres\nf5\nn*:5432\nf6\nn192.168.1.4:80\np300\ncother\nf3\nn127.0.0.1:9000\n"
	got := parseLsofListeners(out, func(pid int) bool { return pid != 300 })
	want := []Listener{{5173, 100, "node"}, {5173, 100, "node"}, {5432, 200, "postgres"}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := uniqueByPort(got); !slices.Equal(got, []Listener{{5173, 100, "node"}, {5432, 200, "postgres"}}) {
		t.Fatalf("unique: %v", got)
	}
}
