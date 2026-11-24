// Package access says who calls the daemon and what that caller may do: named
// agent tokens, the grant each one carries, and the catalog of actions a grant
// can reach. A grant limits API calls. It does not confine a process that runs
// as the same OS user.
package access

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// The levels a grant can hold. Each allows everything the one before it does.
const (
	LevelRead    = "read"
	LevelOperate = "operate"
	LevelManage  = "manage"
)

var levels = []string{LevelRead, LevelOperate, LevelManage}

// Levels returns the level names, narrowest first.
func Levels() []string { return slices.Clone(levels) }

// Grant is what one caller may do. No projects means every project.
type Grant struct {
	Level     string    `json:"level"`
	Projects  []string  `json:"projects,omitempty"`
	ExpiresAt time.Time `json:"expiresAt,omitzero"`
}

// NewGrant builds a grant at a level, limited to projects when any are named.
func NewGrant(level string, projects []string, expiresAt time.Time) (Grant, error) {
	if !slices.Contains(levels, level) {
		return Grant{}, fmt.Errorf("unknown level %q; use one of %s", level, strings.Join(levels, ", "))
	}
	grant := Grant{Level: level, ExpiresAt: expiresAt.UTC()}
	for _, project := range projects {
		if project = strings.TrimSpace(project); project == "" {
			return Grant{}, fmt.Errorf("a project id is empty; an empty limit would mean every project")
		}
		if !slices.Contains(grant.Projects, project) {
			grant.Projects = append(grant.Projects, project)
		}
	}
	return grant, nil
}

// Refusal is why a grant does not allow a call.
type Refusal struct {
	Action  string `json:"action,omitempty"`
	Level   string `json:"level,omitempty"`
	Reason  string `json:"reason"`
	Message string `json:"-"`
}

// The reasons a call is refused.
const (
	ReasonCatalog = "not_in_catalog"
	ReasonLevel   = "level"
	ReasonProject = "project"
)

// Check reports whether the grant allows action on project. known is false
// when the caller named no project.
func (g Grant) Check(action Action, project string, known bool) *Refusal {
	need := slices.Index(levels, action.Level)
	if need < 0 || slices.Index(levels, g.Level) < need {
		return &Refusal{Action: action.ID, Level: action.Level, Reason: ReasonLevel,
			Message: fmt.Sprintf("this grant is %s, and %s needs %s", g.Level, action.ID, action.Level)}
	}
	if len(g.Projects) == 0 || action.Project == ProjectNone {
		return nil
	}
	limit := strings.Join(g.Projects, ", ")
	if action.Project == ProjectAll {
		return &Refusal{Action: action.ID, Level: action.Level, Reason: ReasonProject,
			Message: fmt.Sprintf("%s reaches every project, and this grant is limited to: %s", action.ID, limit)}
	}
	if !known {
		return &Refusal{Action: action.ID, Level: action.Level, Reason: ReasonProject,
			Message: fmt.Sprintf("%s needs a project, and this grant is limited to: %s", action.ID, limit)}
	}
	if !slices.Contains(g.Projects, project) {
		return &Refusal{Action: action.ID, Level: action.Level, Reason: ReasonProject,
			Message: fmt.Sprintf("this grant is limited to: %s", limit)}
	}
	return nil
}

// The kinds of caller the daemon tells apart.
const (
	CallerUser    = "user"
	CallerSession = "session"
	CallerToken   = "token"
	CallerDevice  = "device"
)

// Caller is who made a request. A nil Grant means no limit.
type Caller struct {
	Kind  string `json:"kind"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Grant *Grant `json:"grant,omitempty"`
}

type callerKey struct{}

// WithCaller records the resolved caller on a request context.
func WithCaller(ctx context.Context, caller Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, caller)
}

// CallerFrom returns the resolved caller, or the local user when none was recorded.
func CallerFrom(ctx context.Context) Caller {
	if caller, ok := ctx.Value(callerKey{}).(Caller); ok {
		return caller
	}
	return Caller{Kind: CallerUser}
}
