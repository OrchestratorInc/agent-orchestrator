package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/gmi"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
)

// The GMI Cloud tools `ao mcp` offers.
//
// The tools appear only when AO holds a GMI Cloud API key. Without one the
// catalog cannot be read and every call would fail with an authentication
// error the agent has no way to fix, so an unconfigured account is not given
// tools it cannot use.
//
// The key never reaches the agent's environment. This server reads it from
// AO's provider key store, named by AO_DATA_DIR, and sends it to GMI Cloud as
// a bearer token. It is absent from the tool results, from the logs, and
// from the process environment of the harness that started this server.
const gmiAPIKeyName = providerkeys.ToolKeyGMI

// Bounds on what one tool call may do.
const (
	// gmiMaxSearchResults is how many models one search returns.
	gmiMaxSearchResults = 50
	// gmiDefaultSearchResults is how many models one search returns unless
	// the caller asks for more.
	gmiDefaultSearchResults = 10
	// gmiMaxListResults is how many requests one listing returns.
	gmiMaxListResults = 100
	// gmiDefaultListResults is how many requests one listing returns unless
	// the caller asks for more.
	gmiDefaultListResults = 20
	// gmiMaxUploadBytes bounds one uploaded file.
	gmiMaxUploadBytes = 100 << 20
	// gmiDetailConcurrency bounds how many model descriptions one search
	// fetches at once.
	gmiDetailConcurrency = 6
)

// gmiToolArgs are the arguments the GMI tools share. Every tool reads the
// subset it needs, so one decoder serves them all.
type gmiToolArgs struct {
	ModelID    string         `json:"model_id"`
	RequestID  string         `json:"request_id"`
	Prompt     string         `json:"prompt"`
	Query      string         `json:"query"`
	Modality   string         `json:"modality"`
	Provider   string         `json:"provider"`
	Path       string         `json:"path"`
	URL        string         `json:"url"`
	FileType   string         `json:"file_type"`
	Limit      int            `json:"limit"`
	N          int            `json:"n"`
	Duration   float64        `json:"duration"`
	Parameters map[string]any `json:"parameters"`
}

// gmiTools returns the GMI Cloud tools this server offers, or none when AO
// holds no GMI Cloud API key. The list is decided once per server and then
// reused.
func (s *server) gmiTools(state *mcpState) []map[string]any {
	if !mcpToolGroupNamed(config.MCPToolGroupGMI) {
		return nil
	}
	if state.gmiLoaded {
		return state.gmiTools
	}
	client, err := gmiClient()
	if err != nil {
		// No key, or a key store that cannot be read: the tools are simply
		// not offered. The agent is not told a credential is missing.
		return nil
	}
	state.gmiLoaded = true
	state.gmiClient = client
	state.gmiNames = map[string]bool{}
	for _, tool := range gmiToolDefs {
		name, _ := tool["name"].(string)
		state.gmiNames[name] = true
		state.gmiTools = append(state.gmiTools, tool)
	}
	return state.gmiTools
}

