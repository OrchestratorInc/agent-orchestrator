package domain

// SpeedMode selects a serving tier independently of reasoning effort.
type SpeedMode string

// Supported serving speeds; an omitted choice has Normal behavior.
const (
	SpeedModeNormal    SpeedMode = "normal"
	SpeedModeFast      SpeedMode = "fast"
	SpeedModeUltrafast SpeedMode = "ultrafast"
)

// Valid accepts the three choices and omission by legacy callers.
func (s SpeedMode) Valid() bool {
	return s == "" || s == SpeedModeNormal || s == SpeedModeFast || s == SpeedModeUltrafast
}

// Accelerated reports whether provider capability validation is required.
func (s SpeedMode) Accelerated() bool {
	return s == SpeedModeFast || s == SpeedModeUltrafast
}
