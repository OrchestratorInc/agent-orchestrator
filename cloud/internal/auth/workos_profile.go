package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	workOSAPIBaseURL      = "https://api.workos.com"
	maxWorkOSCacheEntries = 1024
)

type cachedProfile struct {
	profile   WorkOSProfile
	expiresAt time.Time
}

type cachedOrganization struct {
	organization WorkOSOrganization
	expiresAt    time.Time
}

func NewWorkOSProfileResolver(apiKey string, client *http.Client) (ProfileResolver, error) {
	return newWorkOSProfileResolver(apiKey, workOSAPIBaseURL, client)
}

func newWorkOSProfileResolver(
	apiKey string,
	baseURL string,
	client *http.Client,
) (ProfileResolver, error) {
	apiKey = strings.TrimSpace(apiKey)
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if apiKey == "" {
		return nil, errors.New("WorkOS API key is required")
	}
	if baseURL == "" {
		return nil, errors.New("WorkOS API base URL is required")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	var mutex sync.Mutex
	cache := make(map[string]cachedProfile)

	return func(ctx context.Context, userID string) (WorkOSProfile, error) {
		userID = strings.TrimSpace(userID)
		if userID == "" {
			return WorkOSProfile{}, errors.New("WorkOS user ID is required")
		}
		now := time.Now()
		mutex.Lock()
		cached, ok := cache[userID]
		mutex.Unlock()
		if ok && now.Before(cached.expiresAt) {
			return cached.profile, nil
		}

		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			baseURL+"/user_management/users/"+url.PathEscape(userID),
			http.NoBody,
		)
		if err != nil {
			return WorkOSProfile{}, err
		}
		request.Header.Set("Authorization", "Bearer "+apiKey)
		response, err := client.Do(request)
		if err != nil {
			return WorkOSProfile{}, fmt.Errorf("get WorkOS user: %w", err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return WorkOSProfile{}, fmt.Errorf("get WorkOS user: status %d", response.StatusCode)
		}
		var user struct {
			ID         string `json:"id"`
			Email      string `json:"email"`
			FirstName  string `json:"first_name"`
			LastName   string `json:"last_name"`
			ExternalID string `json:"external_id"`
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&user); err != nil {
			return WorkOSProfile{}, err
		}
		if strings.TrimSpace(user.ID) != userID {
			return WorkOSProfile{}, errors.New("WorkOS user response did not match token")
		}
		email := strings.ToLower(strings.TrimSpace(user.Email))
		if email == "" {
			return WorkOSProfile{}, errors.New("WorkOS user has no email")
		}
		displayName := strings.TrimSpace(strings.Join(
			nonEmpty(user.FirstName, user.LastName),
			" ",
		))
		if displayName == "" {
			displayName = email
		}
		profile := WorkOSProfile{
			Email:       email,
			DisplayName: displayName,
			LegacyID:    legacyWorkOSID(user.ExternalID, "user_", userID),
		}
		mutex.Lock()
		trimCache(cache, func(profile cachedProfile) bool {
			return !now.Before(profile.expiresAt)
		})
		cache[userID] = cachedProfile{profile: profile, expiresAt: now.Add(5 * time.Minute)}
		mutex.Unlock()
		return profile, nil
	}, nil
}

// legacyWorkOSID returns the WorkOS ID this record was copied from, or "" when
// external_id is unset, is not a WorkOS ID of the expected kind, or names the
// record itself. Only AO's own API key can set external_id, so a WorkOS ID
// there is the link written by the environment copy, not user input.
func legacyWorkOSID(externalID, prefix, id string) string {
	externalID = strings.TrimSpace(externalID)
	if !strings.HasPrefix(externalID, prefix) || externalID == id {
		return ""
	}
	return externalID
}

func nonEmpty(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func NewWorkOSOrganizationResolver(
	apiKey string,
	client *http.Client,
) (OrganizationResolver, error) {
	return newWorkOSOrganizationResolver(apiKey, workOSAPIBaseURL, client)
}

func newWorkOSOrganizationResolver(
	apiKey string,
	baseURL string,
	client *http.Client,
) (OrganizationResolver, error) {
	apiKey = strings.TrimSpace(apiKey)
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if apiKey == "" || baseURL == "" {
		return nil, errors.New("WorkOS API key and base URL are required")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	var mutex sync.Mutex
	cache := make(map[string]cachedOrganization)

	return func(ctx context.Context, organizationID string) (WorkOSOrganization, error) {
		organizationID = strings.TrimSpace(organizationID)
		if organizationID == "" {
			return WorkOSOrganization{}, errors.New("WorkOS organization ID is required")
		}
		now := time.Now()
		mutex.Lock()
		cached, ok := cache[organizationID]
		mutex.Unlock()
		if ok && now.Before(cached.expiresAt) {
			return cached.organization, nil
		}

		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			baseURL+"/organizations/"+url.PathEscape(organizationID),
			http.NoBody,
		)
		if err != nil {
			return WorkOSOrganization{}, err
		}
		request.Header.Set("Authorization", "Bearer "+apiKey)
		response, err := client.Do(request)
		if err != nil {
			return WorkOSOrganization{}, fmt.Errorf("get WorkOS organization: %w", err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return WorkOSOrganization{}, fmt.Errorf("get WorkOS organization: status %d", response.StatusCode)
		}
		var organization struct {
			ID         string            `json:"id"`
			Name       string            `json:"name"`
			Metadata   map[string]string `json:"metadata"`
			ExternalID string            `json:"external_id"`
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&organization); err != nil {
			return WorkOSOrganization{}, err
		}
		if strings.TrimSpace(organization.ID) != organizationID {
			return WorkOSOrganization{}, errors.New("WorkOS organization response did not match token")
		}
		displayName := strings.TrimSpace(organization.Name)
		if displayName == "" {
			displayName = "WorkOS organization"
		}
		resolved := WorkOSOrganization{
			DisplayName:  displayName,
			Capabilities: parseOrganizationCapabilities(organization.Metadata),
			LegacyID:     legacyWorkOSID(organization.ExternalID, "org_", organizationID),
		}
		mutex.Lock()
		trimCache(cache, func(organization cachedOrganization) bool {
			return !now.Before(organization.expiresAt)
		})
		cache[organizationID] = cachedOrganization{organization: resolved, expiresAt: now.Add(5 * time.Minute)}
		mutex.Unlock()
		return resolved, nil
	}, nil
}

// parseOrganizationCapabilities reads entitlement flags from a WorkOS
// organization's metadata. WorkOS metadata is a flat string map, so capabilities
// are set as a single comma-separated "capabilities" key (e.g. "coder" or
// "coder,feature-x"). Values are lowercased, trimmed, and de-duplicated.
func parseOrganizationCapabilities(metadata map[string]string) []string {
	raw := strings.TrimSpace(metadata["capabilities"])
	if raw == "" {
		return nil
	}
	seen := make(map[string]struct{})
	capabilities := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		capability := strings.ToLower(strings.TrimSpace(part))
		if capability == "" {
			continue
		}
		if _, duplicate := seen[capability]; duplicate {
			continue
		}
		seen[capability] = struct{}{}
		capabilities = append(capabilities, capability)
	}
	if len(capabilities) == 0 {
		return nil
	}
	return capabilities
}

func trimCache[T any](cache map[string]T, expired func(T) bool) {
	if len(cache) < maxWorkOSCacheEntries {
		return
	}
	for key, value := range cache {
		if expired(value) {
			delete(cache, key)
		}
	}
	for len(cache) >= maxWorkOSCacheEntries {
		for key := range cache {
			delete(cache, key)
			break
		}
	}
}