// gmiClient builds a GMI Cloud client from the key in AO's provider key
// store. The key stays in this process: it is never returned to the caller
// and never placed in a child process's environment.
func gmiClient() (*gmi.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	keys, err := providerkeys.Load(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	key := strings.TrimSpace(keys[gmiAPIKeyName])
	if key == "" {
		return nil, gmi.ErrNoKey
	}
	return gmi.New(key)
}

// gmiToolDefs are the GMI Cloud tools, in the order they are advertised.
var gmiToolDefs = []map[string]any{
	{
		"name":  "gmi_search_models",
		"title": "Search GMI Cloud models",
		"description": "Find the image, video and audio models GMI Cloud offers. " +
			"Returns each model's modality, provider, availability and published price. " +
			"Call gmi_get_model for one model's own parameter list before generating with it.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Text to match against model ids and names, such as \"kling\" or \"seedream\". Omit to list everything.",
				},
				"modality": map[string]any{
					"type": "string", "enum": []string{"image", "video", "audio"},
					"description": "Keep only models of this modality.",
				},
				"provider": map[string]any{
					"type":        "string",
					"description": "Keep only models from this provider, such as \"bria\" or \"bytedance\".",
				},
				"limit": map[string]any{
					"type": "integer", "minimum": 1, "maximum": gmiMaxSearchResults,
					"description": fmt.Sprintf("How many models to return, 1-%d. Defaults to %d.", gmiMaxSearchResults, gmiDefaultSearchResults),
				},
			},
		},
		"annotations": map[string]any{
			"title": "Search GMI Cloud models", "readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true,
		},
	},
	{
		"name":  "gmi_get_model",
		"title": "Get a GMI Cloud model",
		"description": "Describe one GMI Cloud model: its parameter list with types, required flags, defaults and allowed values, " +
			"its published price, and whether it is available. " +
			"Read it before generating: each model takes a different set of parameters, and gmi_submit_generation sends them as given.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"model_id": map[string]any{
					"type": "string", "minLength": 1,
					"description": "The model id, as gmi_search_models returned it.",
				},
			},
			"required": []string{"model_id"},
		},
		"annotations": map[string]any{
			"title": "Get a GMI Cloud model", "readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true,
		},
	},
	{
		"name":  "gmi_estimate_generation",
		"title": "Estimate a GMI Cloud generation",
		"description": "Estimate what one generation with a model costs, at the catalog's list price, before spending it. " +
			"Prices are published per image, per request, per second of video or per thousand characters. " +
			"A model whose price the catalog does not publish has no estimate; the call fails rather than reporting a price of zero.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"model_id": map[string]any{
					"type": "string", "minLength": 1,
					"description": "The model to price.",
				},
				"n": map[string]any{
					"type": "integer", "minimum": 1,
					"description": "How many outputs the generation produces. Defaults to 1.",
				},
				"duration": map[string]any{
					"type": "number", "minimum": 0,
					"description": "Length of a video in seconds. Video models are priced per second; the catalog's default length is assumed when this is omitted.",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "The prompt, for models priced per thousand characters.",
				},
			},
			"required": []string{"model_id"},
		},
		"annotations": map[string]any{
			"title": "Estimate a GMI Cloud generation", "readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true,
		},
	},
	{
		"name":  "gmi_submit_generation",
		"title": "Submit a GMI Cloud generation",
		"description": "Submit a generation to GMI Cloud and return the request id that tracks it. " +
			"This spends the account's money, so estimate the cost with gmi_estimate_generation first when the price is not already known. " +
			"The generation runs asynchronously: poll it with gmi_get_generation. " +
			"Pass the model's own parameters, read from gmi_get_model, under \"parameters\"; a parameter the model does not accept is reported back as dropped.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"model_id": map[string]any{
					"type": "string", "minLength": 1,
					"description": "The model to generate with.",
				},
				"prompt": map[string]any{
					"type": "string", "minLength": 1,
					"description": "What to generate.",
				},
				"parameters": map[string]any{
					"type": "object",
					"description": "The model's own parameters, with the names and values gmi_get_model published. " +
						"An aspect ratio such as \"16:9\" is mapped onto a size the model accepts when it names sizes rather than ratios.",
				},
			},
			"required": []string{"model_id", "prompt"},
		},
		"annotations": map[string]any{
			// Not read-only: this submits paid work.
			"title": "Submit a GMI Cloud generation", "readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": true,
		},
	},
	{
		"name":  "gmi_list_generations",
		"title": "List GMI Cloud generations",
		"description": "List the account's recent generation requests with their model, status and media urls. " +
			"Use it to find a request id to read, or to see what has been generated.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{
					"type": "integer", "minimum": 1, "maximum": gmiMaxListResults,
					"description": fmt.Sprintf("How many requests to return, 1-%d. Defaults to %d.", gmiMaxListResults, gmiDefaultListResults),
				},
			},
		},
		"annotations": map[string]any{
			"title": "List GMI Cloud generations", "readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true,
		},
	},
	{
		"name":  "gmi_get_generation",
		"title": "Get a GMI Cloud generation",
		"description": "Read one generation request: its status, the media it produced, and the error it failed with. " +
			"A finished request carries the media urls to show or save.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"request_id": map[string]any{
					"type": "string", "minLength": 1,
					"description": "The request id gmi_submit_generation returned.",
				},
			},
			"required": []string{"request_id"},
		},
		"annotations": map[string]any{
			"title": "Get a GMI Cloud generation", "readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true,
		},
	},
	{
		"name":  "gmi_upload_media",
		"title": "Upload media to GMI Cloud",
		"description": "Upload a local file so it can be used as the input of a generation, and return its public url. " +
			"Pass that url as the model's image, video or reference parameter. " +
			"Accepted files are jpeg, jpg, png, mp4, mp3 and wav.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path of the local file to upload.",
				},
				"url": map[string]any{
					"type":        "string",
					"description": "Address of a remote file to upload instead of a local path.",
				},
				"file_type": map[string]any{
					"type": "string", "enum": []string{"jpeg", "jpg", "png", "mp4", "mp3", "wav"},
					"description": "The file's type. Taken from the file's extension when this is omitted.",
				},
			},
		},
		"annotations": map[string]any{
			"title": "Upload media to GMI Cloud", "readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": true,
		},
	},
	{
		"name":  "gmi_get_account_summary",
		"title": "Get the GMI Cloud account summary",
		"description": "Summarize what this account has been generating: how many requests it has, how they ended, and what they cost at the catalog's list price. " +
			"The organization balance and promotion credit are not exposed through the API key, so they are not reported here.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{
					"type": "integer", "minimum": 1, "maximum": gmiMaxListResults,
					"description": fmt.Sprintf("How many recent requests to summarize, 1-%d. Defaults to %d.", gmiMaxListResults, gmiDefaultListResults),
				},
			},
		},
		"annotations": map[string]any{
			"title": "Get the GMI Cloud account summary", "readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true,
		},
	},
}

