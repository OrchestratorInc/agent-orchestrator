package domain

type HarnessDefault struct {
	DisabledModels []string `json:"disabledModels,omitempty" maxItems:"20000"`
}

func (d HarnessDefault) IsZero() bool {
	return len(d.DisabledModels) == 0
}
