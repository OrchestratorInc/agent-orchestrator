package domain

import "testing"

// Break caught: with a separate "no checks" CI state, a PR the provider calls
// mergeable would either never count as ready (the panel's wait) or count as
// ready while the provider still blocks it.
func TestReadyToMergeCIStates(t *testing.T) {
	ready := func(ci CIState, m Mergeability) bool {
		return MergeReadiness{CI: ci, Review: ReviewNone, Mergeability: m}.ReadyToMerge()
	}
	for _, tc := range []struct {
		name string
		ci   CIState
		m    Mergeability
		want bool
	}{
		{"passing and mergeable", CIPassing, MergeMergeable, true},
		{"no checks and mergeable", CINone, MergeMergeable, true},
		{"no checks but the provider blocks it", CINone, MergeBlocked, false},
		{"no checks but the merge state is unknown", CINone, MergeUnknown, false},
		{"no checks but conflicting", CINone, MergeConflicting, false},
		{"unknown stays a blocker", CIUnknown, MergeMergeable, false},
		{"empty stays a blocker", "", MergeMergeable, false},
		{"pending stays a blocker", CIPending, MergeMergeable, false},
		{"failing stays a blocker", CIFailing, MergeMergeable, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ready(tc.ci, tc.m); got != tc.want {
				t.Fatalf("ReadyToMerge(ci=%q, mergeability=%q) = %v, want %v", tc.ci, tc.m, got, tc.want)
			}
		})
	}
}
