package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/spf13/cobra"
)

type spawnAccountChoice struct {
	Mode      string `json:"mode"`
	AccountID string `json:"accountId,omitempty"`
}

func initialSpawnAccount(cmd *cobra.Command, mode, id string) (*spawnAccountChoice, error) {
	if !cmd.Flags().Changed("account-mode") && !cmd.Flags().Changed("account-id") {
		return nil, nil
	}
	if (mode != "native" && mode != "managed") || (mode == "native" && cmd.Flags().Changed("account-id")) ||
		(mode == "managed" && (id == "" || len(id) > 256 || strings.TrimSpace(id) != id)) {
		return nil, usageError{errors.New("choose --account-mode native, or --account-mode managed with --account-id <public-id>")}
	}
	return &spawnAccountChoice{Mode: mode, AccountID: id}, nil
}

func (c *commandContext) requireInitialAccountSelection(ctx context.Context) error {
	var capability struct {
		InitialSelection bool `json:"initialSelection"`
	}
	if err := c.getJSON(ctx, "sessions/account-selection", &capability); err != nil {
		return err
	}
	if !capability.InitialSelection {
		return errors.New("this daemon does not support an explicit initial account choice; no session was created")
	}
	return nil
}
