package tmux

import "testing"

func TestCountWorkloadCommandsSeesShellCommandsButNotAgentHelpers(t *testing.T) {
	const supervisor = "100 1 /bin/zsh -i\n101 100 /opt/ao agent-process supervise --session sess-1 --launch launch-1 -- claude\n102 101 claude\n"
	for _, tt := range []struct {
		name  string
		table string
		want  int
	}{
		{name: "idle agent", table: supervisor, want: 0},
		{name: "mcp server helper", table: supervisor + "103 102 node /mcp/server.js\n104 102 uvx some-mcp\n", want: 0},
		{name: "tool command", table: supervisor + "103 102 /bin/bash -c npm test\n104 103 node jest\n", want: 2},
		{name: "dev server under a shell", table: supervisor + "103 102 /bin/zsh -c npm run dev\n104 103 node vite\n105 104 esbuild --service\n", want: 3},
		{name: "nested shells count once", table: supervisor + "103 102 sh -c make\n104 103 bash -c ./build.sh\n", want: 2},
		{name: "other launch is ignored", table: supervisor + "200 1 /opt/ao agent-process supervise --session sess-1 --launch launch-0 -- claude\n201 200 claude\n202 201 bash -c sleep 100\n", want: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			entries, err := parseProcessTable(tt.table)
			if err != nil {
				t.Fatal(err)
			}
			got, err := countWorkloadCommands(entries, 100, "sess-1", "launch-1")
			if err != nil || got != tt.want {
				t.Fatalf("commands = %d, %v; want %d", got, err, tt.want)
			}
		})
	}
}

func TestCountWorkloadCommandsRequiresTheExactLaunch(t *testing.T) {
	entries, err := parseProcessTable("100 1 /bin/zsh -i\n101 100 /opt/ao agent-process supervise --session sess-1 --launch launch-0 -- claude\n102 101 claude\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := countWorkloadCommands(entries, 100, "sess-1", "launch-1"); err == nil {
		t.Fatal("counted commands for a launch that is not running")
	}
}
