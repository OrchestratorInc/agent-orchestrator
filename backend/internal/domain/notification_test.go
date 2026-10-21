package domain

import (
	"testing"
	"time"
)

func TestNotificationOrphanWorkersTypeAndValidation(t *testing.T) {
	typ := NotificationOrphanWorkers
	if !typ.Valid() {
		t.Fatal("orphan_workers must be a valid notification type")
	}
	if typ.NeedsResolution() {
		t.Fatal("orphan_workers must not join the resolve machinery: its lifecycle is the user's decision, not a durable fact")
	}
	rec := NotificationRecord{
		SessionID: "dead-1",
		Type:      typ,
		Title:     "Orphaned worker processes found",
		Status:    NotificationUnread,
		CreatedAt: time.Now(),
	}
	if err := rec.Validate(); err != nil {
		t.Fatalf("anchored batch rejected: %v", err)
	}
	other2 := rec
	other2.Type = ""
	if err := other2.Validate(); err == nil {
		t.Fatal("invalid type must be rejected")
	}
	other3 := rec
	other3.Type = NotificationReadyToMerge
	other3.SessionID = ""
	if err := other3.Validate(); err == nil {
		t.Fatal("session-less rows are reserved for the orphan batch; other kinds still require a session")
	}
}
