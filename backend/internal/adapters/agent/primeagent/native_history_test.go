package primeagent

import (
	"context"
	"strings"
	"testing"
)

func TestPrimeNativeFingerprintMatchesProviderChildUsageFolding(t *testing.T) {
	for _, attribution := range []bool{false, true} {
		t.Run(map[bool]string{false: "unattributed usage retained", true: "aggregate usage materialized"}[attribution], func(t *testing.T) {
			root, cwd := t.TempDir(), t.TempDir()
			user := primeNativeFixtureRows()[0]
			assistant := `{"type":"message","id":"assistant-1","parentId":"user-1","message":{"role":"assistant","usage":{"input":1,"output":1},"content":[{"type":"text","text":"same native answer"}]}}`
			materialized := strings.Replace(assistant, `"usage":{"input":1,"output":1}`, `"usage":{"input":9,"output":5}`, 1)
			sourceRows, forkRows := []string{user, assistant}, []string{user, materialized}
			if attribution {
				update := `{"type":"child_usage_attributed","id":"usage-1","parentId":"assistant-1","targetId":"assistant-1","origin":"spawn_task","childUsage":{"input":8,"output":4},"aggregateUsage":{"input":9,"output":5}}`
				sourceRows = append(sourceRows, update)
				forkRows = append(forkRows, update)
			}
			source := writePrimeNativeFixture(t, root, primeSourceID, cwd, "", sourceRows...)
			fork := writePrimeNativeFixture(t, root, primeForkID, cwd, source, forkRows...)
			expected, err := readPrimeNativeHistory(context.Background(), root, source, 0)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := readPrimeNativeHistory(context.Background(), root, fork, 0)
			if err != nil {
				t.Fatal(err)
			}
			if equal := expected.digest == actual.digest; equal != attribution {
				t.Fatalf("usage normalization: equal=%v, attribution=%v", equal, attribution)
			}
			if attribution {
				forkRows[1] = strings.Replace(forkRows[1], "same native answer", "changed answer", 1)
				writePrimeNativeFixture(t, root, primeForkID, cwd, source, forkRows...)
				changed, err := readPrimeNativeHistory(context.Background(), root, fork, 0)
				if err != nil || changed.digest == expected.digest {
					t.Fatalf("normalization hid a non-usage change: %v", err)
				}
			}
		})
	}
}