// callGMI runs one GMI Cloud tool.
func (s *server) callGMI(ctx context.Context, state *mcpState, name string, raw json.RawMessage) map[string]any {
	switch name {
	case "gmi_search_models":
		return s.callGMISearchModels(ctx, state, raw)
	case "gmi_get_model":
		return s.callGMIGetModel(ctx, state, raw)
	case "gmi_estimate_generation":
		return s.callGMIEstimateGeneration(ctx, state, raw)
	case "gmi_submit_generation":
		return s.callGMISubmitGeneration(ctx, state, raw)
	case "gmi_list_generations":
		return s.callGMIListGenerations(ctx, state, raw)
	case "gmi_get_generation":
		return s.callGMIGetGeneration(ctx, state, raw)
	case "gmi_upload_media":
		return s.callGMIUploadMedia(ctx, state, raw)
	case "gmi_get_account_summary":
		return s.callGMIGetAccountSummary(ctx, state, raw)
	}
	return mcpToolError(fmt.Errorf("unknown GMI Cloud tool %q", name))
}

// callGMISearchModels finds the models GMI Cloud offers.
func (s *server) callGMISearchModels(ctx context.Context, state *mcpState, raw json.RawMessage) map[string]any {
	client, err := gmiClientFor(state)
	if err != nil {
		return mcpToolError(err)
	}
	args, err := decodeGMIToolArgs(raw)
	if err != nil {
		return mcpToolError(err)
	}
	models, err := client.ListModels(ctx)
	if err != nil {
		return mcpToolError(err)
	}
	matched := make([]gmi.Model, 0, len(models))
	for _, model := range models {
		if !gmiModelMatches(model, args.Query, args.Modality, args.Provider) {
			continue
		}
		matched = append(matched, model)
	}
	limit := args.Limit
	if limit <= 0 {
		limit = gmiDefaultSearchResults
	}
	if limit > gmiMaxSearchResults {
		limit = gmiMaxSearchResults
	}
	if len(matched) > limit {
		matched = matched[:limit]
	}
	// The catalog answers with bare model ids, so the descriptions and
	// prices have to be fetched for the models this call returns.
	gmiFillModelDetails(ctx, client, matched)
	summaries := make([]map[string]any, 0, len(matched))
	for _, model := range matched {
		summaries = append(summaries, gmiModelSummary(model))
	}
	return mcpJSONResult(map[string]any{
		"models": summaries,
		"count":  len(summaries),
	})
}

// callGMIGetModel describes one model.
func (s *server) callGMIGetModel(ctx context.Context, state *mcpState, raw json.RawMessage) map[string]any {
	client, err := gmiClientFor(state)
	if err != nil {
		return mcpToolError(err)
	}
	args, err := decodeGMIToolArgs(raw)
	if err != nil {
		return mcpToolError(err)
	}
	if strings.TrimSpace(args.ModelID) == "" {
		return mcpToolError(fmt.Errorf("model_id is required"))
	}
	model, err := client.GetModel(ctx, args.ModelID)
	if err != nil {
		return mcpToolError(err)
	}
	detail := gmiModelSummary(*model)
	detail["parameters"] = gmiModelParameters(*model)
	detail["brief_description"] = model.Description
	return mcpJSONResult(detail)
}

