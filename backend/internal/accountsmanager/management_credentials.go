package accountsmanager

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

const credentialManagementPath = "/ao/internal/credentials" // #nosec G101 -- Public route path, not a credential.

// RenameCredential uses a generation check without changing provider identity.
func (c *ManagementClient) RenameCredential(ctx context.Context, ref, label string, generation uint64) error {
	if generation == 0 || len(label) > 320 {
		return ErrInvalidCredential
	}
	return mapCredentialOperationError(c.doJSON(ctx, "rename credential", http.MethodPatch,
		credentialManagementPath+"/label?"+url.Values{"ref": {ref}}.Encode(), struct {
			Label      string `json:"label"`
			Generation uint64 `json:"generation"`
		}{label, generation}, nil))
}

// SetCredentialDisabled fences subsequent managed requests through this credential.
func (c *ManagementClient) SetCredentialDisabled(ctx context.Context, ref string, disabled bool) error {
	record, err := c.resolveCredential(ctx, ref)
	if err != nil {
		return err
	}
	return mapCredentialOperationError(c.doJSON(ctx, "set credential status", http.MethodPatch,
		credentialManagementPath+"/status?"+url.Values{"ref": {record.AuthIndex}}.Encode(),
		map[string]bool{"disabled": disabled}, nil))
}

// RefreshCredential uses the runner's per-credential refresh coordination.
func (c *ManagementClient) RefreshCredential(ctx context.Context, ref string) (CredentialSummary, error) {
	record, err := c.resolveCredential(ctx, ref)
	if err != nil {
		return CredentialSummary{}, err
	}
	var updated rawCredentialRecord
	if err = c.doJSON(ctx, "refresh credential", http.MethodPost,
		credentialManagementPath+"/refresh?"+url.Values{"ref": {record.AuthIndex}}.Encode(), nil, &updated); err != nil {
		return CredentialSummary{}, mapCredentialOperationError(err)
	}
	if updated.AuthIndex != record.AuthIndex || updated.Provider != record.Provider {
		return CredentialSummary{}, ErrInvalidResponse
	}
	return summaryFromRawCredential(updated), nil
}

// RemoveCredential never rewrites another credential or a configuration file.
func (c *ManagementClient) RemoveCredential(ctx context.Context, ref string) error {
	record, err := c.resolveCredential(ctx, ref)
	if errors.Is(err, ErrCredentialNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	err = mapCredentialOperationError(c.doJSON(ctx, "remove credential", http.MethodDelete,
		credentialManagementPath+"?"+url.Values{"ref": {record.AuthIndex}}.Encode(), nil, nil))
	if errors.Is(err, ErrCredentialNotFound) {
		return nil
	}
	return err
}

func (c *ManagementClient) resolveCredential(ctx context.Context, ref string) (rawCredentialRecord, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return rawCredentialRecord{}, ErrCredentialNotFound
	}
	records, err := c.listRawCredentials(ctx)
	if err != nil {
		return rawCredentialRecord{}, err
	}
	matches := make([]rawCredentialRecord, 0, 1)
	for _, record := range records {
		provider := Provider(strings.ToLower(strings.TrimSpace(record.Provider)))
		if provider == "" {
			provider = Provider(strings.ToLower(strings.TrimSpace(record.Type)))
		}
		if strings.TrimSpace(record.AuthIndex) == ref && validProvider(provider) {
			matches = append(matches, record)
		}
	}
	switch len(matches) {
	case 0:
		return rawCredentialRecord{}, ErrCredentialNotFound
	case 1:
		return matches[0], nil
	default:
		return rawCredentialRecord{}, ErrCredentialConflict
	}
}

func mapCredentialOperationError(err error) error {
	if err == nil {
		return nil
	}
	var statusErr *ManagementStatusError
	if !errors.As(err, &statusErr) {
		return err
	}
	switch statusErr.StatusCode {
	case http.StatusNotFound:
		return ErrCredentialNotFound
	case http.StatusConflict:
		return ErrCredentialConflict
	case http.StatusNotImplemented:
		return ErrOperationUnsupported
	case http.StatusUnprocessableEntity:
		return ErrInvalidCredential
	case http.StatusServiceUnavailable:
		return ErrUnavailable
	case http.StatusFailedDependency:
		return ErrVerificationUnavailable
	default:
		return err
	}
}
