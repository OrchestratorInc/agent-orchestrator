package mcpserver

import (
	"fmt"
	"strings"
)

// Limits and defaults for a rendered page.
const (
	MaxRenderFileBytes  = 1 << 20
	DefaultRenderHeight = 400
	DefaultRenderWidth  = 720
)

// RenderRequest publishes a page to the session thread.
type RenderRequest struct {
	HTML     string `json:"html"`
	Title    string `json:"title"`
	Height   int    `json:"height"`
	Artifact bool   `json:"artifact,omitempty"`
}

// RenderResponse names the published render and any artifact kept.
type RenderResponse struct {
	RenderID      string `json:"renderId"`
	ActivityID    string `json:"activityId"`
	Path          string `json:"path"`
	ArtifactPath  string `json:"artifactPath"`
	ArtifactError string `json:"artifactError"`
}

// RenderCheckRequest asks the desktop app to screenshot a page.
type RenderCheckRequest struct {
	HTML  string `json:"html"`
	Width int    `json:"width"`
}

// RenderCheckResponse is the screenshot and what the page reported.
type RenderCheckResponse struct {
	Screenshot struct {
		Data   string `json:"data"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	} `json:"screenshot"`
	ContentHeight   int `json:"contentHeight"`
	ConsoleMessages []struct {
		Level string `json:"level"`
		Text  string `json:"text"`
	} `json:"consoleMessages"`
	Network string `json:"network"`
}

// CheckOfflineNote tells the agent why remote resources did not load
// in a check that ran without network.
const CheckOfflineNote = "The check ran without network access, as your sandbox has none. Remote resources did not load."

// ShownText tells the agent its page is on screen.
func ShownText(renderID string) string {
	return fmt.Sprintf("Shown above your reply (render %s). Do not describe the page; add only what it does not say.", renderID)
}

// ArtifactLine reports where a publish kept the page as an artifact, or
// why it did not; it is empty when no artifact was asked for. A kept page is not
// registered, and in a session that registered anything else it is not listed,
// so the line gives the agent the command while it is still acting on the render.
func ArtifactLine(resp RenderResponse) string {
	switch {
	case resp.ArtifactPath != "":
		return "saved as artifact: " + resp.ArtifactPath + "\nto list it as an artifact, run: ao artifact add " + quoteShellArg(resp.ArtifactPath)
	case resp.ArtifactError != "":
		return "warning: not saved as artifact: " + resp.ArtifactError
	}
	return ""
}

// quoteShellArg single-quotes value for a POSIX shell. The path it quotes holds
// the page title, which can repeat text from a document, and the agent runs what
// it is given, so double quotes would leave "$(...)" and backticks live.
func quoteShellArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
