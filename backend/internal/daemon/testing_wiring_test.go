package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	testingsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/testing"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestTestingWiringMissingProvidersFailsExplicitly(t *testing.T) {
	store := sqlitetest.MustOpen(t)
	svc := newTestingService(config.Config{DataDir: t.TempDir()}, store, testingProviders{})
	defer svc.Close()
	_, err := svc.CreateRun(context.Background(), testingsvc.CreateRunInput{})
	var apiError *apierr.Error
	if !errors.As(err, &apiError) || apiError.Code != "TESTING_PROVIDER_NOT_CONFIGURED" {
		t.Fatal("missing providers did not fail explicitly", err)
	}
}
