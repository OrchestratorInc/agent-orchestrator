package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReviewPublicationBatchWirePreservesRefsAndStatus(t *testing.T) {
	// The target URL is inert receipt data; no HTTP or environment access occurs.
	const input = `{"reviews":[{"runId":"gl","verdict":"approved","publication":{"provider":"gitlab","status":"unconfirmed","prUrl":"https://gitlab.example/g/r/-/merge_requests/39","targetSha":"head","refs":[{"kind":"draft_note","id":"42"}],"error":"timeout after POST"}},{"runId":"gh","verdict":"approved","githubReviewId":"123"}]}`
	cmd := &cobra.Command{}
	cmd.SetIn(strings.NewReader(input))

	items, err := readReviewItems(cmd, "-")

	require.NoError(t, err)
	require.Len(t, items, 2)
	require.NotNil(t, items[0].Publication)
	assert.Equal(t, "unconfirmed", items[0].Publication.Status)
	assert.Empty(t, items[0].GithubReviewID)
	encoded, err := json.Marshal(submitReviewRequest{Reviews: items})
	require.NoError(t, err)
	assert.JSONEq(t, input, string(encoded))

	var response reviewRunResponse
	require.NoError(t, json.Unmarshal([]byte(`{"reviews":[{"id":"gl","publication":{"provider":"gitlab","status":"published","prUrl":"https://gitlab.example/g/r/-/merge_requests/39","targetSha":"head","refs":[{"kind":"note","id":"101"}]}}]}`), &response))
	require.Len(t, response.Reviews, 1)
	require.NotNil(t, response.Reviews[0].Publication)
	assert.Equal(t, "101", response.Reviews[0].Publication.Refs[0].ID)
}
