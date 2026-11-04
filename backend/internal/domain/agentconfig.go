package domain

import "fmt"

// PermissionMode controls how much review an agent requires before acting. It
// lives in domain (not ports) so the typed AgentConfig can carry it; ports
// re-exports it as a type alias so agent adapters keep referring to
// ports.PermissionMode unchanged.
type PermissionMode string

// The permission modes adapters map onto their agent's native approval flags.
const (
	// PermissionModeDefault is special: adapters choose their own baseline
	// behavior for it. Most defer to the agent's own config; some managed
	// adapters may map it to a safer non-interactive default.
	PermissionModeDefault           PermissionMode = "default"
	PermissionModeAcceptEdits       PermissionMode = "accept-edits"
	PermissionModeAuto              PermissionMode = "auto"
	PermissionModeBypassPermissions PermissionMode = "bypass-permissions"
)

// AgentConfig is the typed per-project agent configuration. It replaces the
// former free-form map so the fields are validated and the API/UI render a
// real form rather than arbitrary JSON. An empty value (IsZero) means unset.
type AgentConfig struct {
	// Model overrides the agent's default model (e.g. claude-opus-4-5).
	Model string `json:"model,omitempty"`
	// Effort selects a model-advertised reasoning level. Empty defers to the
	// provider/model default.
	Effort string `json:"effort,omitempty"`
	// Mode selects an agent-owned operating mode when the adapter exposes modes
	// instead of raw model ids (currently Amp: low|medium|high|ultra).
	Mode string `json:"mode,omitempty"`
	// Permissions sets the agent's starting permission mode. Empty inherits the
	// project/role preference; new sessions fall back to Auto when none is saved.
	// Other adapter callers retain their existing baseline for an empty value.
	Permissions PermissionMode `json:"permissions,omitempty"`
	// Launch carries one session's own launch choices to its adapter. It is
	// never part of a saved or served project config.
	Launch AgentLaunchOptions `json:"-"`
}

// AgentLaunchOptions are choices a terminal agent only reads when it starts.
// AO stores them with the session so a later restart launches it the same way.
type AgentLaunchOptions struct {
	// FastMode starts the agent on its faster tier (Codex).
	FastMode bool `json:"fastMode,omitempty"`
	// FastModeOff starts Codex on its standard tier even where its config or the
	// model would pick the faster one, as a switch whose picker showed it off asks.
	FastModeOff bool `json:"fastModeOff,omitempty"`
	// Ultracode starts Claude Code with Ultracode on.
	Ultracode bool `json:"ultracode,omitempty"`
	// AutoCompactWindow is where Claude Code compacts on its own: "200k" or "1m".
	AutoCompactWindow string `json:"autoCompactWindow,omitempty"`
	// HandoffNote is a terminal switch target's note, kept until the target has
	// started. A relaunch that finds it follows a switch cut short by a daemon
	// stop or crash, and delivers the note in place of the session's task.
	HandoffNote string `json:"handoffNote,omitempty"`
}

// AutoCompactTokens is each compaction window choice as a token count.
var AutoCompactTokens = map[string]int{"200k": 200_000, "1m": 1_000_000}

// IsZero reports whether the config carries no settings, so storage can persist
// SQL NULL and resolution can skip an empty config.
func (c AgentConfig) IsZero() bool {
	return c == AgentConfig{}
}

// Valid reports whether the mode is one AO knows. Empty counts as valid: it means
// "the adapter's own baseline", which is a legitimate choice rather than a missing
// one.
func (m PermissionMode) Valid() bool {
	switch m {
	case "", PermissionModeDefault, PermissionModeAcceptEdits,
		PermissionModeAuto, PermissionModeBypassPermissions:
		return true
	default:
		return false
	}
}

// Validate rejects values outside the typed vocabulary so a bad config is
// refused when it is set (CLI/API) rather than silently dropped at spawn.
func (c AgentConfig) Validate() error {
	switch c.Mode {
	case "", "low", "medium", "high", "ultra":
	default:
		return fmt.Errorf("invalid mode %q: want one of low, medium, high, ultra", c.Mode)
	}
	if c.Permissions.Valid() {
		return nil
	}
	return fmt.Errorf("invalid permissions %q: choose a supported permission mode", c.Permissions)
}
