package domain

import "testing"

func TestNotificationTypeBrowserApproval(t *testing.T) {
	if NotificationBrowserApproval != "browser_approval" {
		t.Fatalf("type = %q", NotificationBrowserApproval)
	}
	if !NotificationBrowserApproval.Valid() {
		t.Fatal("browser_approval must be a valid notification type")
	}
	// A waiting approval is an open issue until it is decided or ends.
	if !NotificationBrowserApproval.NeedsResolution() {
		t.Fatal("browser_approval must stay unresolved until the approval ends")
	}
}
