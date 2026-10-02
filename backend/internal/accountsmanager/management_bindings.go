package accountsmanager

import (
	"context"
	"net/http"
)

// RouteBinding contains durable user intent, never provider credentials.
type RouteBinding struct {
	Blocked   bool   `json:"blocked"`
	SessionID string `json:"sessionId"`
	Provider  string `json:"provider"`
	Mode      string `json:"mode"`
	AccountID string `json:"accountId"`
	Revision  int64  `json:"revision"`
}

// BindingSnapshot is ordered by the database's durable change clock.
type BindingSnapshot struct {
	Revision int64          `json:"revision"`
	Bindings []RouteBinding `json:"bindings"`
}

// SynchronizeBindings refreshes the runner's short-lived authorization registry.
func (c *ManagementClient) SynchronizeBindings(ctx context.Context, snapshot BindingSnapshot) error {
	if snapshot.Revision <= 0 {
		return ErrInvalidCredential
	}
	return c.doJSON(ctx, "synchronize bindings", http.MethodPut, "/ao/internal/routes/bindings", snapshot, nil)
}
