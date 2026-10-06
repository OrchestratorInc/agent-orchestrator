package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const testingCapabilityHeader = "X-AO-Test-Capability"

// Keep these schemas in sync with the checkpoint 0 daemon tool contract.
var testingMCPTools = []struct {
	name, description, schema string
}{
	{"screenshot", "Capture the attempt's bound window at original resolution.", `{"type":"object","properties":{},"additionalProperties":false}`},
	{"click", "Click a pixel in a saved screenshot of the bound window.", `{"type":"object","properties":{"screenshotId":{"type":"string","minLength":1},"x":{"type":"integer","minimum":0},"y":{"type":"integer","minimum":0},"button":{"enum":["left","right","middle"]}},"required":["screenshotId","x","y"],"additionalProperties":false}`},
	{"type", "Focus a screenshot pixel and enter text in the bound window.", `{"type":"object","properties":{"screenshotId":{"type":"string","minLength":1},"x":{"type":"integer","minimum":0},"y":{"type":"integer","minimum":0},"text":{"type":"string","maxLength":16384}},"required":["screenshotId","x","y","text"],"additionalProperties":false}`},
	{"key", "Send a key or chord to the bound window.", `{"type":"object","properties":{"screenshotId":{"type":"string","minLength":1},"keys":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":4}},"required":["screenshotId","keys"],"additionalProperties":false}`},
	{"read_target_logs", "Read bounded target logs. maxBytes defaults to 65536.", `{"type":"object","properties":{"cursor":{"type":"string","maxLength":256},"maxBytes":{"type":"integer","minimum":1,"maximum":262144}},"additionalProperties":false}`},
	{"target_daemon_query", "Read projects or sessions from the bound target daemon.", `{"type":"object","properties":{"resource":{"enum":["projects","sessions"]}},"required":["resource"],"additionalProperties":false}`},
	{"submit_report", "Save the attempt outcome and Markdown report, up to 64 KiB.", `{"type":"object","properties":{"outcome":{"enum":["reproduced","not_reproduced","needs_information","environment_blocked","partial","cancelled"]},"markdown":{"type":"string","maxLength":65536}},"required":["outcome","markdown"],"additionalProperties":false}`},
}

func newTestingMCPCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve the attempt's testing tools over stdio",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			for _, name := range []string{"AO_TEST_CAPABILITY", "AO_TEST_ATTEMPT_ID", "AO_SESSION_ID", "AO_RUN_FILE"} {
				if strings.TrimSpace(os.Getenv(name)) == "" {
					return fmt.Errorf("testing mcp requires %s in its launch environment", name)
				}
			}
			capability := os.Getenv("AO_TEST_CAPABILITY")
			attemptID := os.Getenv("AO_TEST_ATTEMPT_ID")
			server := ctx.newTestingMCPServer(attemptID, os.Getenv("AO_SESSION_ID"), capability)
			reader, ok := cmd.InOrStdin().(io.ReadCloser)
			if !ok {
				reader = io.NopCloser(cmd.InOrStdin())
			}
			err := server.Run(cmd.Context(), &mcp.IOTransport{
				Reader: reader, Writer: testingMCPWriter{cmd.OutOrStdout()},
			})
			if err != nil {
				return errors.New(redactTestingCapability(err.Error(), capability))
			}
			return nil
		},
	}
}

type testingMCPWriter struct{ io.Writer }

func (testingMCPWriter) Close() error { return nil }

func redactTestingCapability(text, capability string) string {
	return strings.ReplaceAll(text, capability, "[redacted]")
}

func (c *commandContext) newTestingMCPServer(attemptID, sessionID, capability string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "ao-testing", Version: Version}, nil)
	// SDK validation errors can quote invalid argument values, so redact them too.
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if toolResult, ok := result.(*mcp.CallToolResult); ok {
				for _, content := range toolResult.Content {
					if text, ok := content.(*mcp.TextContent); ok {
						text.Text = redactTestingCapability(text.Text, capability)
					}
				}
			}
			if err != nil && strings.Contains(err.Error(), capability) {
				err = errors.New(redactTestingCapability(err.Error(), capability))
			}
			return result, err
		}
	})
	// Do not allow redirects to move the capability outside the loopback daemon.
	client := *c.deps.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	forward := &commandContext{deps: c.deps}
	forward.deps.HTTPClient = &client
	for _, tool := range testingMCPTools {
		mcp.AddTool(server, &mcp.Tool{
			Name: tool.name, Description: tool.description, InputSchema: json.RawMessage(tool.schema),
		}, func(ctx context.Context, req *mcp.CallToolRequest, input map[string]any) (*mcp.CallToolResult, any, error) {
			if tool.name == "submit_report" {
				markdown, ok := input["markdown"].(string)
				if !ok || len(markdown) > 64<<10 {
					return nil, nil, errors.New("report markdown must be a string of at most 64 KiB")
				}
			}
			arguments := req.Params.Arguments
			if len(arguments) == 0 {
				arguments = json.RawMessage(`{}`)
			}
			var response json.RawMessage
			err := forward.doJSONPathWithHeaders(ctx, http.MethodPost,
				"/api/v1/testing/attempts/"+url.PathEscape(attemptID)+"/tools/"+tool.name,
				testingToolRequestDTO{SessionID: sessionID, RequestID: uuid.NewString(), Input: arguments},
				&response, map[string]string{testingCapabilityHeader: capability})
			if err != nil {
				return nil, nil, errors.New(redactTestingCapability(err.Error(), capability))
			}
			if tool.name != "screenshot" {
				return &mcp.CallToolResult{Content: []mcp.Content{
					&mcp.TextContent{Text: redactTestingCapability(string(response), capability)},
				}}, nil, nil
			}
			var envelope testingScreenshotDTO
			if err := json.Unmarshal(response, &envelope); err != nil {
				return nil, nil, errors.New("daemon returned an invalid screenshot response")
			}
			screenshot := envelope.Screenshot
			if screenshot == nil || screenshot.MIMEType != "image/png" || len(screenshot.Data) == 0 ||
				screenshot.Frame.ScreenshotID == "" || screenshot.Frame.Width <= 0 || screenshot.Frame.Height <= 0 || screenshot.Frame.CapturedAt.IsZero() {
				return nil, nil, errors.New("daemon returned an incomplete PNG screenshot")
			}
			frame := screenshot.Frame
			metadata, err := json.Marshal(struct {
				ScreenshotID string                       `json:"screenshotId"`
				Width        int                          `json:"width"`
				Height       int                          `json:"height"`
				CapturedAt   string                       `json:"capturedAt"`
				Evidence     []domain.TestEvidenceReceipt `json:"evidence"`
			}{frame.ScreenshotID, frame.Width, frame.Height, frame.CapturedAt.Format(time.RFC3339Nano), envelope.Evidence})
			if err != nil {
				return nil, nil, err
			}
			return &mcp.CallToolResult{Content: []mcp.Content{
				&mcp.ImageContent{Data: screenshot.Data, MIMEType: screenshot.MIMEType},
				&mcp.TextContent{Text: string(metadata)},
			}}, nil, nil
		})
	}
	return server
}
