package access

import (
	"net/http"
	"slices"
)

// ProjectSource says where an action's target project comes from.
type ProjectSource int

// Where a call names the project it reaches.
const (
	// ProjectNone marks an action that reaches no project data.
	ProjectNone ProjectSource = iota
	// ProjectAll marks an action that reaches every project at once.
	ProjectAll
	// ProjectParam reads the project ID from a URL parameter.
	ProjectParam
	// ProjectSession reads a session ID from a URL parameter.
	ProjectSession
	// ProjectQuery reads the project ID from a query parameter.
	ProjectQuery
	// ProjectBody reads the project ID from a JSON body field.
	ProjectBody
	// ProjectBodySession reads a session ID from a JSON body field.
	ProjectBodySession
)

// Action is one thing a grant can reach, bound to a route the daemon already serves.
type Action struct {
	ID      string
	Group   Group
	Method  string
	Path    string
	Project ProjectSource
	// Key is the parameter, query or body field that Project reads.
	Key string
}

const (
	sessionPath = "/api/v1/sessions/{sessionId}"
	projectPath = "/api/v1/projects/{id}"
)

func onSession(id string, group Group, method, suffix string) Action {
	return Action{ID: id, Group: group, Method: method, Path: sessionPath + suffix, Project: ProjectSession, Key: "sessionId"}
}

func onProject(id string, group Group, method, suffix string) Action {
	return Action{ID: id, Group: group, Method: method, Path: projectPath + suffix, Project: ProjectParam, Key: "id"}
}

func machine(id string, group Group, method, path string) Action {
	return Action{ID: id, Group: group, Method: method, Path: path}
}

func everyProject(id string, group Group, method, path string) Action {
	return Action{ID: id, Group: group, Method: method, Path: path, Project: ProjectAll}
}

