package cli

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type remoteHostUpdate struct {
	Version   string `json:"version"`
	Supported bool   `json:"supported"`
	State     string `json:"state"`
	Error     string `json:"error"`
}

func newRemoteHostUpdateCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use: "update", Args: noArgs,
		Short: "Install the newest release of this host's channel and restart it",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var status remoteHostUpdate
			if err := ctx.doJSON(cmd.Context(), http.MethodGet, "remote-host/update", nil, &status); err != nil {
				return remoteHostDaemonError(err)
			}
			if !status.Supported {
				return errors.New("this machine wasn't installed with setup-self-hosted.sh or the desktop app, so it can't update itself; update Orchestrator the way you installed it")
			}
			from := strings.TrimPrefix(status.Version, "v")
			message := "Updating Orchestrator from v" + from + "…"
			if status.State == "updating" {
				message = "An update is already running; waiting for it…"
			} else if err := ctx.doJSON(cmd.Context(), http.MethodPost, "remote-host/update", nil, &status); err != nil {
				return remoteHostDaemonError(err)
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), message); err != nil {
				return err
			}
			answered := ctx.deps.Now()
			for {
				ctx.deps.Sleep(3 * time.Second)
				var now remoteHostUpdate
				err := ctx.doJSON(cmd.Context(), http.MethodGet, "remote-host/update", nil, &now)
				switch {
				case err != nil:
					if ctx.deps.Now().Sub(answered) >= 10*time.Minute {
						return fmt.Errorf("the daemon hasn't answered for 10 minutes since the update started; check it with `ao status`: %w", remoteHostDaemonError(err))
					}
				case now.State == "updating":
					answered = ctx.deps.Now()
				case now.State == "failed":
					if now.Error == "" {
						return errors.New("the update failed")
					}
					return errors.New("the update failed: " + now.Error)
				case strings.TrimPrefix(now.Version, "v") == from:
					_, err := fmt.Fprintf(cmd.OutOrStdout(), "Already on the newest release: v%s\n", from)
					return err
				default:
					_, err := fmt.Fprintf(cmd.OutOrStdout(), "Updated Orchestrator to v%s\n", strings.TrimPrefix(now.Version, "v"))
					return err
				}
			}
		},
	}
}
