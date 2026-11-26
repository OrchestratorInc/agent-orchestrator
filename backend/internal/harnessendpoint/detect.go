package harnessendpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// probeTimeout bounds each request to the endpoint so a slow gateway cannot
// hold a settings save or a model refresh for long.
const probeTimeout = 10 * time.Second

// maxModelsBody caps how much of a model listing is read.
const maxModelsBody = 1 << 20

// maxListedModels caps how many models one endpoint can contribute.
const maxListedModels = 500

// ErrKeyRejected reports an endpoint that refused the key in every auth style
// its harness can send.
var ErrKeyRejected = errors.New("the endpoint rejected this key")

// ErrNoModelListing reports an endpoint, or a harness spec, without a model
// listing AO can read.
var ErrNoModelListing = errors.New("the endpoint does not list its models")

// ProbeClient never follows redirects: a redirect could forward the key, and
// Go keeps custom headers such as x-api-key across hosts.
var ProbeClient = &http.Client{
	Timeout: probeTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// Detection is what asking an endpoint's model listing on save found.
type Detection struct {
	// Style is the auth style the endpoint accepted, or the harness default
	// when the endpoint could not tell.
	Style AuthStyle
	// Models are the model IDs the endpoint listed, in its order. Empty when
	// the endpoint does not list models.
	Models []string
}

// Detect asks agentID's model listing on the endpoint with each auth style the
// harness supports, the default first. A 2xx answer picks that style and
// returns the models it lists; 401 or 403 tries the next style. Any other
// answer, or no answer, means the endpoint cannot tell (it may not serve the
// path), so the default style is kept with no models. Only when every style is
// refused does it fail with ErrKeyRejected. baseURL must already be validated:
// the key goes to it.
func Detect(ctx context.Context, client *http.Client, agentID, baseURL, key string) (Detection, error) {
	spec, ok := specs[agentID]
	if !ok {
		return Detection{}, ErrUnsupported
	}
	fallback := Detection{Style: spec.DefaultAuthStyle}
	if spec.ModelsPath == "" || !secureEndpoint(baseURL) {
		return fallback, nil
	}
	for _, style := range spec.probeOrder() {
		models, err := listModels(ctx, client, spec, baseURL, style, key)
		switch {
		case err == nil:
			return Detection{Style: style, Models: models}, nil
		case errors.Is(err, ErrKeyRejected):
			continue
		case ctx.Err() != nil:
			return Detection{}, ctx.Err()
		default:
			return fallback, nil
		}
	}
	return Detection{}, ErrKeyRejected
}

// ListModels returns the model IDs agentID's endpoint lists for a key sent in
// style, in the endpoint's order. It fails with ErrKeyRejected on 401 or 403
// and ErrNoModelListing when the endpoint or spec has no listing. Errors never
// carry the key.
func ListModels(ctx context.Context, client *http.Client, agentID, baseURL string, style AuthStyle, key string) ([]string, error) {
	spec, ok := specs[agentID]
	if !ok {
		return nil, ErrUnsupported
	}
	if spec.ModelsPath == "" {
		return nil, ErrNoModelListing
	}
	if !secureEndpoint(baseURL) {
		return nil, &ValidationError{Message: "Use https:// for a remote endpoint."}
	}
	if _, ok := spec.KeyEnv[style]; !ok {
		style = spec.DefaultAuthStyle
	}
	return listModels(ctx, client, spec, baseURL, style, key)
}

// probeOrder is the default style, then the rest in stable order.
func (s Spec) probeOrder() []AuthStyle {
	out := []AuthStyle{s.DefaultAuthStyle}
	for _, style := range s.AuthStyles() {
		if style != s.DefaultAuthStyle {
			out = append(out, style)
		}
	}
	return out
}

func listModels(ctx context.Context, client *http.Client, spec Spec, baseURL string, style AuthStyle, key string) ([]string, error) {
	if client == nil {
		client = ProbeClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+spec.ModelsPath, http.NoBody)
	if err != nil {
		return nil, errors.New("build the model listing request")
	}
	request.Header.Set("Accept", "application/json")
	for name, value := range spec.ModelsHeaders {
		request.Header.Set(name, value)
	}
	switch style {
	case AuthAPIKey:
		request.Header.Set("x-api-key", key)
	default:
		request.Header.Set("Authorization", "Bearer "+key)
	}
	response, err := client.Do(request)
	if err != nil {
		// Transport errors name the URL, never the headers, so the key stays out.
		return nil, fmt.Errorf("list endpoint models: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxModelsBody))
	switch {
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		return nil, ErrKeyRejected
	case response.StatusCode < 200 || response.StatusCode >= 300:
		return nil, fmt.Errorf("%w: status %d", ErrNoModelListing, response.StatusCode)
	case readErr != nil:
		return nil, fmt.Errorf("read endpoint models: %w", readErr)
	}
	return parseModelIDs(body), nil
}

// parseModelIDs reads the OpenAI- and Anthropic-style listing shape,
// {"data":[{"id":"..."}]}, keeping the endpoint's order and dropping blank,
// duplicate or malformed IDs. A body in another shape lists nothing.
func parseModelIDs(body []byte) []string {
	var listing struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &listing) != nil {
		return nil
	}
	seen := make(map[string]bool, len(listing.Data))
	out := make([]string, 0, len(listing.Data))
	for _, entry := range listing.Data {
		id := strings.TrimSpace(entry.ID)
		if id == "" || seen[id] || len(id) > 256 || strings.ContainsAny(id, " \t\r\n\x00") {
			continue
		}
		seen[id] = true
		out = append(out, id)
		if len(out) == maxListedModels {
			break
		}
	}
	return out
}
