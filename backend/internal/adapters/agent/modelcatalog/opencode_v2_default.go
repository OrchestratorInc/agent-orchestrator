package modelcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
)

// openCodeV2ResolvedDefault asks OpenCode 2 which model it runs for a session
// started in workingDir, or returns "" when it cannot say.
//
// AO cannot work this out from config files: OpenCode 2 layers config from
// directories above the launch worktree's git root, which discovery cannot see
// (see resolveOpenCodeModel). Without an answer the picker has no default and
// falls back to the first listed model, which is not evidence of what opencode
// will run. The CLI knows: its server reports the default model for a
// directory, across every config layer and its own selection rules.
//
// A failure of any kind returns "": the default stays unknown rather than
// guessed, and discovery of the model list is unaffected.
func openCodeV2ResolvedDefault(ctx context.Context, binary, workingDir string, env map[string]string) string {
	if err := ctx.Err(); err != nil {
		return ""
	}
	runCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	// --standalone starts a private server, so the answer does not depend on
	// whether a background service happens to be running. Stdout only: stderr
	// carries logs.
	output, err := modelCommand(runCtx, binary, []string{"api", "--standalone", "GET", "/api/model/default"}, workingDir, env).Output()
	if err != nil {
		return ""
	}
	return parseOpenCodeV2DefaultModel(output)
}

// parseOpenCodeV2DefaultModel reads the response of GET /api/model/default:
// {"location":{...},"data":{"modelID":"...","providerID":"..."}}, where data is
// null when opencode has no usable default.
func parseOpenCodeV2DefaultModel(raw []byte) string {
	start := bytes.IndexByte(raw, '{')
	if start < 0 {
		return ""
	}
	var response struct {
		Data *struct {
			ModelID    string `json:"modelID"`
			ProviderID string `json:"providerID"`
		} `json:"data"`
	}
	if err := json.NewDecoder(bytes.NewReader(raw[start:])).Decode(&response); err != nil || response.Data == nil {
		return ""
	}
	if strings.TrimSpace(response.Data.ModelID) == "" {
		return ""
	}
	return joinProviderModel(response.Data.ProviderID, response.Data.ModelID)
}
