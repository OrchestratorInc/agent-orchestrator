package controllers_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

func TestReviewsSubmitPublicationWire(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "single", body: `{"runId":"gl","verdict":"approved","publication":{"provider":"gitlab","status":"published","prUrl":"https://gitlab.example/g/r/-/merge_requests/39","targetSha":"head","refs":[{"kind":"note","id":"101"}]}}`},
		{name: "batch", body: `{"reviews":[{"runId":"gl","verdict":"approved","publication":{"provider":"gitlab","status":"published","prUrl":"https://gitlab.example/g/r/-/merge_requests/39","targetSha":"head","refs":[{"kind":"note","id":"101"}]}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeReviewService{}
			srv := newReviewTestServer(t, svc)

			response, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/worker/reviews/submit", tc.body)

			if status != http.StatusOK {
				t.Fatalf("submit status = %d, body=%s", status, response)
			}
			if len(svc.submitted) != 1 || svc.submitted[0].Publication == nil {
				t.Fatalf("submitted reviews = %+v, want one publication receipt", svc.submitted)
			}
			publication := svc.submitted[0].Publication
			if len(publication.Refs) != 1 || publication.Refs[0].ID != "101" {
				t.Errorf("publication refs = %+v, want note 101", publication.Refs)
			}
			if svc.submitted[0].GithubReviewID != "" {
				t.Errorf("GitLab receipt has GitHub review ID %q", svc.submitted[0].GithubReviewID)
			}
			var result controllers.ReviewRunResponse
			if err := json.Unmarshal(response, &result); err != nil {
				t.Fatalf("decode submit response: %v", err)
			}
			if !reflect.DeepEqual(publication, result.Review.Publication) {
				t.Errorf("response publication = %+v, want %+v", result.Review.Publication, publication)
			}
		})
	}
}
