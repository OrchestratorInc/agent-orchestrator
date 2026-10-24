package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/aoagents/agent-orchestrator/cloud/internal/agentlogin"
)

// Login sandboxes have no user-worker identity, checkout or terminal history.
// Keep this entry before normal worker validation and self-update/bootstrap.
func runAgentLogin() error {
	config := agentlogin.Config{
		BaseURL:   strings.TrimSpace(os.Getenv("AO_CLOUD_PUBLIC_URL")),
		AttemptID: strings.TrimSpace(os.Getenv("AO_AGENT_LOGIN_ID")),
		Token:     strings.TrimSpace(os.Getenv("AO_AGENT_LOGIN_TOKEN")),
		DataDir:   strings.TrimSpace(os.Getenv("AO_DATA_DIR")),
	}
	if config.Token == "" {
		return errors.New("AO_AGENT_LOGIN_TOKEN is required for agent login")
	}
	if config.BaseURL == "" {
		return errors.New("AO_CLOUD_PUBLIC_URL is required for agent login")
	}
	if config.DataDir == "" {
		return errors.New("AO_DATA_DIR is required for agent login")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return agentlogin.Run(ctx, config)
}
