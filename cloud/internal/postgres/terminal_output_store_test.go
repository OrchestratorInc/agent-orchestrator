package postgres

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

type terminalOutputFixture struct {
	store    *Store
	fixture  notificationFixture
	terminal domain.TerminalSession
}

func openTerminalOutputFixture(t *testing.T) terminalOutputFixture {
	t.Helper()
	store, _, fixture := openNotificationTestStore(t)
	terminal, err := store.EnsureWorkerAgentTerminal(
		context.Background(), fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch, time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.NextOutputSequence != 1 {
		t.Fatalf("fresh terminal cursor = %d, want 1", terminal.NextOutputSequence)
	}
	return terminalOutputFixture{store: store, fixture: fixture, terminal: terminal}
}

func (f terminalOutputFixture) append(t *testing.T, sequence int64, data string) (int64, error) {
	t.Helper()
	return f.store.AppendTerminalOutputAt(
		context.Background(), f.fixture.orgID, f.fixture.sessionID, f.fixture.workerID,
		f.terminal.ID, f.fixture.epoch, sequence, []byte(data),
	)
}

func (f terminalOutputFixture) frames(t *testing.T) []domain.TerminalOutput {
	t.Helper()
	var all []domain.TerminalOutput
	var after int64
	for {
		page, _, err := f.store.ListTerminalOutput(context.Background(), f.terminal, after, 500)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, page...)
		if len(page) < 500 {
			return all
		}
		after = page[len(page)-1].Sequence
	}
}

type terminalOutputRow struct {
	state       string
	next        int64
	outputBytes int64
	storedBytes int64
}