// callGMIEstimateGeneration prices one generation.
func (s *server) callGMIEstimateGeneration(ctx context.Context, state *mcpState, raw json.RawMessage) map[string]any {
	client, err := gmiClientFor(state)
	if err != nil {
		return mcpToolError(err)
	}
	args, err := decodeGMIToolArgs(raw)
	if err != nil {
		return mcpToolError(err)
	}
	if strings.TrimSpace(args.ModelID) == "" {
		return mcpToolError(fmt.Errorf("model_id is required"))
	}
	model, err := client.GetModel(ctx, args.ModelID)
	if err != nil {
		return mcpToolError(err)
	}
	estimate, err := gmi.EstimateCost(*model, gmi.EstimateOptions{
		Count:       args.N,
		Duration:    args.Duration,
		HasDuration: args.Duration > 0,
		Text:        args.Prompt,
	})
	if err != nil {
		return mcpToolError(err)
	}
	return mcpJSONResult(estimate)
}

// callGMISubmitGeneration queues one generation.
func (s *server) callGMISubmitGeneration(ctx context.Context, state *mcpState, raw json.RawMessage) map[string]any {
	client, err := gmiClientFor(state)
	if err != nil {
		return mcpToolError(err)
	}
	args, err := decodeGMIToolArgs(raw)
	if err != nil {
		return mcpToolError(err)
	}
	if strings.TrimSpace(args.ModelID) == "" {
		return mcpToolError(fmt.Errorf("model_id is required"))
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return mcpToolError(fmt.Errorf("prompt is required"))
	}
	model, err := client.GetModel(ctx, args.ModelID)
	if err != nil {
		return mcpToolError(err)
	}
	payload, dropped := model.BuildPayload(args.Prompt, args.Parameters)
	if len(payload) == 0 {
		return mcpToolError(fmt.Errorf("the model needs a prompt, and none was given"))
	}
	if missing := model.MissingRequired(payload); len(missing) > 0 {
		return mcpToolError(fmt.Errorf("the model requires the parameter(s) %s, which were not supplied", strings.Join(missing, ", ")))
	}
	requestID, err := client.Submit(ctx, args.ModelID, payload)
	if err != nil {
		return mcpToolError(err)
	}
	result := map[string]any{
		"request_id": requestID,
		"model_id":   args.ModelID,
		"status":     "queued",
		"note":       "the generation runs asynchronously; read it with gmi_get_generation",
	}
	if len(dropped) > 0 {
		result["dropped_parameters"] = dropped
	}
	// An unpublished price is reported as such. It is not a price of zero,
	// and saying so keeps a caller from treating the generation as free.
	estimate, estimateErr := gmi.EstimateCost(*model, gmi.EstimateOptions{
		Count:       args.N,
		Duration:    args.Duration,
		HasDuration: args.Duration > 0,
		Text:        args.Prompt,
	})
	if estimateErr != nil {
		result["estimate"] = nil
		result["estimate_note"] = estimateErr.Error()
	} else {
		result["estimate"] = estimate
	}
	return mcpJSONResult(result)
}

// callGMIListGenerations lists recent generation requests.
func (s *server) callGMIListGenerations(ctx context.Context, state *mcpState, raw json.RawMessage) map[string]any {
	client, err := gmiClientFor(state)
	if err != nil {
		return mcpToolError(err)
	}
	args, err := decodeGMIToolArgs(raw)
	if err != nil {
		return mcpToolError(err)
	}
	limit := args.Limit
	if limit <= 0 {
		limit = gmiDefaultListResults
	}
	if limit > gmiMaxListResults {
		limit = gmiMaxListResults
	}
	requests, err := client.ListRequests(ctx, limit)
	if err != nil {
		return mcpToolError(err)
	}
	items := make([]map[string]any, 0, len(requests))
	for _, request := range requests {
		items = append(items, gmiRequestSummary(request))
	}
	return mcpJSONResult(map[string]any{"generations": items, "count": len(items)})
}

