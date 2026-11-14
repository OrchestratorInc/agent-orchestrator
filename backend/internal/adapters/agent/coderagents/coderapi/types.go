package coderapi

// The wire types below mirror codersdk at Coder v2.37.9 (codersdk/chats.go),
// limited to the fields AO reads or sends. Unknown fields are ignored so a
// newer deployment's additions do not break decoding.

// User is GET /api/v2/users/me.
type User struct {
	ID              string   `json:"id"`
	Username        string   `json:"username"`
	Email           string   `json:"email"`
	Name            string   `json:"name"`
	OrganizationIDs []string `json:"organization_ids"`
}

// Organization is one entry of GET /api/v2/users/me/organizations.
type Organization struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	IsDefault   bool   `json:"is_default"`
}

// ChatModel is one admin-configured model config.
type ChatModel struct {
	ID               string   `json:"id"`
	Model            string   `json:"model"`
	DisplayName      string   `json:"display_name"`
	Enabled          bool     `json:"enabled"`
	IsDefault        bool     `json:"is_default"`
	ContextLimit     int64    `json:"context_limit"`
	ReasoningEfforts []string `json:"reasoning_efforts,omitempty"`
}
