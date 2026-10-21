package daemon

import (
	"os"
	"testing"
)

// TestStopTeardownDisabledByDefault pins the owner's interim call (issue #35):
// the just-merged `ao stop` teardown ships dark — without an explicit opt-in
// env the daemon keeps the pre-#112 behavior and only stops itself.
func TestStopTeardownDisabledByDefault(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "unset keeps teardown off", value: "", want: false},
		{name: "0 stays off", value: "0", want: false},
		{name: "garbage stays off", value: "yes please", want: false},
		{name: "1 opts in", value: "1", want: true},
		{name: "true opts in", value: "true", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.value == "" {
				// The suite never sets the flag elsewhere; drop any ambient value
				// so the unset case really reads an unset environment.
				t.Cleanup(func() { _ = os.Unsetenv(stopTeardownEnvFlag) })
				_ = os.Unsetenv(stopTeardownEnvFlag)
			} else {
				t.Setenv(stopTeardownEnvFlag, tc.value)
			}
			if got := stopTeardownEnabled(); got != tc.want {
				t.Fatalf("stopTeardownEnabled(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}
