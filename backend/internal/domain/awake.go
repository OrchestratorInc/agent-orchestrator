package domain

import "time"

type AwakeBrowserAttempt struct {
	ID        string `json:"id"`
	BrowserID string `json:"browserId"`
	Outcome   string `json:"outcome"`
}

type AwakeNetworkAttempt struct {
	Episode     string     `json:"episode"`
	Attempts    int        `json:"attempts"`
	LastAttempt *time.Time `json:"lastAttempt,omitempty"`
	Outcome     string     `json:"outcome"`
}

type AwakeDocument struct {
	AgentAttempts   []AwakeAgentAttempt   `json:"agentAttempts,omitempty"`
	AgentOutcome    string                `json:"agentOutcome,omitempty"`
	Version         int                   `json:"version"`
	Settings        AwakeSettings         `json:"settings"`
	Sessions        []AwakeSession        `json:"sessions"`
	Rules           []AwakeRule           `json:"rules"`
	Paused          bool                  `json:"paused"`
	StopReason      string                `json:"stopReason"`
	BrowserAttempts []AwakeBrowserAttempt `json:"browserAttempts"`
	Network         AwakeNetworkAttempt   `json:"network"`
}

type AwakeSettings struct {
	LidClosed              bool     `json:"lidClosed"`
	DisplayAwake           bool     `json:"displayAwake"`
	StopBatteryPercent     int      `json:"stopBatteryPercent"`
	LowPowerBatteryPercent int      `json:"lowPowerBatteryPercent"`
	LowPowerOnSeriousHeat  bool     `json:"lowPowerOnSeriousHeat"`
	MuteAudio              bool     `json:"muteAudio"`
	DarkenKeyboard         bool     `json:"darkenKeyboard"`
	RestoreDisplay         bool     `json:"restoreDisplay"`
	PauseApps              []string `json:"pauseApps"`
	HotspotName            string   `json:"hotspotName"`
	HotspotRecovery        bool     `json:"hotspotRecovery"`
	AgentReconnect         bool     `json:"agentReconnect"`
	BrowserActions         bool     `json:"browserActions"`
}

type AwakeSession struct {
	ID            string     `json:"id"`
	Scope         string     `json:"scope" enum:"ao,session"`
	SessionID     string     `json:"sessionId"`
	Label         string     `json:"label"`
	Mode          string     `json:"mode" enum:"indefinite,duration,until,app,download"`
	CreatedAt     time.Time  `json:"createdAt"`
	Deadline      *time.Time `json:"deadline,omitempty"`
	BoundID       string     `json:"boundId"`
	BoundIdentity string     `json:"boundIdentity"`
	EndedAt       *time.Time `json:"endedAt,omitempty"`
	EndReason     string     `json:"endReason"`
	LidClosed     bool       `json:"lidClosed"`
}

type AwakeCondition struct {
	Kind      string `json:"kind" enum:"agent_activity,schedule,power,battery,network,device,display,app,uptime,download"`
	Value     string `json:"value"`
	Threshold int    `json:"threshold"`
	Start     string `json:"start"`
	End       string `json:"end"`
	Timezone  string `json:"timezone"`
}

type AwakeRule struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Enabled    bool             `json:"enabled"`
	Match      string           `json:"match" enum:"all,any"`
	Conditions []AwakeCondition `json:"conditions"`
	LidClosed  bool             `json:"lidClosed"`
	Suppressed bool             `json:"suppressed"`
}

type AwakeCapability struct {
	ID        string `json:"id"`
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

type AwakeApplication struct {
	Protected bool   `json:"protected"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Identity  string `json:"identity"`
}

type AwakeObservation struct {
	DisplayControl     bool               `json:"displayControl"`
	KeyboardControl    bool               `json:"keyboardControl"`
	ObservedAt         time.Time          `json:"observedAt"`
	BatteryPercent     *int               `json:"batteryPercent,omitempty"`
	OnBattery          *bool              `json:"onBattery,omitempty"`
	Thermal            string             `json:"thermal"`
	UptimeSeconds      float64            `json:"uptimeSeconds"`
	ExternalDisplays   int                `json:"externalDisplays"`
	AgentActive        *bool              `json:"agentActive,omitempty"`
	NetworkAddresses   []string           `json:"networkAddresses"`
	Devices            []string           `json:"devices"`
	LowPowerControl    bool               `json:"lowPowerControl"`
	LowPowerAuthorized bool               `json:"lowPowerAuthorized"`
	LowPowerMode       bool               `json:"lowPowerMode"`
	Applications       []AwakeApplication `json:"applications"`
	AudioControl       bool               `json:"audioControl"`
	AppPause           bool               `json:"appPause"`
	Browsers           []AwakeApplication `json:"browsers"`
	NetworkConnected   *bool              `json:"networkConnected,omitempty"`
	KnownNetworks      []string           `json:"knownNetworks"`
}

type AwakeState struct {
	AgentOutcome   string            `json:"agentOutcome"`
	Settings       AwakeSettings     `json:"settings"`
	Sessions       []AwakeSession    `json:"sessions"`
	Rules          []AwakeRule       `json:"rules"`
	ActiveRules    []string          `json:"activeRules"`
	Capabilities   []AwakeCapability `json:"capabilities"`
	Observation    AwakeObservation  `json:"observation"`
	Active         bool              `json:"active"`
	LidActive      bool              `json:"lidActive"`
	Reason         string            `json:"reason"`
	Paused         bool              `json:"paused"`
	StartedAt      *time.Time        `json:"startedAt,omitempty"`
	NetworkOutcome string            `json:"networkOutcome"`
}

type StartAwakeRequest struct {
	Scope           string     `json:"scope" enum:"ao,session"`
	SessionID       string     `json:"sessionId"`
	Mode            string     `json:"mode" enum:"indefinite,duration,until,app,download"`
	DurationSeconds int        `json:"durationSeconds"`
	Until           *time.Time `json:"until,omitempty"`
	BoundID         string     `json:"boundId"`
	LidClosed       bool       `json:"lidClosed"`
	BoundIdentity   string     `json:"boundIdentity,omitempty"`
}

type AwakeAgentTarget struct {
	SessionID SessionID
	Owner     SessionControllerOwner
}

type AwakeAgentAttempt struct {
	SessionID  SessionID `json:"sessionId"`
	Generation string    `json:"generation"`
	Outcome    string    `json:"outcome"`
}
