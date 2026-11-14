package providerkeys

import "testing"

func TestAlreadyUsersSeparatesTheUsersValueFromAOs(t *testing.T) {
	for _, tc := range []struct {
		current, value, previous string
		want                     bool
	}{
		{"same", "same", "", true},
		{"same", "same", "old", true},
		{"same", "same", "same", false},
		{"", "same", "", false},
		{"other", "same", "", false},
	} {
		if got := alreadyUsers(tc.current, tc.value, tc.previous); got != tc.want {
			t.Errorf("alreadyUsers(%q, %q, %q) = %v", tc.current, tc.value, tc.previous, got)
		}
	}
}