// callGMIGetGeneration reads one generation request.
func (s *server) callGMIGetGeneration(ctx context.Context, state *mcpState, raw json.RawMessage) map[string]any {
	client, err := gmiClientFor(state)
	if err != nil {
		return mcpToolError(err)
	}
	args, err := decodeGMIToolArgs(raw)
	if err != nil {
		return mcpToolError(err)
	}
	if strings.TrimSpace(args.RequestID) == "" {
		return mcpToolError(fmt.Errorf("request_id is required"))
	}
	request, err := client.GetRequest(ctx, args.RequestID)
	if err != nil {
		return mcpToolError(err)
	}
	return mcpJSONResult(gmiRequestSummary(*request))
}

// callGMIUploadMedia uploads one input file and returns its public url.
func (s *server) callGMIUploadMedia(ctx context.Context, state *mcpState, raw json.RawMessage) map[string]any {
	client, err := gmiClientFor(state)
	if err != nil {
		return mcpToolError(err)
	}
	args, err := decodeGMIToolArgs(raw)
	if err != nil {
		return mcpToolError(err)
	}
	data, name, err := gmiReadUploadSource(ctx, args)
	if err != nil {
		return mcpToolError(err)
	}
	fileType := strings.ToLower(strings.TrimSpace(args.FileType))
	if fileType == "" {
		fileType = gmiFileType(name)
	}
	if fileType == "" {
		return mcpToolError(fmt.Errorf("cannot tell what kind of file %q is; pass file_type", name))
	}
	uploadURL, publicURL, err := client.UploadURL(ctx, fileType)
	if err != nil {
		return mcpToolError(err)
	}
	if err := gmi.Upload(ctx, uploadURL, gmiContentType(fileType), data); err != nil {
		return mcpToolError(err)
	}
	return mcpJSONResult(map[string]any{
		"public_url": publicURL,
		"file_type":  fileType,
		"bytes":      len(data),
		"note":       "pass the public url as the model's image, video or reference parameter",
	})
}

// callGMIGetAccountSummary summarizes the account's generation activity.
func (s *server) callGMIGetAccountSummary(ctx context.Context, state *mcpState, raw json.RawMessage) map[string]any {
	client, err := gmiClientFor(state)
	if err != nil {
		return mcpToolError(err)
	}
	args, err := decodeGMIToolArgs(raw)
	if err != nil {
		return mcpToolError(err)
	}
	limit := args.Limit
	if limit <= 0 {
		limit = gmiDefaultListResults
	}
	if limit > gmiMaxListResults {
		limit = gmiMaxListResults
	}
	requests, err := client.ListRequests(ctx, limit)
	if err != nil {
		return mcpToolError(err)
	}
	byStatus := map[string]int{}
	models := map[string]bool{}
	for _, request := range requests {
		status := request.Status
		if status == "" {
			status = "unknown"
		}
		byStatus[status]++
		if request.Model != "" {
			models[request.Model] = true
		}
	}
	estimated, priced, unpriced := gmiEstimateSpend(ctx, client, requests)
	summary := map[string]any{
		"requests_examined":     len(requests),
		"requests_by_status":    byStatus,
		"distinct_models":       len(models),
		"estimated_spend_usd":   estimated,
		"priced_requests":       priced,
		"unpriced_requests":     unpriced,
		"note":                  "the spend is estimated at the catalog's list price and covers only the requests examined",
		"balance_not_available": "GMI Cloud's API key does not expose the organization balance or promotion credit; its MCP server does",
	}
	return mcpJSONResult(summary)
}

// gmiEstimateSpend estimates what the examined requests cost at the
// catalog's list price. Requests whose model has no published price are
// counted separately rather than valued at zero.
func gmiEstimateSpend(ctx context.Context, client *gmi.Client, requests []gmi.Request) (total float64, priced, unpriced int) {
	prices := map[string]*gmi.Model{}
	for _, request := range requests {
		if request.Model == "" {
			continue
		}
		if _, ok := prices[request.Model]; ok {
			continue
		}
		model, err := client.GetModel(ctx, request.Model)
		if err != nil || model == nil {
			continue
		}
		prices[request.Model] = model
	}
	for _, request := range requests {
		model, ok := prices[request.Model]
		if !ok || !model.PriceInfo.HasPublishedPrice() {
			unpriced++
			continue
		}
		estimate, err := gmi.EstimateCost(*model, gmi.EstimateOptions{
			Count:       gmiPayloadInt(request.Payload, "n"),
			Duration:    gmiPayloadFloat(request.Payload, "duration", "durationSeconds", "duration_seconds", "seconds"),
			HasDuration: gmiPayloadFloat(request.Payload, "duration", "durationSeconds", "duration_seconds", "seconds") > 0,
			Text:        gmiPayloadText(request.Payload, "prompt", "text"),
		})
		if err != nil {
			unpriced++
			continue
		}
		total += estimate.TotalUSD
		priced++
	}
	return total, priced, unpriced
}

