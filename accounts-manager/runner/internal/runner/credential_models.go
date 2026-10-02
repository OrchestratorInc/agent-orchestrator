package runner

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	sdkapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/api"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

type credentialModels map[string][]*cliproxy.ModelInfo

func (r *credentialRuntime) attachExecutors(source *coreauth.Manager) error {
	if r.manager == nil || source == nil || source == r.manager {
		return errors.New("credential executor ownership is invalid")
	}
	executors := make([]coreauth.ProviderExecutor, 0, len(oauthProviders))
	for provider := range oauthProviders {
		executor, exists := source.Executor(provider)
		if !exists || executor == nil || executor.Identifier() != provider {
			return errors.New("credential executor is unavailable")
		}
		executors = append(executors, executor)
	}
	for _, executor := range executors {
		r.manager.RegisterExecutor(executor)
	}
	return nil
}

func loadCredentialModels() (credentialModels, error) {
	models := make(credentialModels)
	// The public SDK exposes its static catalog through this handler only.
	handler := &sdkapi.Handler{}
	router := gin.New()
	router.GET("/:channel", handler.GetStaticModelDefinitions)
	for provider := range oauthProviders {
		response := &catalogResponse{header: make(http.Header)}
		request, err := http.NewRequest(http.MethodGet, "/"+provider, http.NoBody)
		if err != nil {
			return nil, errCredentialStorage
		}
		router.ServeHTTP(response, request)
		var payload struct {
			Models []*cliproxy.ModelInfo `json:"models"`
		}
		if response.status != http.StatusOK || json.Unmarshal(response.body.Bytes(), &payload) != nil || len(payload.Models) == 0 {
			return nil, errCredentialStorage
		}
		models[provider] = payload.Models
	}
	return models, nil
}

func (r *credentialRuntime) registerModels(auth *coreauth.Auth) {
	registry := cliproxy.GlobalModelRegistry()
	if auth.Disabled {
		registry.UnregisterClient(auth.ID)
	} else if models := r.models[auth.Provider]; len(models) != 0 {
		registry.RegisterClient(auth.ID, auth.Provider, models)
	}
	r.manager.RefreshSchedulerEntry(auth.ID)
}

type catalogResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *catalogResponse) Header() http.Header    { return w.header }
func (w *catalogResponse) WriteHeader(status int) { w.status = status }
func (w *catalogResponse) Write(data []byte) (int, error) {
	if w.body.Len()+len(data) > 1<<20 {
		return 0, errCredentialStorage
	}
	return w.body.Write(data)
}
