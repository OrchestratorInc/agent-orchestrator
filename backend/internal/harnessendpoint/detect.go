package harnessendpoint

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// probeTimeout bounds each auth-style probe so a slow gateway cannot hold a
// settings save for long; an endpoint that does not answer in time keeps the
// default style.
const probeTimeout = 10 * time.Second

// ErrKeyRejected reports an endpoint that refused the key in every auth style
// its harness can send.
var ErrKeyRejected = errors.New("the endpoint rejected this key")

// ProbeClient never follows redirects: a redirect could forward the key, and
// Go keeps custom headers such as x-api-key across hosts.
var ProbeClient = &http.Client{
	Timeout: probeTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// DetectAuthStyle finds the auth style an endpoint accepts for agentID's
// harness by asking its probe path with each style in turn, the default first.
// A 2xx answer picks that style; 401 or 403 tries the next. Any other answer,
// or no answer, means the endpoint cannot tell (it may not serve the path), so
// the default is kept. Only when every style is refused does it fail with
// ErrKeyRejected. baseURL must already be validated: the key goes to it.
func DetectAuthStyle(ctx context.Context, client *http.Client, agentID, baseURL, key string) (AuthStyle, error) {
	spec, ok := specs[agentID]
	if !ok {
		return "", ErrUnsupported
	}
	styles := spec.probeOrder()
	if len(styles) < 2 || spec.ProbePath == "" || !secureEndpoint(baseURL) {
		return spec.DefaultAuthStyle, nil
	}
	if client == nil {
		client = ProbeClient
	}
	for _, style := range styles {
		status, err := probe(ctx, client, spec, baseURL, style, key)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return spec.DefaultAuthStyle, nil
		}
		switch {
		case status >= 200 && status < 300:
			return style, nil
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			continue
		default:
			return spec.DefaultAuthStyle, nil
		}
	}
	return "", ErrKeyRejected
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

func probe(ctx context.Context, client *http.Client, spec Spec, baseURL string, style AuthStyle, key string) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+spec.ProbePath, http.NoBody)
	if err != nil {
		return 0, err
	}
	for name, value := range spec.ProbeHeaders {
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
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	return response.StatusCode, nil
}
