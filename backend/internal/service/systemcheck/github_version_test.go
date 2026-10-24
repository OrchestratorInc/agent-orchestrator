package systemcheck

import (
	"context"
	"errors"
	"testing"
)

func TestGitHubVersionUpgradeRequirement(t *testing.T) {
	for _, tc := range []struct {
		banner   string
		version  string
		old      bool
		probeErr error
	}{
		{banner: "gh version 1.14.0 (2021-08-24)", version: "1.14.0", old: true},
		{banner: "gh version 2.3.0 (2021-11-16)", version: "2.3.0", old: true},
		{banner: "gh version 2.4.0-rc.1\n", version: "2.4.0-rc.1", old: true},
		{banner: "gh version 2.4.0 (2021-12-21)\nhttps://github.com/cli/cli/releases", version: "2.4.0"},
		{banner: "gh version v2.4.0+distribution", version: "2.4.0+distribution"},
		{banner: "gh version 2.10.0", version: "2.10.0"},
		{banner: "gh version 3.0.0", version: "3.0.0"},
		{banner: "custom build"},
		{banner: "gh version 2.3"},
		{banner: "gh version 2.3.0garbage"},
		{banner: "gh version 99999999999999999999999999999.0.0"},
		{banner: "gh version 1.0.0", probeErr: errors.New("failed probe")},
	} {
		t.Run(tc.banner, func(t *testing.T) {
			runner := &fakeCommandRunner{version: tc.banner, versionErr: tc.probeErr, stdout: `{"hosts":{"github.com":[{"active":true,"state":"success"}]}}`}
			svc := NewWithCommandRunner(nil, executableFinderFunc(lookPathFound(map[string]string{"gh": "/usr/bin/gh"})), runner)
			got, err := svc.CheckGitHubAuth(context.Background())
			if err != nil || got.Version != tc.version || got.UpdateRequired != tc.old || got.Required || got.Satisfied == tc.old {
				t.Fatalf("requirement=%+v, err=%v", got, err)
			}
			if tc.old && len(runner.argvLog) != 0 {
				t.Fatalf("old CLI must be updated before authentication: %v", runner.argvLog)
			}
			if runner.versionCalls != 1 {
				t.Fatalf("version calls=%d", runner.versionCalls)
			}
		})
	}
}
