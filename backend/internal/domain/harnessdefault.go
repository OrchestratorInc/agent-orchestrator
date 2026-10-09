package domain

// HarnessDefault is a daemon-owned model preference for future sessions.
// Empty Model means use the harness's own default; Effort belongs to Model.
type HarnessDefault struct {
	Model  string `json:"model,omitempty" maxLength:"256"`
	Effort string `json:"effort,omitempty" maxLength:"32"`
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