// gmiFillModelDetails fetches the full description of the models a search
// returned, so the caller sees modality, price and availability rather than
// bare ids. A model whose description cannot be fetched keeps what the
// catalog listed.
func gmiFillModelDetails(ctx context.Context, client *gmi.Client, models []gmi.Model) {
	var wait sync.WaitGroup
	slots := make(chan struct{}, gmiDetailConcurrency)
	for index := range models {
		if models[index].ModelID == "" {
			continue
		}
		wait.Add(1)
		go gmiFetchModelDetail(ctx, client, models, index, slots, &wait)
	}
	wait.Wait()
}

// gmiFetchModelDetail fills in one model's description. It runs in its own
// goroutine and takes a slot, so the number of descriptions fetched at once
// stays bounded.
func gmiFetchModelDetail(ctx context.Context, client *gmi.Client, models []gmi.Model, index int, slots chan struct{}, wait *sync.WaitGroup) {
	defer wait.Done()
	slots <- struct{}{}
	defer func() { <-slots }()
	detail, err := client.GetModel(ctx, models[index].ModelID)
	if err != nil || detail == nil {
		return
	}
	models[index] = *detail
}

// gmiModelMatches keeps the models a search asked for.
func gmiModelMatches(model gmi.Model, query, modality, provider string) bool {
	if modality != "" && !strings.EqualFold(model.Modality(), modality) {
		return false
	}
	if provider != "" && !strings.Contains(strings.ToLower(model.Provider), strings.ToLower(provider)) {
		return false
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return true
	}
	query = strings.ToLower(query)
	return strings.Contains(strings.ToLower(model.ModelID), query) ||
		strings.Contains(strings.ToLower(model.DisplayName), query) ||
		strings.Contains(strings.ToLower(model.Description), query)
}

// gmiModelSummary is the catalog entry a search returns.
func gmiModelSummary(model gmi.Model) map[string]any {
	summary := map[string]any{
		"model_id":        model.ModelID,
		"display_name":    model.DisplayName,
		"modality":        model.Modality(),
		"provider":        model.Provider,
		"status":          model.Status,
		"available":       model.IsAvailable(),
		"price_published": model.PriceInfo.HasPublishedPrice(),
		"unit":            model.PriceInfo.PricingUnit(),
		"unit_price_usd":  nil,
	}
	if model.Description != "" {
		summary["description"] = model.Description
	}
	if model.PriceInfo.HasPublishedPrice() {
		summary["unit_price_usd"] = model.PriceInfo.UnitPriceUSD()
	}
	return summary
}

// gmiModelParameters is the model's own parameter list.
func gmiModelParameters(model gmi.Model) []map[string]any {
	parameters := make([]map[string]any, 0, len(model.Parameters))
	for _, parameter := range model.Parameters {
		entry := map[string]any{
			"name":     parameter.Name,
			"type":     parameter.Type,
			"required": parameter.Required,
		}
		if parameter.Default != nil {
			entry["default"] = parameter.Default
		}
		if len(parameter.Options) > 0 {
			entry["options"] = parameter.Options
		}
		if parameter.Description != "" {
			entry["description"] = parameter.Description
		}
		parameters = append(parameters, entry)
	}
	return parameters
}

// gmiRequestSummary is one generation request.
func gmiRequestSummary(request gmi.Request) map[string]any {
	summary := map[string]any{
		"request_id": request.RequestID,
		"model_id":   request.Model,
		"status":     request.Status,
	}
	if request.CreatedAt != "" {
		summary["created_at"] = request.CreatedAt
	}
	if urls := request.MediaURLs(); len(urls) > 0 {
		summary["media_urls"] = urls
	}
	if request.Outcome != nil {
		if thumbnail := request.Outcome.Thumbnail(); thumbnail != "" {
			summary["thumbnail_url"] = thumbnail
		}
	}
	if request.Error != "" {
		summary["error"] = request.Error
	}
	return summary
}

