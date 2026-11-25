package sessionmanager

import "testing"

func TestOpenHandsProcessRequiresItsEntrypoint(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    bool
	}{
		{"/usr/bin/python3 /venv/bin/openhands --resume native", true},
		{"python -m openhands_cli --resume native", true},
		{"python3 -u -m openhands_cli.entrypoint", true},
		{"python3 /venv/openhands_cli/__main__.py", true},
		{"python3 unrelated.py", false},
		{"python3 unrelated.py --label openhands_cli", false},
		{"python3 -m http.server", false},
		{"python3 -m openhands_cli_other", false},
		{"python-other /venv/bin/openhands", false},
		{"python3 -c 'import openhands_cli'", false},
		{"python3 /venv/openhands_cli_other/__main__.py", false},
		{"python3", false},
		{"python3 unrelated.py --label openhands", false},
		{"python3 /tmp/not-openhands", false},
	} {
		t.Run(tc.command, func(t *testing.T) {
			if got := openHandsProcessCommand(tc.command); got != tc.want {
				t.Fatalf("entrypoint match = %v, want %v", got, tc.want)
			}
		})
	}
}
