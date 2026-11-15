package agentcreds

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var ErrInvalidCredential = errors.New("agent credential is invalid or expired")

type State string

const (
	StateValid   State = "valid"
	StateInvalid State = "invalid"
	StateUnknown State = "unknown"
)

type Kind string

const (
	KindAPIKey     Kind = "api_key"
	KindOAuthToken Kind = "oauth_token" //nolint:gosec // Credential kind label, not a credential value.
	KindAuthToken  Kind = "auth_token"
)

type Credential struct {
	Kind             Kind
	Secret           string
	Source           string
	Provider         Provider
	BaseURL          string
	ExpiresAt        time.Time
	Renewable        bool
	RefreshExpiresAt time.Time
}

func (c Credential) ExpiredButRenewable(now time.Time) bool {
	return c.Renewable && !c.ExpiresAt.IsZero() && !now.Before(c.ExpiresAt) &&
		(c.RefreshExpiresAt.IsZero() || now.Before(c.RefreshExpiresAt))
}

func (c Credential) Fingerprint() string {
	secret := strings.TrimSpace(c.Secret)
	if secret == "" {
		return ""
	}
	identity := strings.Join([]string{
		secret,
		string(c.Kind),
		string(c.Provider),
		strings.TrimSpace(c.BaseURL),
	}, "\x00")
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])[:12]
}

type Model struct {
	ID          string
	DisplayName string
	Efforts     []string
}

type Result struct {
	HTTPStatus  int
	State       State
	Provider    Provider
	Source      string
	Fingerprint string
	Models      []Model
	Detail      string
	CheckedAt   time.Time
	Err         error
}

const anthropicAPIVersion = "2023-06-01"

const DefaultTimeout = 3 * time.Second

const maxBodyBytes = 256 << 10

type Validator struct {
	client *http.Client
}

func New(client *http.Client) *Validator {
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	return &Validator{client: client}
}

func (v *Validator) Validate(ctx context.Context, cred Credential) Result {
	result := Result{
		Provider:    cred.Provider,
		Source:      cred.Source,
		Fingerprint: cred.Fingerprint(),
		CheckedAt:   time.Now(),
	}
	provider := cred.Provider
	if provider == "" {
		provider = ProviderFirstParty
	}
	spec, err := v.requestFor(ctx, provider, cred)
	if err != nil {
		if errors.Is(err, ErrInvalidCredential) {
			result.State = StateInvalid
		} else {
			result.State = StateUnknown
		}
		result.Err = err
		result.Detail = err.Error()
		return result
	}
	return v.probe(result, spec)
}

type requestSpec struct {
	request                       *http.Request
	parseModels                   func([]byte) ([]Model, error)
	rateLimitProvesAuthentication bool
	paginateAnthropic             bool
	label                         string
}

func (v *Validator) probe(result Result, spec requestSpec) Result {
	response, err := v.client.Do(spec.request)
	if err != nil {
		result.State = StateUnknown
		result.Err = err
		result.Detail = fmt.Sprintf("could not reach %s: %v", spec.label, err)
		return result
	}
	defer func() { _ = response.Body.Close() }()
	result.HTTPStatus = response.StatusCode
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes))
	if readErr != nil {
		result.State = StateUnknown
		result.Err = readErr
		result.Detail = fmt.Sprintf("could not read %s response: %v", spec.label, readErr)
		return result
	}

	switch response.StatusCode {
	case http.StatusUnauthorized:
		result.State = StateInvalid
		result.Err = ErrInvalidCredential
		result.Detail = rejectionDetail(spec.label, body, spec.request)
		return result
	case http.StatusOK, http.StatusTooManyRequests:
	default:
		result.State = StateUnknown
		result.Detail = fmt.Sprintf("%s returned HTTP %d", spec.label, response.StatusCode)
		return result
	}

	if response.StatusCode == http.StatusTooManyRequests {
		if spec.rateLimitProvesAuthentication {
			result.State = StateValid
			result.Detail = fmt.Sprintf("%s accepted the credential (rate limited)", spec.label)
		} else {
			result.State = StateUnknown
			result.Detail = fmt.Sprintf("%s returned HTTP %d", spec.label, response.StatusCode)
		}
		return result
	}

	if spec.parseModels != nil {
		models, parseErr := spec.parseModels(body)
		if parseErr != nil {
			models = nil
		}
		result.Models = models
		if parseErr == nil && spec.paginateAnthropic {
			var pageErr error
			result.Models, pageErr = v.followAnthropicPages(spec, body, result.Models)
			if pageErr != nil {
				result.State = StateUnknown
				result.Err = pageErr
				result.Detail = fmt.Sprintf("could not read the %s model list: %v", spec.label, pageErr)
				return result
			}
		}
	}
	result.State = StateValid
	result.Detail = fmt.Sprintf("%s accepted the credential", spec.label)
	return result
}

func (v *Validator) followAnthropicPages(spec requestSpec, body []byte, models []Model) ([]Model, error) {
	seen := map[string]struct{}{}
	for {
		hasMore, cursor, err := anthropicPageCursor(body)
		if err != nil || !hasMore {
			return models, err
		}
		if cursor == "" {
			return nil, errors.New("provider reported more models without a cursor")
		}
		if _, duplicate := seen[cursor]; duplicate {
			return nil, fmt.Errorf("provider repeated model cursor %q", cursor)
		}
		seen[cursor] = struct{}{}

		request := spec.request.Clone(spec.request.Context())
		query := request.URL.Query()
		query.Set("after_id", cursor)
		request.URL.RawQuery = query.Encode()
		response, err := v.client.Do(request)
		if err != nil {
			return nil, err
		}
		pageBody, readErr := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes))
		_ = response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("pagination returned HTTP %d", response.StatusCode)
		}
		pageModels, parseErr := spec.parseModels(pageBody)
		if parseErr != nil {
			return nil, parseErr
		}
		models = append(models, pageModels...)
		body = pageBody
	}
}

func rejectionDetail(label string, body []byte, request *http.Request) string {
	if message := providerErrorMessage(body); message != "" {
		for _, header := range []string{"Authorization", "X-Api-Key", "Api-Key"} {
			if request != nil {
				if secret := strings.TrimSpace(request.Header.Get(header)); secret != "" {
					message = strings.ReplaceAll(message, secret, "[redacted]")
					message = strings.ReplaceAll(message, strings.TrimPrefix(secret, "Bearer "), "[redacted]")
				}
			}
		}
		return fmt.Sprintf("%s rejected the credential: %s", label, message)
	}
	return fmt.Sprintf("%s rejected the credential", label)
}

func IsResolverBug(detail string) bool {
	lowered := strings.ToLower(detail)
	for _, phrase := range []string{
		"header is required",
		"missing api key",
		"no credentials",
		"could not be parsed",
	} {
		if strings.Contains(lowered, phrase) {
			return true
		}
	}
	return false
}
