package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// newOperatorRequestCommand 为既有人工读取、干预入口提供相同闭集内的正式客户端。
func newOperatorRequestCommand(c *commandContext) *cobra.Command {
	var method, path, bodyFile string
	cmd := &cobra.Command{
		Use: "request", Short: "Verify and send an allowlisted operator API request", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			method = strings.ToUpper(strings.TrimSpace(method))
			if !principal.IsOperatorOperation(method, path) {
				return usageError{errors.New("--method and --path must name an allowlisted operator operation")}
			}
			var body []byte
			if bodyFile != "" {
				var err error
				body, err = readOperatorBody(bodyFile)
				if err != nil {
					return err
				}
			}
			result, err := c.performOperatorAction(cmd.Context(), operatorAction{method: method}, path, body)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(c.deps.Out, string(result.raw))
			return err
		},
	}
	cmd.Flags().StringVar(&method, "method", "GET", "HTTP method for the allowlisted operation")
	cmd.Flags().StringVar(&path, "path", "", "exact local API path including any query string")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "file containing the exact JSON body (optional)")
	return cmd
}

// readOperatorBody 保留原始正文供批准摘要使用，并在读取时限制大小。
func readOperatorBody(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read operator payload: %w", err)
	}
	body, err := io.ReadAll(io.LimitReader(file, principal.MaxApprovalPayloadBytes+1))
	closeErr := file.Close()
	if err != nil {
		return nil, fmt.Errorf("read operator payload: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close operator payload: %w", closeErr)
	}
	if len(body) > principal.MaxApprovalPayloadBytes {
		return nil, usageError{errors.New("operator payload exceeds 64 KiB")}
	}
	return body, nil
}
