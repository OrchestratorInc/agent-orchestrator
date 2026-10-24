package session

import "testing"

func TestClaimOutputReconcileOncePerRevision(t *testing.T) {
	var s Service
	if !s.claimOutputReconcile("a", 3) {
		t.Fatal("the first read of revision 3 must claim")
	}
	if s.claimOutputReconcile("a", 3) || s.claimOutputReconcile("a", 2) {
		t.Fatal("a read of the same or an older revision must not claim again")
	}
	if !s.claimOutputReconcile("a", 4) {
		t.Fatal("a newer revision is a new mismatch and must claim")
	}
	if !s.claimOutputReconcile("b", 1) {
		t.Fatal("claims are per session")
	}
}
