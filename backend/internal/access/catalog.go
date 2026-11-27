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
	Level   string
	Method  string
	Path    string
	Project ProjectSource
	Key     string
}

const (
	sessionPath = "/api/v1/sessions/{sessionId}"
	projectPath = "/api/v1/projects/{id}"
)

func onSession(id, level, method, suffix string) Action {
	return Action{ID: id, Level: level, Method: method, Path: sessionPath + suffix, Project: ProjectSession, Key: "sessionId"}
}

func onProject(id, level, method, suffix string) Action {
	return Action{ID: id, Level: level, Method: method, Path: projectPath + suffix, Project: ProjectParam, Key: "id"}
}

func machine(id, level, method, path string) Action {
	return Action{ID: id, Level: level, Method: method, Path: path}
}

func everyProject(id, level, method, path string) Action {
	return Action{ID: id, Level: level, Method: method, Path: path, Project: ProjectAll}
}

var catalog = []Action{
	{ID: "session.list", Level: LevelRead, Method: http.MethodGet, Path: "/api/v1/sessions", Project: ProjectQuery, Key: "project"},
	onSession("session.get", LevelRead, http.MethodGet, ""),
	onSession("session.history", LevelRead, http.MethodGet, "/history"),
	onSession("session.conversation", LevelRead, http.MethodGet, "/conversation"),
	onSession("session.prs", LevelRead, http.MethodGet, "/pr"),
	onSession("session.reviews", LevelRead, http.MethodGet, "/reviews"),
	onSession("session.interface-transition", LevelRead, http.MethodGet, "/interface-transition"),
	everyProject("session.search", LevelRead, http.MethodGet, "/api/v1/search/sessions"),
	everyProject("orchestrator.list", LevelRead, http.MethodGet, "/api/v1/orchestrators"),
	everyProject("notification.list", LevelRead, http.MethodGet, "/api/v1/notifications"),
	{ID: "report.list", Level: LevelRead, Method: http.MethodGet, Path: "/api/v1/reports", Project: ProjectQuery, Key: "projectId"},

	{ID: "session.spawn", Level: LevelOperate, Method: http.MethodPost, Path: "/api/v1/sessions", Project: ProjectBody, Key: "projectId"},
	{ID: "orchestrator.spawn", Level: LevelOperate, Method: http.MethodPost, Path: "/api/v1/orchestrators", Project: ProjectBody, Key: "projectId"},
	onSession("session.send", LevelOperate, http.MethodPost, "/send"),
	onSession("session.steer", LevelOperate, http.MethodPost, "/conversation/steer-or-send"),
	onSession("session.interrupt", LevelOperate, http.MethodPost, "/stop-turn"),
	onSession("session.restore", LevelOperate, http.MethodPost, "/restore"),
	onSession("session.rename", LevelOperate, http.MethodPatch, ""),
	onSession("session.kill", LevelOperate, http.MethodPost, "/kill"),
	onSession("session.exit-agent", LevelOperate, http.MethodPost, "/exit-agent"),
	onSession("session.resume-agent", LevelOperate, http.MethodPost, "/resume-agent"),
	onSession("session.claim-pr", LevelOperate, http.MethodPost, "/pr/claim"),
	onSession("session.rollback", LevelOperate, http.MethodPost, "/rollback"),
	{ID: "report.create", Level: LevelOperate, Method: http.MethodPost, Path: "/api/v1/reports", Project: ProjectBodySession, Key: "sessionId"},
	machine("agent.refresh", LevelOperate, http.MethodPost, "/api/v1/agents/refresh"),
	machine("agent.ensure-ready", LevelOperate, http.MethodPost, "/api/v1/agents/readiness/ensure"),

	onSession("session.delete", LevelManage, http.MethodDelete, ""),
	{ID: "session.cleanup", Level: LevelManage, Method: http.MethodPost, Path: "/api/v1/sessions/cleanup", Project: ProjectQuery, Key: "project"},

	onSession("chat.approval.resolve", LevelOperate, http.MethodPost, "/conversation/approvals/{requestId}/resolve"),
	onSession("chat.input.resolve", LevelOperate, http.MethodPost, "/conversation/inputs/{requestId}/resolve"),

	everyProject("project.list", LevelRead, http.MethodGet, "/api/v1/projects"),
	onProject("project.get", LevelRead, http.MethodGet, ""),
	{ID: "cue.list", Level: LevelRead, Method: http.MethodGet, Path: "/api/v1/projects/{projectId}/cues", Project: ProjectParam, Key: "projectId"},

	everyProject("project.add", LevelManage, http.MethodPost, "/api/v1/projects"),
	onProject("project.update", LevelManage, http.MethodPut, ""),
	onProject("project.configure", LevelManage, http.MethodPut, "/config"),
	onProject("project.remove", LevelManage, http.MethodDelete, ""),
	{ID: "cue.create", Level: LevelManage, Method: http.MethodPost, Path: "/api/v1/projects/{projectId}/cues", Project: ProjectParam, Key: "projectId"},

	everyProject("pr.merge", LevelOperate, http.MethodPost, "/api/v1/prs/{id}/merge"),
	everyProject("pr.resolve-comments", LevelOperate, http.MethodPost, "/api/v1/prs/{id}/resolve-comments"),

	onSession("review.trigger", LevelOperate, http.MethodPost, "/reviews/trigger"),
	onSession("review.cancel", LevelOperate, http.MethodPost, "/reviews/cancel"),
	onSession("review.submit", LevelOperate, http.MethodPost, "/reviews/submit"),

	everyProject("automation.list", LevelRead, http.MethodGet, "/api/v1/automations"),
	everyProject("automation.get", LevelRead, http.MethodGet, "/api/v1/automations/{automationId}"),
	everyProject("automation.runs", LevelRead, http.MethodGet, "/api/v1/automations/{automationId}/runs"),
	everyProject("automation.create", LevelManage, http.MethodPost, "/api/v1/automations"),
	everyProject("automation.update", LevelManage, http.MethodPatch, "/api/v1/automations/{automationId}"),
	everyProject("automation.delete", LevelManage, http.MethodDelete, "/api/v1/automations/{automationId}"),

	machine("agent.list", LevelRead, http.MethodGet, "/api/v1/agents"),
	machine("account.list", LevelRead, http.MethodGet, "/api/v1/agents/accounts"),
	machine("account.usage", LevelRead, http.MethodGet, "/api/v1/agents/accounts/usage"),
	{ID: "account.session", Level: LevelRead, Method: http.MethodGet, Path: "/api/v1/agents/accounts/sessions/{sessionId}", Project: ProjectSession, Key: "sessionId"},
	everyProject("account.set-default", LevelManage, http.MethodPost, "/api/v1/agents/accounts/{accountId}/default"),
	{ID: "account.move-session", Level: LevelManage, Method: http.MethodPut, Path: "/api/v1/agents/accounts/sessions/{sessionId}", Project: ProjectSession, Key: "sessionId"},

	machine("settings.get", LevelRead, http.MethodGet, "/api/v1/settings"),
	everyProject("settings.harness-default", LevelManage, http.MethodPatch, "/api/v1/settings/harness-defaults/{agent}"),
	everyProject("settings.session-interface", LevelManage, http.MethodPatch, "/api/v1/settings/session-interface"),
	everyProject("settings.chat-hibernation", LevelManage, http.MethodPatch, "/api/v1/settings/chat-hibernation"),
	everyProject("settings.session-retention", LevelManage, http.MethodPatch, "/api/v1/settings/session-retention"),
	everyProject("settings.account-handoff", LevelManage, http.MethodPatch, "/api/v1/settings/account-handoff"),
	everyProject("settings.version-refresh", LevelManage, http.MethodPatch, "/api/v1/settings/version-refresh"),
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
