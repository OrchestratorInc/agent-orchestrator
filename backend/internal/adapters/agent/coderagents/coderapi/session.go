package coderapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrOrganizationAmbiguous means the user belongs to several organizations and
// none is the deployment default; AO will not guess where chats should run.
var ErrOrganizationAmbiguous = fmt.Errorf("coder user belongs to several organizations; set %s to the one Coder Agents should use", EnvOrganization)

// ResolveOrganization picks the organization AO uses: the selector (ID or
// name) when given, otherwise the only membership, otherwise the default.
func ResolveOrganization(ctx context.Context, client *Client, selector string) (Organization, error) {
	orgs, err := client.Organizations(ctx)
	if err != nil {
		return Organization{}, err
	}
	if len(orgs) == 0 {
		return Organization{}, errors.New("coder user is not a member of any organization")
	}
	if selector = strings.TrimSpace(selector); selector != "" {
		for _, org := range orgs {
			if org.ID == selector || strings.EqualFold(org.Name, selector) {
				return org, nil
			}
		}
		return Organization{}, fmt.Errorf("coder user is not a member of organization %q (%s)", selector, EnvOrganization)
	}
	if len(orgs) == 1 {
		return orgs[0], nil
	}
	for _, org := range orgs {
		if org.IsDefault {
			return org, nil
		}
	}
	return Organization{}, ErrOrganizationAmbiguous
}

// EnabledModels keeps only models the admin enabled, preserving order.
func EnabledModels(models []ChatModel) []ChatModel {
	out := make([]ChatModel, 0, len(models))
	for _, model := range models {
		if model.Enabled && strings.TrimSpace(model.ID) != "" {
			out = append(out, model)
		}
	}
	return out
}

// ModelLabel is the human name for a model config.
func ModelLabel(model ChatModel) string {
	if label := strings.TrimSpace(model.DisplayName); label != "" {
		return label
	}
	return model.Model
}
