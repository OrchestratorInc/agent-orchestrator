package httpd

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// ReviewSessionHeader marks a request made from inside a reviewer pane. The
// AO CLI sets it whenever AO_REVIEW_SESSION_ID is in its environment, which the
// review launcher sets for every reviewer harness.
const ReviewSessionHeader = "X-AO-Review-Session"

// reviewerAllowedWrites are the only mutating routes a reviewer needs: record
// its verdict, report its own hook activity, and CLI telemetry.
var reviewerAllowedWrites = []*regexp.Regexp{
	regexp.MustCompile(`^/api/v1/sessions/[^/]+/reviews/submit$`),
	regexp.MustCompile(`^/api/v1/reviews/[^/]+/activity$`),
	regexp.MustCompile(`^/internal/telemetry/cli-(?:invoked|usage-error)$`),
}

// reviewerGateMiddleware keeps reviewers read-and-report only at the daemon,
// whatever harness they run in (#6842). A reviewer reaches the daemon through
// `ao`, so without this gate `ao spawn`, `ao send`, `ao session kill` or
// `ao stop` from a reviewer pane would act with the user's full authority.
// It is a guard against a misled or misbehaving reviewer, not a hard boundary:
// a caller that drops the header is indistinguishable from the user.
func reviewerGateMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.Header.Get(ReviewSessionHeader)) == "" || reviewerRequestAllowed(r) {
			next.ServeHTTP(w, r)
			return
		}
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "REVIEWER_ACTION_FORBIDDEN",
			"AO reviewers may read and submit their review only; they cannot spawn, message, stop or manage sessions", nil)
	})
}

func reviewerRequestAllowed(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		// Terminal streams accept keystrokes into other sessions' panes.
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	case http.MethodPost:
		for _, route := range reviewerAllowedWrites {
			if route.MatchString(r.URL.Path) {
				return true
			}
		}
	}
	return false
}
