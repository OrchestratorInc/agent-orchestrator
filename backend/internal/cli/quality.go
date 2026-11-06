package cli

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

// newQualityCommand 复用精确正文的 Operator HTTP 流程；客户端不访问 SQLite 或 Git。
func newQualityCommand(c *commandContext) *cobra.Command {
	root := &cobra.Command{Use: "quality", Short: "Read and approve workflow delivery evidence"}
	for _, action := range []struct {
		name, method, path string
		target             bool
	}{
		{"summary", "GET", "quality-summary", false},
		{"coordination", "GET", "coordination-summary", false},
		{"policies", "GET", "quality-policies", false},
		{"configure", "PUT", "quality-policy", false},
		{"snapshot", "POST", "attempts/%s/artifacts/snapshot", true},
		{"gate", "POST", "artifacts/%s/gates", true},
		{"review", "POST", "artifacts/%s/reviews", true},
		{"finish-review", "POST", "reviews/%s/finish", true},
		{"commit", "POST", "artifacts/%s/commit", true},
		{"preview", "POST", "integrations/preview", false},
		{"plan", "GET", "integrations/%s", true},
		{"apply", "POST", "integrations/%s/apply", true},
		{"push", "POST", "integrations/%s/push", true},
		{"operation", "GET", "integration-operations/%s", true},
		{"reconcile", "POST", "integration-operations/%s/reconcile", true},
	} {
		var bodyFile string
		use, count := action.name+" <workflow-id>", 1
		if action.target {
			use += " <target-id>"
			count++
		}
		cmd := &cobra.Command{Use: use, Short: action.name + " workflow delivery evidence", Args: cobra.ExactArgs(count),
			RunE: func(cmd *cobra.Command, args []string) error {
				for _, id := range args {
					if strings.TrimSpace(id) == "" || strings.ContainsAny(id, "/\\?#%") || id == "." || id == ".." {
						return usageError{errors.New("identifiers must be non-empty path segments")}
					}
				}
				suffix := action.path
				if action.target {
					suffix = fmt.Sprintf(suffix, url.PathEscape(args[1]))
				}
				path := "/api/v1/workflows/" + url.PathEscape(args[0]) + "/" + suffix
				var body []byte
				if action.method != "GET" {
					if bodyFile == "" {
						return usageError{errors.New("--body-file is required")}
					}
					var err error
					body, err = readOperatorBody(bodyFile)
					if err != nil {
						return err
					}
				}
				result, err := c.performOperatorAction(cmd.Context(), operatorAction{method: action.method}, path, body)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(c.deps.Out, string(result.raw))
				return err
			},
		}
		if action.method != "GET" {
			cmd.Flags().StringVar(&bodyFile, "body-file", "", "file containing the exact JSON body to approve")
		}
		root.AddCommand(cmd)
	}
	return root
}
