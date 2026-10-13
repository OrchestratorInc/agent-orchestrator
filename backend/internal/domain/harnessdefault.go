package domain

// HarnessDefault is a daemon-owned model preference for future sessions.
// Empty Model means use the harness's own default; Effort belongs to Model.
// EnabledModels narrows the models clients offer in their pickers; empty means
// every catalog model. It never changes what a session may be launched with.
type HarnessDefault struct {
	Model         string   `json:"model,omitempty" maxLength:"256"`
	Effort        string   `json:"effort,omitempty" maxLength:"32"`
	EnabledModels []string `json:"enabledModels,omitempty"`
}

// IsZero reports whether the preference holds nothing worth storing.
func (d HarnessDefault) IsZero() bool {
	return d.Model == "" && d.Effort == "" && len(d.EnabledModels) == 0
}

// Apply fills project omissions without leaking effort to a different model.
func (d HarnessDefault) Apply(config AgentConfig) AgentConfig {
	if config.Model == "" && config.Mode == "" {
		config.Model = d.Model
	}
	if config.Effort == "" && config.Mode == "" && config.Model == d.Model {
		config.Effort = d.Effort
	}
	return config
}