var catalog = []Action{
	{ID: "session.list", Group: SessionsRead, Method: http.MethodGet, Path: "/api/v1/sessions", Project: ProjectQuery, Key: "project"},
	onSession("session.get", SessionsRead, http.MethodGet, ""),
	onSession("session.history", SessionsRead, http.MethodGet, "/history"),
	onSession("session.conversation", SessionsRead, http.MethodGet, "/conversation"),
	onSession("session.prs", SessionsRead, http.MethodGet, "/pr"),
	onSession("session.reviews", SessionsRead, http.MethodGet, "/reviews"),
	onSession("session.interface-transition", SessionsRead, http.MethodGet, "/interface-transition"),
	everyProject("session.search", SessionsRead, http.MethodGet, "/api/v1/search/sessions"),
	everyProject("orchestrator.list", SessionsRead, http.MethodGet, "/api/v1/orchestrators"),
	everyProject("notification.list", SessionsRead, http.MethodGet, "/api/v1/notifications"),
	{ID: "report.list", Group: SessionsRead, Method: http.MethodGet, Path: "/api/v1/reports", Project: ProjectQuery, Key: "projectId"},

	{ID: "session.spawn", Group: SessionsOperate, Method: http.MethodPost, Path: "/api/v1/sessions", Project: ProjectBody, Key: "projectId"},
	{ID: "orchestrator.spawn", Group: SessionsOperate, Method: http.MethodPost, Path: "/api/v1/orchestrators", Project: ProjectBody, Key: "projectId"},
	onSession("session.send", SessionsOperate, http.MethodPost, "/send"),
	onSession("session.steer", SessionsOperate, http.MethodPost, "/conversation/steer-or-send"),
	onSession("session.interrupt", SessionsOperate, http.MethodPost, "/stop-turn"),
	onSession("session.restore", SessionsOperate, http.MethodPost, "/restore"),
	onSession("session.rename", SessionsOperate, http.MethodPatch, ""),
	onSession("session.kill", SessionsOperate, http.MethodPost, "/kill"),
	onSession("session.exit-agent", SessionsOperate, http.MethodPost, "/exit-agent"),
	onSession("session.resume-agent", SessionsOperate, http.MethodPost, "/resume-agent"),
	onSession("session.claim-pr", SessionsOperate, http.MethodPost, "/pr/claim"),
	onSession("session.rollback", SessionsOperate, http.MethodPost, "/rollback"),
	{ID: "report.create", Group: SessionsOperate, Method: http.MethodPost, Path: "/api/v1/reports", Project: ProjectBodySession, Key: "sessionId"},
	machine("agent.refresh", SessionsOperate, http.MethodPost, "/api/v1/agents/refresh"),
	machine("agent.ensure-ready", SessionsOperate, http.MethodPost, "/api/v1/agents/readiness/ensure"),

	onSession("session.delete", SessionsManage, http.MethodDelete, ""),
	{ID: "session.cleanup", Group: SessionsManage, Method: http.MethodPost, Path: "/api/v1/sessions/cleanup", Project: ProjectQuery, Key: "project"},

	onSession("chat.approval.resolve", ChatRespond, http.MethodPost, "/conversation/approvals/{requestId}/resolve"),
	onSession("chat.input.resolve", ChatRespond, http.MethodPost, "/conversation/inputs/{requestId}/resolve"),

	everyProject("project.list", ProjectsRead, http.MethodGet, "/api/v1/projects"),
	onProject("project.get", ProjectsRead, http.MethodGet, ""),
	{ID: "cue.list", Group: ProjectsRead, Method: http.MethodGet, Path: "/api/v1/projects/{projectId}/cues", Project: ProjectParam, Key: "projectId"},

	everyProject("project.add", ProjectsManage, http.MethodPost, "/api/v1/projects"),
	onProject("project.update", ProjectsManage, http.MethodPut, ""),
	onProject("project.configure", ProjectsManage, http.MethodPut, "/config"),
	onProject("project.remove", ProjectsManage, http.MethodDelete, ""),
	{ID: "cue.create", Group: ProjectsManage, Method: http.MethodPost, Path: "/api/v1/projects/{projectId}/cues", Project: ProjectParam, Key: "projectId"},

	everyProject("pr.merge", PRsOperate, http.MethodPost, "/api/v1/prs/{id}/merge"),
	everyProject("pr.resolve-comments", PRsOperate, http.MethodPost, "/api/v1/prs/{id}/resolve-comments"),

	onSession("review.trigger", ReviewsOperate, http.MethodPost, "/reviews/trigger"),
	onSession("review.cancel", ReviewsOperate, http.MethodPost, "/reviews/cancel"),
	onSession("review.submit", ReviewsOperate, http.MethodPost, "/reviews/submit"),

	everyProject("automation.list", AutomationsRead, http.MethodGet, "/api/v1/automations"),
	everyProject("automation.get", AutomationsRead, http.MethodGet, "/api/v1/automations/{automationId}"),
	everyProject("automation.runs", AutomationsRead, http.MethodGet, "/api/v1/automations/{automationId}/runs"),
	everyProject("automation.create", AutomationsManage, http.MethodPost, "/api/v1/automations"),
	everyProject("automation.update", AutomationsManage, http.MethodPatch, "/api/v1/automations/{automationId}"),
	everyProject("automation.delete", AutomationsManage, http.MethodDelete, "/api/v1/automations/{automationId}"),

	machine("agent.list", AccountsRead, http.MethodGet, "/api/v1/agents"),
	machine("account.list", AccountsRead, http.MethodGet, "/api/v1/agents/accounts"),
	machine("account.usage", AccountsRead, http.MethodGet, "/api/v1/agents/accounts/usage"),
	{ID: "account.session", Group: AccountsRead, Method: http.MethodGet, Path: "/api/v1/agents/accounts/sessions/{sessionId}", Project: ProjectSession, Key: "sessionId"},
	machine("account.set-default", AccountsManage, http.MethodPost, "/api/v1/agents/accounts/{accountId}/default"),
	{ID: "account.move-session", Group: AccountsManage, Method: http.MethodPut, Path: "/api/v1/agents/accounts/sessions/{sessionId}", Project: ProjectSession, Key: "sessionId"},

	machine("settings.get", SettingsRead, http.MethodGet, "/api/v1/settings"),
	machine("settings.harness-default", SettingsManage, http.MethodPatch, "/api/v1/settings/harness-defaults/{agent}"),
	machine("settings.session-interface", SettingsManage, http.MethodPatch, "/api/v1/settings/session-interface"),
	machine("settings.chat-hibernation", SettingsManage, http.MethodPatch, "/api/v1/settings/chat-hibernation"),
	machine("settings.session-retention", SettingsManage, http.MethodPatch, "/api/v1/settings/session-retention"),
	machine("settings.account-handoff", SettingsManage, http.MethodPatch, "/api/v1/settings/account-handoff"),
	machine("settings.version-refresh", SettingsManage, http.MethodPatch, "/api/v1/settings/version-refresh"),
}

// Catalog returns every action a grant can reach, in catalog order.
func Catalog() []Action { return slices.Clone(catalog) }

// ActionByID finds a catalog action by its ID.
func ActionByID(id string) (Action, bool) {
	for _, action := range catalog {
		if action.ID == id {
			return action, true
		}
	}
	return Action{}, false
}

// ActionForRoute finds the catalog action served by a method and route pattern.
func ActionForRoute(method, pattern string) (Action, bool) {
	for _, action := range catalog {
		if action.Method == method && action.Path == pattern {
			return action, true
		}
	}
	return Action{}, false
}

// StartsSession reports whether the action creates a session that keeps the caller's grant.
func (a Action) StartsSession() bool {
	return a.ID == "session.spawn" || a.ID == "orchestrator.spawn"
}
