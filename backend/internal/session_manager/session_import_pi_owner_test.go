package sessionmanager

import "testing"

func TestIdentifiesPi(t *testing.T) {
	for name, tc := range map[string]struct {
		psLine string
		want   bool
	}{
		"native pi executable":            {"/opt/homebrew/bin/pi /opt/homebrew/bin/pi --session abc", true},
		"native pi-acp executable":        {"/Users/u/.bun/bin/pi-acp /Users/u/.bun/bin/pi-acp", true},
		"node running pi-acp package":     {"/usr/bin/node node /Users/u/.npm/node_modules/@victor-software-house/pi-acp/dist/index.mjs", true},
		"bun running pi-acp package":      {"/Users/u/.bun/bin/bun bun /Users/u/.bun/install/global/node_modules/@victor-software-house/pi-acp/dist/index.mjs", true},
		"node running pi-coding-agent":    {"/usr/bin/node node /Users/u/.npm/node_modules/@earendil-works/pi-coding-agent/dist/index.js", true},
		"node running pi bin shim":        {"/usr/bin/node node /Users/u/project/node_modules/.bin/pi chat", true},
		"plain node rejected":             {"/usr/bin/node node /Users/u/app/server.js", false},
		"plain bun rejected":              {"/Users/u/.bun/bin/bun bun /Users/u/app/server.ts", false},
		"pip is not pi":                   {"/usr/bin/pip pip install requests", false},
		"api-server substring is not pi":  {"/usr/local/bin/api-server api-server --port 8080", false},
		"spin substring is not pi":        {"/usr/local/bin/spin spin up", false},
		"python with pi in path rejected": {"/usr/bin/python3 python3 /Users/u/pipeline/run.py", false},
		"empty line":                      {"", false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := identifiesPi(tc.psLine); got != tc.want {
				t.Fatalf("identifiesPi(%q) = %v, want %v", tc.psLine, got, tc.want)
			}
		})
	}
}
