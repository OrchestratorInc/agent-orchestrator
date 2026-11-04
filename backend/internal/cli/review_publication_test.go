package cli

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestReviewPublicationBatchWirePreservesRefsAndStatus(t *testing.T) {
	const input = `{"reviews":[{"runId":"gl","verdict":"approved","publication":{"provider":"gitlab","status":"unconfirmed","prUrl":"https://gitlab.example/g/r/-/merge_requests/39","targetSha":"head","refs":[{"kind":"draft_note","id":"42"}],"error":"timeout after POST"}},{"runId":"gh","verdict":"approved","githubReviewId":"123"}]}`
	cmd := &cobra.Command{}
	cmd.SetIn(strings.NewReader(input))

	items, err := readReviewItems(cmd, "-")

	if err != nil {
		t.Fatalf("read review items: %v", err)
	}
	if len(items) != 2 || items[0].Publication == nil {
		t.Fatalf("review items = %+v, want two reviews with first receipt", items)
	}
	if items[0].Publication.Status != "unconfirmed" || items[0].GithubReviewID != "" {
		t.Errorf("unconfirmed GitLab result = %+v", items[0])
	}
	encoded, err := json.Marshal(submitReviewRequest{Reviews: items})
	if err != nil {
		t.Fatalf("encode submit request: %v", err)
	}
	var want, got any
	if err := json.Unmarshal([]byte(input), &want); err != nil {
		t.Fatalf("decode expected request: %v", err)
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("decode encoded request: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("encoded request = %s, want %s", encoded, input)
	}

	var response reviewRunResponse
	if err := json.Unmarshal([]byte(`{"reviews":[{"id":"gl","publication":{"provider":"gitlab","status":"published","prUrl":"https://gitlab.example/g/r/-/merge_requests/39","targetSha":"head","refs":[{"kind":"note","id":"101"}]}}]}`), &response); err != nil {
		t.Fatalf("decode review response: %v", err)
	}
	if len(response.Reviews) != 1 || response.Reviews[0].Publication == nil {
		t.Fatalf("review response = %+v, want one publication receipt", response)
	}
	refs := response.Reviews[0].Publication.Refs
	if len(refs) != 1 || refs[0].ID != "101" {
		t.Errorf("response refs = %+v, want note 101", refs)
	}
}
