package catalogsync

import (
	"context"
	"crypto/sha1" //nolint:gosec // A git object id is SHA-1 by definition; it names bytes, it does not protect them.
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
)

const (
	// LiteLLMURL is LiteLLM's live model price table, the one place the daemon reads prices from.
	LiteLLMURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"
	// MaxSourceBytes bounds the price table.
	MaxSourceBytes = 32 << 20
	fetchTimeout   = time.Minute
)

// Catalog builds a runtime catalog from one copy of LiteLLM's price table, with every provider it prices: a harness can bill through any of them.
func Catalog(upstream []byte) (*pricing.Catalog, error) {
	generated, err := build(upstream)
	if err != nil {
		return nil, err
	}
	manifestBytes, err := canonicalJSON(generated.manifest)
	if err != nil {
		return nil, err
	}
	providers := make(map[string][]byte, len(generated.manifest.Providers))
	for _, ref := range generated.manifest.Providers {
		providers[ref.Path] = generated.blobs[ref.ProviderID]
	}
	return pricing.ParseCatalog(manifestBytes, providers)
}

func gitBlobID(contents []byte) string {
	sum := sha1.New() //nolint:gosec // See the import.
	_, _ = io.WriteString(sum, "blob "+strconv.Itoa(len(contents))+"\x00")
	_, _ = sum.Write(contents)
	return hex.EncodeToString(sum.Sum(nil))
}

// Fetcher downloads LiteLLM's price table and builds the catalog from it, so a model LiteLLM prices is priced within a refresh, with no AO release or catalog commit in between.
type Fetcher struct {
	client *http.Client
	url    string
}

// NewFetcher reads the table at url: LiteLLMURL in production, a test server in tests.
func NewFetcher(client *http.Client, url string) *Fetcher {
	clone := *client
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Fetcher{client: &clone, url: url}
}

// Fetch implements pricing.CatalogFetcher.
func (f *Fetcher) Fetch(ctx context.Context, etag string, cacheAvailable bool) (pricing.FetchResult, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, http.NoBody)
	if err != nil {
		return pricing.FetchResult{}, err
	}
	conditional := etag != "" && cacheAvailable
	if conditional {
		request.Header.Set("If-None-Match", etag)
	}
	response, err := f.client.Do(request)
	if err != nil {
		return pricing.FetchResult{}, fmt.Errorf("fetch LiteLLM prices: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	switch {
	case conditional && response.StatusCode == http.StatusNotModified:
		return pricing.FetchResult{ETag: etag, NotModified: true}, nil
	case response.StatusCode != http.StatusOK:
		return pricing.FetchResult{}, fmt.Errorf("fetch LiteLLM prices: unexpected HTTP status %d", response.StatusCode)
	}
	upstream, err := io.ReadAll(io.LimitReader(response.Body, MaxSourceBytes+1))
	if err != nil {
		return pricing.FetchResult{}, fmt.Errorf("read LiteLLM prices: %w", err)
	}
	if len(upstream) > MaxSourceBytes {
		return pricing.FetchResult{}, fmt.Errorf("LiteLLM prices exceed %d bytes", MaxSourceBytes)
	}
	catalog, err := Catalog(upstream)
	if err != nil {
		return pricing.FetchResult{}, fmt.Errorf("build pricing catalog from LiteLLM: %w", err)
	}
	return pricing.FetchResult{Catalog: catalog, ETag: response.Header.Get("ETag")}, nil
}
