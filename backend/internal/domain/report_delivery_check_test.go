package domain

import (
	"strings"
	"testing"
)

// Issue #6931: a report is acknowledged only when its envelope arrives whole.
// These tests pin what "whole" means.

const testDeliveryBatchID = "report-batch:rpt_1"

const testDeliveryBody = "Reports since your previous turn:\n\n[done] [worker](ao://sessions/p/w)\nfinished\nartifact: `/tmp/a.png`"

func TestReportDeliveryAcceptedBehindOtherText(t *testing.T) {
	// The user's half-typed draft was in the composer when AO pasted.
	prompt := "is that a reasonable amount of work for " + WrapReportDelivery(testDeliveryBatchID, testDeliveryBody)
	if got, ok := ReportDeliveryID(prompt); !ok || got != testDeliveryBatchID {
		t.Fatalf("ReportDeliveryID() = %q, %v; want %q, true", got, ok, testDeliveryBatchID)
	}
}

func TestReportDeliveryRejectedWhenBodyChanged(t *testing.T) {
	whole := WrapReportDelivery(testDeliveryBatchID, testDeliveryBody)
	for name, broken := range map[string]string{
		"text lost":         strings.Replace(whole, "finished\n", "", 1),
		"image placeholder": strings.Replace(whole, "finished", "[Image #1] finished", 1),
		"text reordered":    strings.Replace(whole, "[done] [worker](ao://sessions/p/w)\nfinished", "finished\n[done] [worker](ao://sessions/p/w)", 1),
		"tail only":         whole[len(whole)/2:],
		"no closing tag":    strings.TrimSuffix(whole, "</ao-report-delivery>"),
	} {
		if got, ok := ReportDeliveryID(broken); ok {
			t.Errorf("%s: ReportDeliveryID() = %q, true; want rejected", name, got)
		}
	}
}

func TestReportDeliveryCheckIgnoresWhitespace(t *testing.T) {
	// Terminals may drop blank lines or change line endings without losing content.
	whole := WrapReportDelivery(testDeliveryBatchID, testDeliveryBody)
	changed := strings.ReplaceAll(whole, "\n\n", "\n")
	changed = strings.ReplaceAll(changed, "finished\n", "finished\r\n")
	if got, ok := ReportDeliveryID(changed); !ok || got != testDeliveryBatchID {
		t.Fatalf("ReportDeliveryID() = %q, %v; want %q, true", got, ok, testDeliveryBatchID)
	}
}

func TestLegacyReportEnvelopeStillAcceptedOnlyAtStart(t *testing.T) {
	// Envelopes written before the checksum existed keep the old rule.
	legacy := "<ao-report-delivery id=\"" + testDeliveryBatchID + "\">\n" + testDeliveryBody + "\n</ao-report-delivery>"
	if got, ok := ReportDeliveryID(legacy); !ok || got != testDeliveryBatchID {
		t.Fatalf("legacy at start = %q, %v; want accepted", got, ok)
	}
	if got, ok := ReportDeliveryID("draft " + legacy); ok {
		t.Fatalf("legacy behind text = %q, true; want rejected (no checksum to prove it is whole)", got)
	}
}

func TestSessionDeliveryUsesTheSameCheck(t *testing.T) {
	whole := WrapSessionDelivery("session-send:1", "run the test pass.")
	if got, ok := CoordinationDeliveryID("draft " + whole); !ok || got != "session-send:1" {
		t.Fatalf("intact session delivery = %q, %v; want accepted", got, ok)
	}
	// Seen in production: only the tail of a session delivery arrived.
	if got, ok := CoordinationDeliveryID("o test pass.\n</ao-session-delivery>"); ok {
		t.Fatalf("tail fragment = %q, true; want rejected", got)
	}
}

func TestReportDeliveryEnvelopeMustEndThePrompt(t *testing.T) {
	whole := WrapReportDelivery(testDeliveryBatchID, testDeliveryBody)
	// A human prompt that quotes an earlier report is still a human prompt.
	if got, ok := ReportDeliveryID("what does this mean? " + whole + " please explain"); ok {
		t.Fatalf("quoted envelope = %q, true; want rejected", got)
	}
}

func TestReportDeliveryAcceptsTheLastOfTwoCopies(t *testing.T) {
	// An earlier copy was left unsent in the composer and a retry pasted a
	// second copy behind it.
	whole := WrapReportDelivery(testDeliveryBatchID, testDeliveryBody)
	if got, ok := ReportDeliveryID(whole + "\n" + whole); !ok || got != testDeliveryBatchID {
		t.Fatalf("two copies = %q, %v; want accepted", got, ok)
	}
}

func TestReportDeliveryBodyMayContainTags(t *testing.T) {
	// A worker reporting on AO itself may quote AO's own markup.
	body := "fixed the parser for <ao-report-delivery id=\"x\"> and </ao-report-delivery>"
	if got, ok := ReportDeliveryID(WrapReportDelivery(testDeliveryBatchID, body)); !ok || got != testDeliveryBatchID {
		t.Fatalf("body with tags = %q, %v; want accepted", got, ok)
	}
}

func TestRejectedLegacyEnvelopeReturnsNoID(t *testing.T) {
	legacy := "<ao-report-delivery id=\"" + testDeliveryBatchID + "\">\nbody\n</ao-report-delivery>"
	if got, ok := ReportDeliveryID("draft " + legacy); ok || got != "" {
		t.Fatalf("ReportDeliveryID() = %q, %v; want \"\", false", got, ok)
	}
}

// legacyDeliveryID is the parser hooks used before the check existed. Agents
// keep calling the hook binary they were started with, so an older hook can
// meet a newer daemon's envelope.
func legacyDeliveryID(message, prefix string) (string, bool) {
	message = strings.TrimSpace(message)
	if !strings.HasPrefix(message, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(message, prefix)
	end := strings.Index(rest, `">`)
	if end <= 0 || end > 128 {
		return "", false
	}
	id := rest[:end]
	if !validDeliveryID(id) {
		return "", false
	}
	return id, true
}

func TestOlderHooksStillReadTheCheckedEnvelope(t *testing.T) {
	for prefix, wrapped := range map[string]string{
		`<ao-report-delivery id="`:  WrapReportDelivery(testDeliveryBatchID, testDeliveryBody),
		`<ao-session-delivery id="`: WrapSessionDelivery("session-send:1", "run the test pass."),
	} {
		if _, ok := legacyDeliveryID(wrapped, prefix); !ok {
			t.Fatalf("an older hook cannot read %q", wrapped[:60])
		}
	}
}

func TestCheckedEnvelopeWithoutItsEndIsRejected(t *testing.T) {
	// The check line marks the envelope as checked, so losing the end of the
	// text is caught even when the envelope is at the start.
	whole := WrapReportDelivery(testDeliveryBatchID, testDeliveryBody)
	if got, ok := ReportDeliveryID(whole[:len(whole)-40]); ok {
		t.Fatalf("truncated envelope = %q, true; want rejected", got)
	}
}
