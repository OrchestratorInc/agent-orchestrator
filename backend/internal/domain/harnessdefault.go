package domain

// HarnessDefault is a daemon-owned picker preference for one harness.
// DisabledModels lists the models clients leave out of their pickers; empty
// means every catalog model, and models a catalog adds later are offered. It
// never changes what a session may be launched with. Rows saved by earlier
// builds may carry model, effort, and enabledModels keys; they are ignored.
type HarnessDefault struct {
	DisabledModels []string `json:"disabledModels,omitempty" maxItems:"20000"`
}

// IsZero reports whether the preference holds nothing worth storing.
func (d HarnessDefault) IsZero() bool {
	return len(d.DisabledModels) == 0
}
