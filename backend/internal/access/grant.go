// Package access says who calls the daemon and what that caller may do: named
// agent tokens, the grant each one carries, and the catalog of actions a grant
// can reach. A grant limits API calls. It does not confine a process that runs
// as the same OS user.
package access

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// Group names a set of actions a grant can allow.
type Group string

// The groups a grant can allow.
const (
	SessionsRead      Group = "sessions.read"
	SessionsOperate   Group = "sessions.operate"
	SessionsManage    Group = "sessions.manage"
	ChatRespond       Group = "chat.respond"
	ProjectsRead      Group = "projects.read"
	ProjectsManage    Group = "projects.manage"
	PRsOperate        Group = "prs.operate"
	ReviewsOperate    Group = "reviews.operate"
	AutomationsRead   Group = "automations.read"
	AutomationsManage Group = "automations.manage"
	AccountsRead      Group = "accounts.read"
	AccountsManage    Group = "accounts.manage"
	SettingsRead      Group = "settings.read"
	SettingsManage    Group = "settings.manage"
)

// The presets, each a superset of the one before.
const (
	PresetRead    = "read"
	PresetOperate = "operate"
	PresetManage  = "manage"
)

var (
	readGroups    = []Group{SessionsRead, ProjectsRead, AutomationsRead, AccountsRead, SettingsRead}
	operateGroups = append(slices.Clone(readGroups), SessionsOperate, ChatRespond, PRsOperate, ReviewsOperate)
	manageGroups  = append(slices.Clone(operateGroups), SessionsManage, ProjectsManage, AutomationsManage, AccountsManage, SettingsManage)
	presets       = map[string][]Group{PresetRead: readGroups, PresetOperate: operateGroups, PresetManage: manageGroups}
)

// Presets returns the preset names, narrowest first.
func Presets() []string { return []string{PresetRead, PresetOperate, PresetManage} }

// PresetGroups returns the groups a preset allows.
func PresetGroups(name string) ([]Group, bool) {
	groups, ok := presets[name]
	return slices.Clone(groups), ok
}

// Groups returns every group, sorted.
func Groups() []Group {
	out := slices.Clone(manageGroups)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Grant is what one caller may do. No projects means every project.
type Grant struct {
	Preset    string    `json:"preset,omitempty"`
	Groups    []Group   `json:"groups"`
	Projects  []string  `json:"projects,omitempty"`
	ExpiresAt time.Time `json:"expiresAt,omitzero"`
	// Ended marks the grant of a session whose token is gone.
	Ended bool `json:"-"`
}

// NewGrant builds a grant from a preset or from named groups, not both.
func NewGrant(preset string, groups []Group, projects []string, expiresAt time.Time) (Grant, error) {
	grant := Grant{ExpiresAt: expiresAt.UTC()}
	switch {
	case preset != "" && len(groups) > 0:
		return Grant{}, fmt.Errorf("give a preset or groups, not both")
	case preset != "":
		presetGroups, ok := PresetGroups(preset)
		if !ok {
			return Grant{}, fmt.Errorf("unknown preset %q; use one of %s", preset, strings.Join(Presets(), ", "))
		}
		grant.Preset, grant.Groups = preset, presetGroups
	case len(groups) > 0:
		for _, group := range groups {
			if !slices.Contains(manageGroups, group) {
				return Grant{}, fmt.Errorf("unknown group %q", group)
			}
			if !slices.Contains(grant.Groups, group) {
				grant.Groups = append(grant.Groups, group)
			}
		}
	default:
		return Grant{}, fmt.Errorf("give a preset or at least one group")
	}
	for _, project := range projects {
		if project = strings.TrimSpace(project); project != "" && !slices.Contains(grant.Projects, project) {
			grant.Projects = append(grant.Projects, project)
		}
	}
	return grant, nil
}

// Refusal is why a grant does not allow a call.
type Refusal struct {
	Action  string `json:"action,omitempty"`
	Group   Group  `json:"group,omitempty"`
	Reason  string `json:"reason"`
	Message string `json:"-"`
}

func (r *Refusal) Error() string { return r.Message }

// The reasons a call is refused.
const (
	ReasonCatalog = "not_in_catalog"
	ReasonGroup   = "group"
	ReasonProject = "project"
	ReasonEnded   = "grant_ended"
)

// Check reports whether the grant allows action on project. known is false
// when the caller named no project.
func (g Grant) Check(action Action, project string, known bool, now time.Time) *Refusal {
	if g.Ended || (!g.ExpiresAt.IsZero() && !now.Before(g.ExpiresAt)) {
		return &Refusal{Action: action.ID, Reason: ReasonEnded, Message: "this grant has ended"}
	}
	if !slices.Contains(g.Groups, action.Group) {
		return &Refusal{Action: action.ID, Group: action.Group, Reason: ReasonGroup,
			Message: fmt.Sprintf("this grant has no group %s, which %s needs", action.Group, action.ID)}
	}
	if len(g.Projects) == 0 || action.Project == ProjectNone {
		return nil
	}
	limit := strings.Join(g.Projects, ", ")
	if action.Project == ProjectAll {
		return &Refusal{Action: action.ID, Group: action.Group, Reason: ReasonProject,
			Message: fmt.Sprintf("%s reaches every project, and this grant is limited to: %s", action.ID, limit)}
	}
	if !known {
		return &Refusal{Action: action.ID, Group: action.Group, Reason: ReasonProject,
			Message: fmt.Sprintf("%s needs a project, and this grant is limited to: %s", action.ID, limit)}
	}
	if !slices.Contains(g.Projects, project) {
		return &Refusal{Action: action.ID, Group: action.Group, Reason: ReasonProject,
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
	// TokenID is the token whose grant applies, also for a session it started.
	TokenID string `json:"tokenId,omitempty"`
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
