package conpty

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func restoreAfter(t *testing.T, chunks ...string) string {
	t.Helper()
	m := newModeTracker()
	for _, c := range chunks {
		if _, err := m.Write([]byte(c)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	return string(m.Restore(nil))
}

func restoreOver(t *testing.T, chunks ...string) string {
	t.Helper()
	m := newModeTracker()
	for _, c := range chunks {
		if _, err := m.Write([]byte(c)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	return string(m.Restore([]byte(chunks[len(chunks)-1])))
}

func TestModeTracker_DefaultStateRestoresNothing(t *testing.T) {
	if got := restoreAfter(t, "plain text\r\n\x1b[31mred\x1b[0m\n"); got != "" {
		t.Fatalf("Restore = %q, want empty", got)
	}
	if got := newModeTracker().Restore(nil); got != nil {
		t.Fatalf("Restore on a fresh tracker = %v, want nil", got)
	}
}

func TestModeTracker_FullScreenHandshake(t *testing.T) {
	got := restoreAfter(t, "\x1b[?2004h", "banner\n", "\x1b[?1049h\x1b[?25l", "frame\n", "\x1b[?1003h\x1b[?1006h")
	want := "\x1b[?1049h\x1b[?25l\x1b[?1003h\x1b[?1006h\x1b[?2004h"
	if got != want {
		t.Fatalf("Restore = %q, want %q", got, want)
	}
}

func TestModeTracker_LeavingTheAltScreenClearsIt(t *testing.T) {
	got := restoreAfter(t, "\x1b[?1049h\x1b[?1003h\x1b[?1006h", "\x1b[?1003l\x1b[?1006l\x1b[?1049l")
	if got != "" {
		t.Fatalf("Restore after exit = %q, want empty", got)
	}
}

func TestModeTracker_MouseSlotsMirrorXterm(t *testing.T) {
	cases := []struct{ in, want string }{
		{"\x1b[?1000h\x1b[?1003h", "\x1b[?1003h"},
		{"\x1b[?1000h\x1b[?1003h\x1b[?1000l", ""},
		{"\x1b[?1006h\x1b[?1016h", "\x1b[?1016h"},
		{"\x1b[?1002h\x1b[?1006h\x1b[?1016l", "\x1b[?1002h"},
		{"\x1b[?9h", "\x1b[?9h"},
		{"\x1b[?1005h", ""},
		{"\x1b[?1002h\x1b[?1006h\x1b[?1015l", "\x1b[?1002h\x1b[?1006h"},
	}
	for _, c := range cases {
		if got := restoreAfter(t, c.in); got != c.want {
			t.Errorf("after %q: Restore = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestModeTracker_AltScreenReplaysTheNumberUsed(t *testing.T) {
	for _, n := range []int{47, 1047, 1049} {
		in := fmt.Sprintf("\x1b[?%dh", n)
		if got := restoreAfter(t, in); got != in {
			t.Errorf("after %q: Restore = %q, want %q", in, got, in)
		}
		if got := restoreAfter(t, in, "\x1b[?47l"); got != "" {
			t.Errorf("after %q then 47l: Restore = %q, want empty", in, got)
		}
	}
}

func TestModeTracker_MultipleParamsInOneSequence(t *testing.T) {
	got := restoreAfter(t, "\x1b[?1049;1002;1006h")
	want := "\x1b[?1049h\x1b[?1002h\x1b[?1006h"
	if got != want {
		t.Fatalf("Restore = %q, want %q", got, want)
	}
}

func TestModeTracker_DefaultOnModesReplayOnlyWhenReset(t *testing.T) {
	if got := restoreAfter(t, "\x1b[?7h\x1b[?25h"); got != "" {
		t.Fatalf("setting default-on modes: Restore = %q, want empty", got)
	}
	got := restoreAfter(t, "\x1b[?7l\x1b[?25l")
	want := "\x1b[?7l\x1b[?25l"
	if got != want {
		t.Fatalf("Restore = %q, want %q", got, want)
	}
}

func TestModeTracker_SequenceSplitAcrossWrites(t *testing.T) {
	full := "\x1b[?1049h\x1b[?1003h\x1b[?1006h"
	want := restoreAfter(t, full)
	for cut := 1; cut < len(full); cut++ {
		if got := restoreAfter(t, full[:cut], full[cut:]); got != want {
			t.Errorf("split at %d: Restore = %q, want %q", cut, got, want)
		}
	}
}

func TestModeTracker_StringPayloadsAreSkipped(t *testing.T) {
	cases := []struct{ in, want string }{
		{"\x1b]0;[?1049h\x07", ""},
		{"\x1b]0;title\x1b\\\x1b[?2004h", "\x1b[?2004h"},
		{"\x1bPq[?1003h\x1b\\", ""},
		{"\x1b_G[?47h\x1b\\\x1b[?1049h", "\x1b[?1049h"},
		{"\x1bPq\x07\x1b[?1049h\x1b\\", "\x1b[?1049h"},
		{"\x1b_G\x07[?2004h\x1b\\\x1b[?47h", "\x1b[?47h"},
	}
	for _, c := range cases {
		if got := restoreAfter(t, c.in); got != c.want {
			t.Errorf("after %q: Restore = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestModeTracker_SoftAndFullReset(t *testing.T) {
	all := "\x1b[?1049h\x1b[?1003h\x1b[?1006h\x1b[?1h\x1b[?66h\x1b[?1004h\x1b[?2004h\x1b[?7l\x1b[?25l"
	got := restoreAfter(t, all, "\x1b[!p")
	want := "\x1b[?1049h\x1b[?1003h\x1b[?1006h"
	if got != want {
		t.Fatalf("after DECSTR: Restore = %q, want %q", got, want)
	}
	if got := restoreAfter(t, all, "\x1bc"); got != "\x1b[?25l" {
		t.Fatalf("after RIS: Restore = %q, want only the hidden cursor", got)
	}
	if got := restoreAfter(t, "\x1b[?1049h\x1b[?1003h\x1b[?2004h", "\x1bc"); got != "" {
		t.Fatalf("after RIS with the cursor visible: Restore = %q, want empty", got)
	}
}

func TestModeTracker_KeypadEscapesShareTheFlag(t *testing.T) {
	if got := restoreAfter(t, "\x1b="); got != "\x1b[?66h" {
		t.Fatalf("after ESC =: Restore = %q, want ?66h", got)
	}
	if got := restoreAfter(t, "\x1b[?66h", "\x1b>"); got != "" {
		t.Fatalf("after ?66h then ESC >: Restore = %q, want empty", got)
	}
}

func TestModeTracker_ControlBytesInsideSequences(t *testing.T) {
	cases := []struct{ in, want string }{
		{"\x1b\r[?1049h", "\x1b[?1049h"},
		{"\x1b[?10\x0049h", "\x1b[?1049h"},
		{"\x1b[?1049\x7fh", "\x1b[?1049h"},
		{"\x1b[?1049\x18h", ""},
		{"\x1b\x1a[?1049h", ""},
		{"\x1b]0;title\x18\x1b[?2004h", "\x1b[?2004h"},
	}
	for _, c := range cases {
		if got := restoreAfter(t, c.in); got != c.want {
			t.Errorf("after %q: Restore = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestModeTracker_IgnoresAnsiModesAndUnknownPrivateModes(t *testing.T) {
	got := restoreAfter(t, "\x1b[4h\x1b[20h\x1b[?12h\x1b[?2026h\x1b[?1048h\x1b[?45h")
	if got != "" {
		t.Fatalf("Restore = %q, want empty", got)
	}
}

func TestModeTracker_OversizedParametersAreDropped(t *testing.T) {
	junk := "\x1b[?" + strings.Repeat("1;", maxParams) + "h"
	got := restoreAfter(t, junk, "\x1b[?2004h")
	if got != "\x1b[?2004h" {
		t.Fatalf("Restore = %q, want only the mode set after the junk", got)
	}
}

func TestModeTracker_RestoreRoundTrips(t *testing.T) {
	inputs := []string{
		"\x1b[?1049h\x1b[?1003h\x1b[?1006h\x1b[?2004h\x1b[?25l",
		"\x1b[?47h\x1b[?1000h\x1b[?1h\x1b[?7l",
		"\x1b[?2004h",
	}
	for _, in := range inputs {
		first := restoreAfter(t, in)
		if again := restoreAfter(t, first); again != first {
			t.Errorf("%q: Restore(Restore) = %q, want %q", in, again, first)
		}
		if !bytes.Equal([]byte(first), []byte(restoreAfter(t, first, first))) {
			t.Errorf("%q: applying the restore twice changed the state", in)
		}
	}
}

func TestModeTracker_PrefixCarriesOnlyWhatTheReplayLacks(t *testing.T) {
	handshake := "\x1b[?1049h\x1b[?25l\x1b[?1003h\x1b[?1006h\x1b[?2004h"
	cases := []struct {
		name, trimmed, replay, want string
	}{
		{"nothing trimmed", "", "$ prompt\n" + handshake + "frame\n", ""},
		{"everything trimmed", handshake + "frame\n", "frame\n", "\x1b[?1049h\x1b[?25l\x1b[?1003h\x1b[?1006h\x1b[?2004h"},
		{"paste toggled inside the replay", handshake, "\x1b[?2004l pasted \x1b[?2004h", "\x1b[?1049h\x1b[?25l\x1b[?1003h\x1b[?1006h"},
		{"replay re-hides the cursor", handshake, "\x1b[?25h\x1b[?25l", "\x1b[?1049h\x1b[?1003h\x1b[?1006h\x1b[?2004h"},
		{"program left the alt screen inside the replay", handshake, "\x1b[?1049l$ back\n", "\x1b[?25l\x1b[?1003h\x1b[?1006h\x1b[?2004h"},
		{"mouse protocol changed inside the replay", handshake, "\x1b[?1002h", "\x1b[?1049h\x1b[?25l\x1b[?1006h\x1b[?2004h"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := restoreOver(t, c.trimmed, c.replay); got != c.want {
				t.Fatalf("Restore = %q, want %q", got, c.want)
			}
		})
	}
}

func TestModeTracker_PrefixPlusReplayEqualsFullStream(t *testing.T) {
	streams := [][]string{
		{"\x1b[?1049h\x1b[?1003h\x1b[?1006h\x1b[?2004h\x1b[?25l", "frame\n\x1b[?2004l\x1b[?2004h"},
		{"\x1b[?47h\x1b[?1000h\x1b[?1h\x1b[?7l", "\x1b[?1000l\x1b[?1002h"},
		{"\x1b[?1049h\x1b[?1003h", "\x1b[!p"},
		{"\x1b[?1049h\x1b[?1003h\x1b[?2004h", "\x1bc$ fresh\n"},
		{"", "\x1b[?2004h"},
	}
	for _, st := range streams {
		full := newModeTracker()
		for _, c := range st {
			_, _ = full.Write([]byte(c))
		}
		replay := st[len(st)-1]
		prefix := full.Restore([]byte(replay))
		fresh := newModeTracker()
		_, _ = fresh.Write(prefix)
		_, _ = fresh.Write([]byte(replay))
		if fresh.st != full.st {
			t.Errorf("%q: prefix %q + replay gives %+v, full stream gives %+v", st, prefix, fresh.st, full.st)
		}
	}
}

func FuzzModeTracker(f *testing.F) {
	f.Add([]byte("\x1b[?2004h$ qwen\r\n\x1b[?1049h\x1b[?25l\x1b[?1003h\x1b[?1006h\x1b[2K\x1b[1Aframe\n"), 12)
	f.Add([]byte("\x1b]0;title\x1b\\\x1b[?47h\x1b=\x1b[?7l\x1b[!p\x1bc\x1b[?1002;1016h"), 3)
	f.Add([]byte("\x1b[?"+strings.Repeat("1;", 70)+"h\x1b[?2004h\x1b\x18[?1049h\x1bP q \x07 \x1b\\"), 40)
	f.Add([]byte{}, 0)
	f.Fuzz(func(t *testing.T, stream []byte, split int) {
		whole := newModeTracker()
		_, _ = whole.Write(stream)

		byByte := newModeTracker()
		for i := range stream {
			_, _ = byByte.Write(stream[i : i+1])
		}
		if byByte.st != whole.st {
			t.Fatalf("byte-at-a-time state %+v != whole-write state %+v", byByte.st, whole.st)
		}

		if split < 0 {
			split = -split
		}
		if len(stream) > 0 {
			split %= len(stream) + 1
		} else {
			split = 0
		}
		replay := stream[split:]
		prefix := whole.Restore(replay)
		late := newModeTracker()
		_, _ = late.Write(prefix)
		_, _ = late.Write(replay)
		if late.st != whole.st {
			t.Fatalf("split %d: prefix %q + replay leaves %+v, full stream leaves %+v", split, prefix, late.st, whole.st)
		}
	})
}

func BenchmarkModeTracker(b *testing.B) {
	var frame bytes.Buffer
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&frame, "\x1b[2K\x1b[1A\x1b[38;2;120;120;120m│ line %02d some text with unicode → ✓ and more text\x1b[0m\n", i)
	}
	frame.WriteString("\x1b[?2004l pasted \x1b[?2004h\x1b]0;title\x07")
	chunk := frame.Bytes()
	m := newModeTracker()
	b.SetBytes(int64(len(chunk)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = m.Write(chunk)
	}
}
