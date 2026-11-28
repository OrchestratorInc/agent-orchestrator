package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every sqlc query set must be built through utcTimes (see utc_times.go). Using
// Queries.WithTx or gen.New directly would quietly bring back stored times in
// the sender's zone, which the driver may not be able to read back.
func TestQueriesAreNeverBuiltAroundTheUTCWrapper(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") || file == "utc_times.go" {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if strings.Contains(line, ".WithTx(") {
				t.Errorf("%s:%d uses Queries.WithTx; use s.txQueries(tx)", file, i+1)
			}
			if strings.Contains(line, "gen.New(") && !strings.Contains(line, "utcTimes{") {
				t.Errorf("%s:%d builds a query set without utcTimes", file, i+1)
			}
		}
	}
}
