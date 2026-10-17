package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A deletion reads the project's Cleanup as it is now: an edit after the
// session started is honored, a project without Cleanup reads as an empty
// list, and an archived project is not found (the provider then runs the
// commands the worker installed).
func TestProjectCleanupCommandsReadsTheCurrentProjectConfig(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	setCleanup := func(config string) {
		t.Helper()
		if _, err := admin.Exec(ctx, `UPDATE ao_projects SET config = $1::jsonb
			WHERE id = (SELECT project_id FROM ao_sessions WHERE id = $2)`, config, fixture.sessionID); err != nil {
			t.Fatal(err)
		}
	}

	setCleanup(`{"preRemove": ["docker compose down", " "], "postCreate": ["make up"]}`)
	commands, err := store.ProjectCleanupCommands(ctx, fixture.orgID, fixture.sessionID)
	if err != nil || strings.Join(commands, "|") != "docker compose down" {
		t.Fatalf("Cleanup = %v (%v), want the project's current commands", commands, err)
	}

	setCleanup(`{"postCreate": ["make up"]}`)
	if commands, err := store.ProjectCleanupCommands(ctx, fixture.orgID, fixture.sessionID); err != nil || len(commands) != 0 {
		t.Fatalf("Cleanup without preRemove = %v (%v), want an empty list", commands, err)
	}

	if _, err := admin.Exec(ctx, `UPDATE ao_projects SET archived_at = now()
		WHERE id = (SELECT project_id FROM ao_sessions WHERE id = $1)`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	// Removing a project deletes its sandboxes; they run its last Cleanup.
	setCleanup(`{"preRemove": ["docker compose down"]}`)
	if commands, err := store.ProjectCleanupCommands(ctx, fixture.orgID, fixture.sessionID); err != nil || strings.Join(commands, "|") != "docker compose down" {
		t.Fatalf("archived project Cleanup = %v (%v), want its last commands", commands, err)
	}
	if _, err := store.ProjectCleanupCommands(ctx, fixture.orgID, "00000000-0000-4000-8000-000000000999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown session lookup err = %v, want ErrNotFound", err)
	}
}
