package domain

// HarnessDefault is a daemon-owned picker preference for one harness.
// EnabledModels narrows the models clients offer in their pickers; empty means
// every catalog model. It never changes what a session may be launched with.
// Rows saved by earlier builds may carry model and effort keys; they are
// ignored.
type HarnessDefault struct {
	EnabledModels []string `json:"enabledModels,omitempty" maxItems:"5000"`
}

// IsZero reports whether the preference holds nothing worth storing.
func (d HarnessDefault) IsZero() bool {
	return len(d.EnabledModels) == 0
}
