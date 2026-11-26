package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/approval"
	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// vaultDescribeAction asks the panel which saved credential a fill would use,
// without any secret. It is internal: agents cannot call it.
const vaultDescribeAction = "vault-describe"

// credentialActions type vault secrets into the page. The browser runtime that
// shows the page resolves the secret itself; only an item id travels here.
var credentialActions = map[string]struct{}{"fill-secret": {}, "login": {}}

type vaultDescription struct {
	Origin     string        `json:"origin"`
	PageURL    string        `json:"pageUrl"`
	PageTitle  string        `json:"pageTitle"`
	Item       *vaultItemRef `json:"item"`
	Candidates []interface{} `json:"candidates"`
}

type vaultItemRef struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Username string `json:"username"`
}

// credentialPreflight scopes a credential action to the session's project and
// asks the user before every first use per origin. It returns the arguments to
// dispatch, or a final result (a candidate list) that needs no fill.
func (s *Service) credentialPreflight(
	ctx context.Context,
	session domain.Session,
	rt runtime,
	action string,
	args map[string]interface{},
) (map[string]interface{}, *browserruntime.Result, error) {
	if s.approvals == nil {
		return nil, nil, apierr.Invalid("BROWSER_VAULT_UNAVAILABLE", "Saved credentials need the approval prompt, which is unavailable", nil)
	}
	// The project and the approved origin come from the session record and
	// the user's decision, never from the caller. A standalone session has no
	// project; the panel then allows only items scoped to all projects.
	stamped := make(map[string]interface{}, len(args)+2)
	for key, value := range args {
		if key != "approvedOrigin" {
			stamped[key] = value
		}
	}
	stamped["projectId"] = string(session.ProjectID)

	describeArgs := make(map[string]interface{}, len(stamped)+1)
	for key, value := range stamped {
		describeArgs[key] = value
	}
	describeArgs["action"] = action
	described, err := rt.Execute(ctx, session.ID, vaultDescribeAction, describeArgs)
	if err != nil {
		return nil, nil, err
	}
	var description vaultDescription
	raw, err := json.Marshal(described.Value)
	if err != nil || json.Unmarshal(raw, &description) != nil {
		return nil, nil, apierr.Invalid("BROWSER_VAULT_UNAVAILABLE", "The browser could not describe the saved credential", nil)
	}
	if description.Item == nil {
		if len(description.Candidates) > 0 {
			return nil, &browserruntime.Result{RequestID: described.RequestID, Value: map[string]interface{}{
				"filled": []interface{}{}, "origin": description.Origin, "candidates": description.Candidates,
			}}, nil
		}
		return nil, nil, apierr.Invalid("BROWSER_VAULT_NO_MATCH", "No saved credential matches this page", nil)
	}

	summary := fmt.Sprintf("Sign in to %s as %s with saved %q", description.Origin, description.Item.Username, description.Item.Label)
	if field, _ := stamped["field"].(string); action == "fill-secret" && field != "" {
		summary = fmt.Sprintf("Fill the saved %s for %s (%q) on %s", field, description.Item.Username, description.Item.Label, description.Origin)
	}
	// Credentials always need the user's yes for a new origin, whatever the
	// session's approval mode. The rule key is one saved login on one exact
	// origin: "always allow" for alice never covers another port, nor another
	// login saved later for the same site (an admin account, say).
	site := savedLoginSite(description.Origin, description.Item)
	reason := "An agent wants to sign in with a saved password. Orchestrator types it; the agent never sees it."
	// Plain HTTP on this computer can be served by any local process,
	// including one the agent starts. Never remember "always allow" there:
	// ask every time, and say why.
	loopback := loopbackHTTP(description.Origin)
	var forgetRule func() error
	if loopback {
		rules, ok := s.approvals.(interface {
			RemoveSiteRule(site, category string) error
		})
		if !ok {
			return nil, nil, apierr.Invalid("BROWSER_VAULT_UNAVAILABLE", "This approval prompt cannot ask every time for a local site", nil)
		}
		forgetRule = func() error { return rules.RemoveSiteRule(site, approval.CategoryCredential) }
		if err := forgetRule(); err != nil {
			return nil, nil, err
		}
		reason = "This site is plain HTTP on this computer, which any local program (including one an agent starts) can serve, so Orchestrator asks every time. The agent never sees the password."
	}
	outcome, err := s.approvals.Request(ctx, approval.Request{
		Kind:      approval.KindSavedLogin,
		SessionID: string(session.ID),
		Site:      site,
		URL:       description.PageURL,
		PageTitle: description.PageTitle,
		Action:    summary,
		Category:  approval.CategoryCredential,
		Reason:    reason,
		Timeout:   s.approvalTimeout,
	})
	if err != nil {
		return nil, nil, err
	}
	if forgetRule != nil && outcome.Decision == approval.DecisionAllowSite {
		if err := forgetRule(); err != nil {
			return nil, nil, err
		}
	}
	if !outcome.Allowed() {
		return nil, nil, approvalRefusal(outcome.Decision, summary, s.approvalTimeout)
	}
	stamped["approvedOrigin"] = description.Origin
	stamped["itemId"] = description.Item.ID
	return stamped, nil, nil
}

// savedLoginSite is the approval rule key for one saved login on one origin.
// It reads well in the prompt and the always-allowed list, and the short item
// id keeps two logins with the same name apart.
func savedLoginSite(origin string, item *vaultItemRef) string {
	name := item.Username
	if name == "" {
		name = item.Label
	}
	id := item.ID
	if len(id) > 8 {
		id = id[:8]
	}
	return fmt.Sprintf("%s · %s (%s)", origin, name, id)
}

// loopbackHTTP reports a plain-HTTP origin on this computer (localhost,
// *.localhost, 127.0.0.0/8, [::1]): one any local process can serve.
func loopbackHTTP(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
