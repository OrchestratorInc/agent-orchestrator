package coderagents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Journal states of one client-executed tool call.
const (
	callStarted   = "started"
	callDone      = "done"
	callSubmitted = "submitted"
)

// callRecord is the durable state of one Coder tool call ID.
type callRecord struct {
	State   string       `json:"state"`
	Outcome *toolOutcome `json:"outcome,omitempty"`
}

// toolJournal makes tool execution idempotent across stream replay, reconnect
// and daemon restart. A call is recorded as started before it runs and done
// with its result after; a replayed call is answered from the record and
// never run again. A call found started without a result had an unknown
// effect, so it is reported to the model as such instead of being re-run.
type toolJournal struct {
	dir string
}

func newToolJournal(dataDir, conversationKey string) *toolJournal {
	sum := sha256.Sum256([]byte(conversationKey))
	return &toolJournal{dir: filepath.Join(dataDir, "coder-agents", "tool-calls", hex.EncodeToString(sum[:16]))}
}

func (j *toolJournal) path(callID string) string {
	sum := sha256.Sum256([]byte(callID))
	return filepath.Join(j.dir, hex.EncodeToString(sum[:16])+".json")
}

func (j *toolJournal) load(callID string) (callRecord, bool, error) {
	data, err := os.ReadFile(j.path(callID))
	if errors.Is(err, fs.ErrNotExist) {
		return callRecord{}, false, nil
	}
	if err != nil {
		return callRecord{}, false, err
	}
	var record callRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return callRecord{}, false, fmt.Errorf("decode tool call record: %w", err)
	}
	return record, true, nil
}

func (j *toolJournal) store(callID string, record callRecord) error {
	if err := os.MkdirAll(j.dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	target := j.path(callID)
	tmp, err := os.CreateTemp(j.dir, ".call-*")
	if err != nil {
		return err
	}
	_, writeErr := tmp.Write(data)
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

func (j *toolJournal) start(callID string) error {
	return j.store(callID, callRecord{State: callStarted})
}

func (j *toolJournal) finish(callID string, outcome toolOutcome) error {
	return j.store(callID, callRecord{State: callDone, Outcome: &outcome})
}

// submitted drops the stored output once Coder accepted it; only the fact
// that the call completed is kept, so a replay still never re-runs it.
func (j *toolJournal) submitted(callID string) error {
	return j.store(callID, callRecord{State: callSubmitted})
}
