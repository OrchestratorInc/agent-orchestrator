package browser

import (
	"context"
	"encoding/json"
	"fmt"

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
	if session.ProjectID == "" {
		return nil, nil, apierr.Invalid("BROWSER_VAULT_UNAVAILABLE", "Saved credentials need a session that belongs to a project", nil)
	}
	if s.approvals == nil {
		return nil, nil, apierr.Invalid("BROWSER_VAULT_UNAVAILABLE", "Saved credentials need the approval prompt, which is unavailable", nil)
	}
	// The project and the approved origin come from the session record and
	// the user's decision, never from the caller.
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
	// session's approval mode. The site key is the exact origin, so a
	// "always allow" on http://localhost:4173 never covers another port.
	outcome, err := s.approvals.Request(ctx, approval.Request{
		Kind:      approval.KindCredentialUse,
		SessionID: string(session.ID),
		Site:      description.Origin,
		URL:       description.PageURL,
		PageTitle: description.PageTitle,
		Action:    summary,
		Category:  approval.CategoryCredential,
		Reason:    "An agent wants to sign in with a saved password. Orchestrator types it; the agent never sees it.",
		Timeout:   s.approvalTimeout,
	})
	if err != nil {
		return nil, nil, err
	}
	if !outcome.Allowed() {
		return nil, nil, approvalRefusal(outcome.Decision, summary, s.approvalTimeout)
	}
	stamped["approvedOrigin"] = description.Origin
	stamped["itemId"] = description.Item.ID
	return stamped, nil, nil
}
