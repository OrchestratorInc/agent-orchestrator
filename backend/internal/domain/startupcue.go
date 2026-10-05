package domain

import "time"

// StartupCueRun records execution facts, independent of derived session status.
type StartupCueRun struct {
	CueID          CueID      `json:"cueId"`
	Name           string     `json:"name"`
	Command        string     `json:"command"`
	TerminalHandle string     `json:"terminalHandle,omitempty"`
	DeliveryHeld   bool       `json:"deliveryHeld,omitempty"`
	State          string     `json:"state" enum:"pending,running,succeeded,failed,interrupted,cancelled"`
	StartedAt      time.Time  `json:"startedAt"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
	Output         string     `json:"output,omitempty"`
	Error          string     `json:"error,omitempty"`
	ExitCode       *int       `json:"exitCode,omitempty"`
}

// HoldsInput reports whether setup or its queued delivery still gates provider input.
func (r *StartupCueRun) HoldsInput() bool {
	return r != nil && (r.State == "pending" || r.State == "running" || r.DeliveryHeld)
}

// StartupCueMessage is a durable TUI message held during startup.
type StartupCueMessage struct {
	ID              int64
	Message         string
	ClientMessageID string
}