// gmiReadUploadSource reads the file one upload call sends, from a local
// path or from a remote address.
func gmiReadUploadSource(ctx context.Context, args gmiToolArgs) ([]byte, string, error) {
	path := strings.TrimSpace(args.Path)
	remote := strings.TrimSpace(args.URL)
	switch {
	case path != "" && remote != "":
		return nil, "", fmt.Errorf("pass either path or url, not both")
	case path != "":
		info, err := os.Stat(path)
		if err != nil {
			return nil, "", fmt.Errorf("read %s: %w", path, err)
		}
		if info.IsDir() {
			return nil, "", fmt.Errorf("%s is a directory, not a file", path)
		}
		if info.Size() > gmiMaxUploadBytes {
			return nil, "", fmt.Errorf("%s is %d bytes, over the %d-byte limit", path, info.Size(), gmiMaxUploadBytes)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, "", fmt.Errorf("read %s: %w", path, err)
		}
		return data, filepath.Base(path), nil
	case remote != "":
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, remote, http.NoBody)
		if err != nil {
			return nil, "", fmt.Errorf("read %s: %w", remote, err)
		}
		req.Header.Set("User-Agent", gmi.UserAgent)
		resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
		if err != nil {
			return nil, "", fmt.Errorf("read %s: %w", remote, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil, "", fmt.Errorf("read %s: the server answered HTTP %d", remote, resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, gmiMaxUploadBytes+1))
		if err != nil {
			return nil, "", fmt.Errorf("read %s: %w", remote, err)
		}
		if len(data) > gmiMaxUploadBytes {
			return nil, "", fmt.Errorf("%s is over the %d-byte limit", remote, gmiMaxUploadBytes)
		}
		name := filepath.Base(remote)
		if index := strings.IndexAny(name, "?#"); index >= 0 {
			name = name[:index]
		}
		return data, name, nil
	}
	return nil, "", fmt.Errorf("a file path or a url is required")
}

// gmiFileType returns the type GMI Cloud accepts for a file name.
func gmiFileType(name string) string {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")) {
	case "jpg", "jpeg":
		return "jpeg"
	case "png":
		return "png"
	case "mp4":
		return "mp4"
	case "mp3":
		return "mp3"
	case "wav":
		return "wav"
	}
	return ""
}

// gmiContentType returns the content type to send the bytes of one file with.
func gmiContentType(fileType string) string {
	switch strings.ToLower(fileType) {
	case "jpeg", "jpg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "mp4":
		return "video/mp4"
	case "mp3":
		return "audio/mpeg"
	case "wav":
		return "audio/wav"
	}
	return "application/octet-stream"
}

// gmiPayloadInt reads a whole number out of a request's own payload.
func gmiPayloadInt(payload map[string]any, name string) int {
	value, ok := payload[name]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	}
	return 0
}

// gmiPayloadFloat reads a number out of a request's own payload, under any
// of the names a model might use for it.
func gmiPayloadFloat(payload map[string]any, names ...string) float64 {
	for _, name := range names {
		value, ok := payload[name]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return typed
		case int:
			return float64(typed)
		case string:
			var parsed float64
			if _, err := fmt.Sscanf(strings.TrimSpace(typed), "%g", &parsed); err == nil {
				return parsed
			}
		}
	}
	return 0
}

// gmiPayloadText reads text out of a request's own payload.
func gmiPayloadText(payload map[string]any, names ...string) string {
	for _, name := range names {
		if value, ok := payload[name].(string); ok {
			return value
		}
	}
	return ""
}

// gmiClientFor returns the client a tool call uses, building one when the
// server has not built it yet.
func gmiClientFor(state *mcpState) (*gmi.Client, error) {
	if state.gmiClient != nil {
		return state.gmiClient, nil
	}
	client, err := gmiClient()
	if err != nil {
		return nil, fmt.Errorf("GMI Cloud is not configured in this AO install: %w", err)
	}
	state.gmiClient = client
	return client, nil
}

// decodeGMIToolArgs reads a tool call's arguments.
func decodeGMIToolArgs(raw json.RawMessage) (gmiToolArgs, error) {
	var args gmiToolArgs
	if len(raw) == 0 || string(raw) == "null" {
		return args, nil
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return args, fmt.Errorf("invalid arguments: %w", err)
	}
	return args, nil
}

// mcpJSONResult renders one tool result as JSON text.
func mcpJSONResult(value any) map[string]any {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return mcpToolError(err)
	}
	return map[string]any{"content": []any{mcpText(string(encoded))}}
}
