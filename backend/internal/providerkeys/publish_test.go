package providerkeys

import "testing"

func TestAlreadyUsersSeparatesTheUsersValueFromAOs(t *testing.T) {
	for _, tc := range []struct {
		current, value, previous string
		want                     bool
	}{
		{"same", "same", "", true},      // the user had it before AO shared
		{"same", "same", "old", true},   // AO's copy was replaced by the user
		{"same", "same", "same", false}, // AO's own copy
		{"", "same", "", false},
		{"other", "same", "", false},
	} {
		if got := alreadyUsers(tc.current, tc.value, tc.previous); got != tc.want {
			t.Errorf("alreadyUsers(%q, %q, %q) = %v", tc.current, tc.value, tc.previous, got)
		}
	}
}
