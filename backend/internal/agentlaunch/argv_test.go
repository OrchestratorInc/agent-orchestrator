package agentlaunch

import (
	"reflect"
	"testing"
)

func TestSplitEnvAssignments(t *testing.T) {
	tests := []struct {
		name            string
		argv            []string
		wantAssignments []string
		wantCommand     []string
		wantOK          bool
	}{
		{
			name:        "no env prefix returns argv unchanged",
			argv:        []string{"opencode", "--agent", "ao-x"},
			wantCommand: []string{"opencode", "--agent", "ao-x"},
			wantOK:      true,
		},
		{
			name:            "one assignment",
			argv:            []string{"env", "OPENCODE_CONFIG=C:/cfg.json", "opencode", "--agent", "ao-x"},
			wantAssignments: []string{"OPENCODE_CONFIG=C:/cfg.json"},
			wantCommand:     []string{"opencode", "--agent", "ao-x"},
			wantOK:          true,
		},
		{
			name:            "multiple assignments preserve equals in values",
			argv:            []string{"env", "A=1", "DATA=left=right", "opencode"},
			wantAssignments: []string{"A=1", "DATA=left=right"},
			wantCommand:     []string{"opencode"},
			wantOK:          true,
		},
		{
			name:            "env with no command reports invalid argv",
			argv:            []string{"env", "A=1", "B=2"},
			wantAssignments: []string{"A=1", "B=2"},
			wantOK:          false,
		},
		{
			name:   "empty argv reports invalid argv",
			wantOK: false,
		},
		{
			name:        "a binary merely starting with env is not treated as a prefix",
			argv:        []string{"envoy", "--config", "x"},
			wantCommand: []string{"envoy", "--config", "x"},
			wantOK:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAssignments, gotCommand, gotOK := SplitEnvAssignments(tt.argv)
			if !reflect.DeepEqual(gotAssignments, tt.wantAssignments) {
				t.Errorf("assignments = %#v, want %#v", gotAssignments, tt.wantAssignments)
			}
			if !reflect.DeepEqual(gotCommand, tt.wantCommand) {
				t.Errorf("command = %#v, want %#v", gotCommand, tt.wantCommand)
			}
			if gotOK != tt.wantOK {
				t.Errorf("ok = %v, want %v", gotOK, tt.wantOK)
			}
		})
	}
}
