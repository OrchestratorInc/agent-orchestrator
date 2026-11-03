package usage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// maxSnapshotFileBytes bounds one read of a whole-file usage snapshot.
	// Snapshots hold totals per session and model, so they stay small.
	maxSnapshotFileBytes = 8 << 20
	// maxSnapshotUsageRows bounds the per-row baselines a snapshot source keeps.
	maxSnapshotUsageRows = 4096
)

// snapshotUsageReader reads the cumulative rows of one usage file that its
// agent rewrites whole. Like a SQLite reader it returns the rows as JSON
// records and a cursor that grows with usage, so the polled ingestion path
// reads both alike.
type snapshotUsageReader func(ctx context.Context, path string, source domain.UsageSourceRecord) (sqliteUsageChunk, error)

var snapshotUsageReaders = map[domain.UsageSourceKind]snapshotUsageReader{
	domain.UsageSourceGrokSession: readGrokUsageChunk,
}

func snapshotUsageSource(kind domain.UsageSourceKind) bool {
	_, ok := snapshotUsageReaders[kind]
	return ok
}

// snapshotParserStateV1 is the last observed value of every cumulative row a
// snapshot reports. The difference from that baseline is exactly the usage
// since the previous read, as Hermes's and Codex's baselines are.
type snapshotParserStateV1 struct {
	Rows map[string]hermesUsageCounters `json:"rows"`
	// Seeded records that the first read took the usage the session
	// inherited, from a fork or restore, as its baseline.
	Seeded bool `json:"seeded,omitempty"`
}

func validateSnapshotParserState(state *snapshotParserStateV1) error {
	if len(state.Rows) > maxSnapshotUsageRows {
		return errors.New("snapshot state exceeds row bound")
	}
	for _, counters := range state.Rows {
		if !counters.valid() {
			return errors.New("snapshot state has negative counters")
		}
	}
	return nil
}

// readSnapshotFile reads a usage snapshot no larger than the snapshot bound.
func readSnapshotFile(path string) ([]byte, error) {
	file, err := os.Open(path) //nolint:gosec // path was validated at registration.
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxSnapshotFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSnapshotFileBytes {
		return nil, fmt.Errorf("usage snapshot exceeds %d bytes", maxSnapshotFileBytes)
	}
	return data, nil
}

// minCounters is the smaller value of every counter. A fork's inherited usage
// is at most what both the parent and the fork report now.
func minCounters(a, b hermesUsageCounters) hermesUsageCounters {
	return hermesUsageCounters{
		APICalls:           min(a.APICalls, b.APICalls),
		InputTokens:        min(a.InputTokens, b.InputTokens),
		OutputTokens:       min(a.OutputTokens, b.OutputTokens),
		CacheReadTokens:    min(a.CacheReadTokens, b.CacheReadTokens),
		CacheWriteTokens:   min(a.CacheWriteTokens, b.CacheWriteTokens),
		ReasoningTokens:    min(a.ReasoningTokens, b.ReasoningTokens),
		EstimatedCostNanos: min(a.EstimatedCostNanos, b.EstimatedCostNanos),
		ActualCostNanos:    min(a.ActualCostNanos, b.ActualCostNanos),
	}
}

// counterVolume is the total of every token and call counter, the cursor a
// snapshot source stores: it grows whenever any row does.
func counterVolume(total int64, c hermesUsageCounters) int64 {
	if volume, ok := sumNonNegative(
		total, c.APICalls, c.InputTokens, c.OutputTokens, c.CacheReadTokens, c.CacheWriteTokens, c.ReasoningTokens,
	); ok {
		return volume
	}
	return total
}

// advanceCumulativeRow makes current the row's baseline and returns the growth
// since the previous one. It reports false when there is nothing to bill: the
// row is unchanged, over the row bound, or shrank. A shrinking row only
// happens when the agent reset its totals, so billing the drop would be
// negative and billing the new value could repeat earlier calls; it records
// an anomaly instead.
func advanceCumulativeRow(
	rows map[string]hermesUsageCounters,
	key string,
	current hermesUsageCounters,
	result *parseResult,
) (hermesUsageCounters, bool) {
	baseline, known := rows[key]
	if !known && len(rows) >= maxSnapshotUsageRows {
		recordMalformed(result)
		return hermesUsageCounters{}, false
	}
	rows[key] = current
	if current.below(baseline) {
		result.Cursor.AnomalyCount++
		result.Cursor.LastErrorCode = domain.UsageErrorNonMonotonicCumulativeUsage
		return hermesUsageCounters{}, false
	}
	if current == baseline {
		return hermesUsageCounters{}, false
	}
	return current.minus(baseline), true
}