func (f terminalOutputFixture) row(t *testing.T) terminalOutputRow {
	t.Helper()
	var row terminalOutputRow
	err := f.store.withOrg(context.Background(), f.fixture.orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT terminal.state, terminal.next_output_sequence, terminal.output_bytes,
				COALESCE((SELECT sum(octet_length(output.data)) FROM ao_terminal_output output
					WHERE output.org_id = terminal.org_id AND output.terminal_id = terminal.id), 0)
			FROM ao_terminal_sessions terminal
			WHERE terminal.org_id = $1 AND terminal.id = $2`,
			f.fixture.orgID, f.terminal.ID,
		).Scan(&row.state, &row.next, &row.outputBytes, &row.storedBytes)
	})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func sequences(frames []domain.TerminalOutput) []int64 {
	result := make([]int64, 0, len(frames))
	for _, frame := range frames {
		result = append(result, frame.Sequence)
	}
	return result
}

// A worker resends a frame it already delivered when its stream died before
// the acknowledgement arrived. The resend must be acknowledged, not rejected,
// and must not be stored twice.
func TestAppendTerminalOutputAtAcknowledgesDuplicate(t *testing.T) {
	f := openTerminalOutputFixture(t)
	if _, err := f.append(t, 1, "first"); err != nil {
		t.Fatal(err)
	}
	sequence, err := f.append(t, 1, "first")
	if err != nil {
		t.Fatalf("duplicate frame error = %v, want acknowledgement", err)
	}
	if sequence != 1 {
		t.Fatalf("duplicate frame sequence = %d, want 1", sequence)
	}
	if got := sequences(f.frames(t)); len(got) != 1 || got[0] != 1 {
		t.Fatalf("stored sequences = %v, want [1]", got)
	}
	row := f.row(t)
	if row.next != 2 || row.outputBytes != int64(len("first")) || row.storedBytes != row.outputBytes {
		t.Fatalf("terminal row = %+v, want cursor 2 and %d bytes", row, len("first"))
	}
	if _, err := f.append(t, 2, "second"); err != nil {
		t.Fatalf("next frame after a duplicate: %v", err)
	}
}

// A frame lost in transit leaves a hole in the worker's numbering. Every later
// frame must still be stored; the hole stays visible as a sequence gap.
func TestAppendTerminalOutputAtAcceptsGap(t *testing.T) {
	f := openTerminalOutputFixture(t)
	if _, err := f.append(t, 1, "one"); err != nil {
		t.Fatal(err)
	}
	sequence, err := f.append(t, 3, "three")
	if err != nil {
		t.Fatalf("frame after a gap error = %v, want it stored", err)
	}
	if sequence != 3 {
		t.Fatalf("gap frame sequence = %d, want 3", sequence)
	}
	// The lost frame arriving late is below the cursor now: acknowledged, but
	// not spliced into output that already moved on.
	if _, err := f.append(t, 2, "two"); err != nil {
		t.Fatalf("late frame error = %v, want acknowledgement", err)
	}
	if _, err := f.append(t, 4, "four"); err != nil {
		t.Fatal(err)
	}
	got := sequences(f.frames(t))
	want := []int64{1, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("stored sequences = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("stored sequences = %v, want %v", got, want)
		}
	}
	if row := f.row(t); row.next != 5 || row.storedBytes != row.outputBytes {
		t.Fatalf("terminal row = %+v, want cursor 5 with accurate bytes", row)
	}
}

// Output past the retention bound prunes the oldest frames instead of failing
// the terminal (the old lifetime cap rejected every frame after 4 MiB). The
// terminal stays open, output_bytes matches what is stored, and replay is the
// newest frames in order.
func TestAppendTerminalOutputPrunesOldestFrames(t *testing.T) {
	f := openTerminalOutputFixture(t)
	frame := bytes.Repeat([]byte("x"), 16<<10)
	total := int64(terminalOutputRetainBytes/len(frame)) + 64
	for sequence := int64(1); sequence <= total; sequence++ {
		if _, err := f.store.AppendTerminalOutputAt(
			context.Background(), f.fixture.orgID, f.fixture.sessionID, f.fixture.workerID,
			f.terminal.ID, f.fixture.epoch, sequence, frame,
		); err != nil {
			t.Fatalf("append frame %d of %d: %v", sequence, total, err)
		}
		if sequence%64 == 0 {
			if row := f.row(t); row.outputBytes > terminalOutputRetainBytes || row.outputBytes != row.storedBytes {
				t.Fatalf("after frame %d terminal row = %+v, want <= %d bytes matching storage",
					sequence, row, terminalOutputRetainBytes)
			}
		}
	}
	row := f.row(t)
	if row.state != "open" {
		t.Fatalf("terminal state = %q, want open", row.state)
	}
	if row.outputBytes > terminalOutputRetainBytes || row.outputBytes != row.storedBytes {
		t.Fatalf("terminal row = %+v, want <= %d bytes matching storage", row, terminalOutputRetainBytes)
	}
	got := sequences(f.frames(t))
	if len(got) == 0 || got[0] <= 1 {
		t.Fatalf("replay starts at %v, want the pruned head (> 1)", got[:min(len(got), 1)])
	}
	for index := 1; index < len(got); index++ {
		if got[index] != got[index-1]+1 {
			t.Fatalf("replay out of order at %d: %d after %d", index, got[index], got[index-1])
		}
	}
	if got[len(got)-1] != total {
		t.Fatalf("replay ends at %d, want newest frame %d", got[len(got)-1], total)
	}
}

// A closed terminal and an expired lease are different conditions with their
// own errors, neither of them the worker-request ErrTransportExpired. A
// rejection writes nothing and leaves the terminal row as it was.
func TestAppendTerminalOutputDistinguishesClosedAndExpired(t *testing.T) {
	f := openTerminalOutputFixture(t)
	ctx := context.Background()
	if _, err := f.append(t, 1, "live"); err != nil {
		t.Fatal(err)
	}

	if err := f.store.withOrg(ctx, f.fixture.orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE ao_terminal_sessions
			SET created_at = now() - interval '2 hours', expires_at = now() - interval '1 hour'
			WHERE org_id = $1 AND id = $2`,
			f.fixture.orgID, f.terminal.ID,
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	before := f.row(t)
	_, err := f.append(t, 2, "late")
	if !errors.Is(err, ErrTerminalExpired) || errors.Is(err, ErrTransportExpired) {
		t.Fatalf("expired terminal error = %v, want ErrTerminalExpired", err)
	}
	if after := f.row(t); after != before {
		t.Fatalf("expired rejection changed the terminal: %+v -> %+v", before, after)
	}

	if err := f.store.withOrg(ctx, f.fixture.orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE ao_terminal_sessions SET expires_at = now() + interval '1 hour'
			WHERE org_id = $1 AND id = $2`,
			f.fixture.orgID, f.terminal.ID,
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CloseTerminal(ctx, f.terminal); err != nil {
		t.Fatal(err)
	}
	before = f.row(t)
	_, err = f.append(t, 2, "late")
	if !errors.Is(err, ErrTerminalClosed) || errors.Is(err, ErrTransportExpired) {
		t.Fatalf("closed terminal error = %v, want ErrTerminalClosed", err)
	}
	if after := f.row(t); after != before || after.state != "closed" {
		t.Fatalf("closed rejection changed the terminal: %+v -> %+v", before, after)
	}

	_, err = f.store.AppendTerminalOutputAt(
		ctx, f.fixture.orgID, f.fixture.sessionID, f.fixture.workerID,
		uuid.NewString(), f.fixture.epoch, 1, []byte("nowhere"),
	)
	if !errors.Is(err, ErrTerminalClosed) {
		t.Fatalf("unknown terminal error = %v, want ErrTerminalClosed", err)
	}
}

// The old lifetime cap marked the terminal failed inside the transaction that
// then returned its error, so the state change rolled back and the terminal
// stayed open while refusing all output. Crossing that old cap must now commit
// a consistent row: still open, still accepting frames, bytes accurate.
func TestAppendTerminalOutputPastOldCapCommitsConsistentState(t *testing.T) {
	f := openTerminalOutputFixture(t)
	frame := bytes.Repeat([]byte("y"), 16<<10)
	oldCap := int64(4 << 20)
	last := oldCap/int64(len(frame)) + 8
	for sequence := int64(1); sequence <= last; sequence++ {
		if _, err := f.store.AppendTerminalOutputAt(
			context.Background(), f.fixture.orgID, f.fixture.sessionID, f.fixture.workerID,
			f.terminal.ID, f.fixture.epoch, sequence, frame,
		); err != nil {
			t.Fatalf("append frame %d past the old cap: %v", sequence, err)
		}
	}
	row := f.row(t)
	if row.state != "open" || row.next != last+1 || row.outputBytes != row.storedBytes ||
		row.outputBytes != last*int64(len(frame)) {
		t.Fatalf("terminal row = %+v, want open, cursor %d, %d bytes", row, last+1, last*int64(len(frame)))
	}
}
