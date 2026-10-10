package host

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	proxycore "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy"
)

// sessionModelList reports whether the request is a session asking which models
// it may use. Codex's own catalogue request (client_version) has a format of its
// own and is left to CLIProxyAPI.
func sessionModelList(r *http.Request) bool {
	if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
		return false
	}
	_, codexCatalogue := r.URL.Query()["client_version"]
	return !codexCatalogue
}

// serveSessionModels answers a session's model list with the models of the
// account that session is bound to. CLIProxyAPI's own answer covers every
// account it holds, and disguises other providers' models under Claude names for
// a Claude caller; a session can only ever reach its own account, so anything
// else in the list is a model it cannot run. CLIProxyAPI still writes the
// answer, in whichever format the caller asked for; AO only removes entries.
func serveSessionModels(c *gin.Context, authID string) {
	allowed := make(map[string]bool)
	for _, model := range proxycore.GlobalModelRegistry().GetModelsForClient(authID) {
		if model != nil && model.ID != "" {
			allowed[model.ID] = true
		}
	}
	held := &bufferedAnswer{ResponseWriter: c.Writer}
	c.Writer = held
	c.Next()
	c.Writer = held.ResponseWriter

	body, status := held.body.Bytes(), held.status
	if status == 0 {
		status = http.StatusOK
	}
	if status == http.StatusOK {
		body = onlyModels(body, allowed)
	}
	c.Writer.Header().Del("Content-Length")
	c.Writer.WriteHeader(status)
	_, _ = c.Writer.Write(body)
}

// onlyModels keeps the listed entries whose id is allowed. A body that is not a
// model list is returned as it came.
func onlyModels(body []byte, allowed map[string]bool) []byte {
	var list map[string]json.RawMessage
	if json.Unmarshal(body, &list) != nil {
		return body
	}
	var entries []json.RawMessage
	if raw, ok := list["data"]; !ok || json.Unmarshal(raw, &entries) != nil {
		return body
	}
	kept := make([]json.RawMessage, 0, len(entries))
	var first, last *string
	for _, entry := range entries {
		var model struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(entry, &model) != nil || !allowed[model.ID] {
			continue
		}
		kept = append(kept, entry)
		if first == nil {
			first = &model.ID
		}
		id := model.ID
		last = &id
	}
	list["data"], _ = json.Marshal(kept)
	// Anthropic's list names its first and last entry; keep them true.
	for key, id := range map[string]*string{"first_id": first, "last_id": last} {
		if _, present := list[key]; present {
			list[key], _ = json.Marshal(id)
		}
	}
	filtered, err := json.Marshal(list)
	if err != nil {
		return body
	}
	return filtered
}

// bufferedAnswer keeps a handler's answer in memory so it can be edited before it
// is sent.
type bufferedAnswer struct {
	gin.ResponseWriter
	body   bytes.Buffer
	status int
}

func (w *bufferedAnswer) WriteHeader(status int)            { w.status = status }
func (w *bufferedAnswer) WriteHeaderNow()                   {}
func (w *bufferedAnswer) Write(data []byte) (int, error)    { return w.body.Write(data) }
func (w *bufferedAnswer) WriteString(s string) (int, error) { return w.body.WriteString(s) }
func (w *bufferedAnswer) Written() bool                     { return w.status != 0 || w.body.Len() > 0 }
func (w *bufferedAnswer) Size() int                         { return w.body.Len() }
func (w *bufferedAnswer) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}
