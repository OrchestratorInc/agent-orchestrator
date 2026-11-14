package telemetry

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// emitAll pushes bare named events through sink; recordingSink is shared with
// ratelimit_test.go.
func emitAll(sink ports.EventSink, names ...string) {
	for _, name := range names {
		sink.Emit(context.Background(), ports.TelemetryEvent{Name: name})
	}
}

func forwardedNames(rec *recordingSink) []string {
	names := make([]string, 0, len(rec.events))
	for _, ev := range rec.events {
		names = append(names, ev.Name)
	}
	return names
}

func TestDenylistSinkPassesEverythingWhenUnconfigured(t *testing.T) {
	next := &recordingSink{}
	sink := NewDenylistSink(next, nil)
	if sink != ports.EventSink(next) {
		t.Fatal("empty denylist should return the wrapped sink unchanged")
	}
	emitAll(sink, "ao.app.active", "ao.sessions.spawned")
	if len(next.events) != 2 {
		t.Fatalf("names = %v, want both events forwarded", forwardedNames(next))
	}
}

func TestDenylistSinkDropsNamedEvents(t *testing.T) {
	next := &recordingSink{}
	sink := NewDenylistSink(next, []string{"ao.sessions.spawned"})
	emitAll(sink, "ao.app.active", "ao.sessions.spawned", "ao.http.5xx")
	if len(next.events) != 2 || forwardedNames(next)[0] != "ao.app.active" || forwardedNames(next)[1] != "ao.http.5xx" {
		t.Fatalf("names = %v, want the denied event dropped", forwardedNames(next))
	}
}

func TestDenylistSinkDropsRenamedEvents(t *testing.T) {
	next := &recordingSink{}
	sink := NewDenylistSink(next, []string{"ao.sessions.ended", "ao.prs.closed"})
	emitAll(sink, "ao.sessions.ended", "ao.prs.closed", "ao.sessions.spawned")
	if got := forwardedNames(next); len(got) != 1 || got[0] != "ao.sessions.spawned" {
		t.Fatalf("forwarded = %v", got)
	}
}

// A kill switch configured before the event rename names the old stream. It
// must still silence the renamed one, or the upgrade quietly re-enables it.
func TestDenylistSinkMatchesCurrentAndLegacyNames(t *testing.T) {
	for _, denied := range []string{"ao.cli.active", "ao.v2.app.active", "ao.session.*"} {
		next := &recordingSink{}
		sink := NewDenylistSink(next, []string{denied})
		event := "ao.cli.active"
		if denied == "ao.session.*" {
			event = "ao.sessions.spawned"
		}
		emitAll(sink, event)
		if len(next.events) != 0 {
			t.Fatalf("denylist %q forwarded %v, want dropped", denied, forwardedNames(next))
		}
	}
}

func TestDenylistSinkSupportsPrefixWildcardAndIsCaseInsensitive(t *testing.T) {
	next := &recordingSink{}
	sink := NewDenylistSink(next, []string{"  AO.RENDERER.*  "})
	emitAll(sink, "ao.renderer.route_viewed", "ao.renderer.api_error", "ao.sessions.spawned")
	if len(next.events) != 1 || forwardedNames(next)[0] != "ao.sessions.spawned" {
		t.Fatalf("names = %v, want only the non-renderer event forwarded", forwardedNames(next))
	}
}

// A denylist of only blank or bare "*" entries must not silence everything by
// accident, and must not add a pointless wrapper either.
// ao.app.active is still a current name (the desktop heartbeat); silencing
// it must not also silence CLI activity.
func TestDesktopActiveDenyDoesNotSilenceCLIActive(t *testing.T) {
	if EventDenied([]string{"ao.app.active"}, "ao.cli.active") {
		t.Fatal("ao.app.active entry silenced ao.cli.active")
	}
}

func TestEventDeniedHonorsLegacyNames(t *testing.T) {
	if !EventDenied([]string{"ao.session.spawned"}, "ao.sessions.spawned") {
		t.Fatal("legacy ao.session.spawned entry must deny ao.sessions.spawned")
	}
	if EventDenied(nil, "ao.sessions.spawned") || EventDenied([]string{"ao.http.5xx"}, "ao.sessions.spawned") {
		t.Fatal("unrelated entries must not deny")
	}
}

func TestDenylistSinkIgnoresEmptyEntries(t *testing.T) {
	next := &recordingSink{}
	sink := NewDenylistSink(next, []string{"", "   ", "*"})
	if sink != ports.EventSink(next) {
		t.Fatal("blank entries should leave the sink unwrapped")
	}
	emitAll(sink, "ao.app.active")
	if len(next.events) != 1 {
		t.Fatalf("names = %v, want the event forwarded", forwardedNames(next))
	}
}
