package agentauth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/harnessendpoint"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// authenticationInvalidator drops cached readiness so a changed endpoint is
// observed on the next check. AO's agent service provides it.
type authenticationInvalidator interface {
	InvalidateAgentAuthentication(agentID string)
}

func (s *Service) endpoints() *harnessendpoint.Store {
	return harnessendpoint.ForDataDir(s.dataDir)
}

// endpointStatus is agentID's display-safe custom endpoint status, or nil when
// the harness has no custom endpoint support or AO has no data directory. A
// config that cannot be read still reports support, so the user can overwrite
// or remove it.
func (s *Service) endpointStatus(agentID string) *harnessendpoint.Status {
	if s.dataDir == "" {
		return nil
	}
	status, ok, _ := s.endpoints().Status(agentID)
	if !ok {
		return nil
	}
	return &status
}

// SetEndpoint stores agentID's custom endpoint. The API key is accepted but
// never returned.
func (s *Service) SetEndpoint(ctx context.Context, agentID string, in harnessendpoint.Input) (harnessendpoint.Status, error) {
	if err := ctx.Err(); err != nil {
		return harnessendpoint.Status{}, err
	}
	if err := s.requireEndpointTarget(agentID); err != nil {
		return harnessendpoint.Status{}, err
	}
	if err := s.completeEndpointInput(ctx, agentID, &in); err != nil {
		return harnessendpoint.Status{}, err
	}
	status, err := s.endpoints().Save(agentID, in, time.Now())
	if err != nil {
		return harnessendpoint.Status{}, endpointError(err)
	}
	s.invalidateEndpointAuthentication(agentID)
	return status, nil
}

// ClearEndpoint removes agentID's custom endpoint, returning the harness to
// its native sign-in.
func (s *Service) ClearEndpoint(ctx context.Context, agentID string) (harnessendpoint.Status, error) {
	if err := ctx.Err(); err != nil {
		return harnessendpoint.Status{}, err
	}
	if err := s.requireEndpointTarget(agentID); err != nil {
		return harnessendpoint.Status{}, err
	}
	if err := s.endpoints().Delete(agentID); err != nil {
		return harnessendpoint.Status{}, endpointError(err)
	}
	s.invalidateEndpointAuthentication(agentID)
	status, _, err := s.endpoints().Status(agentID)
	if err != nil {
		return harnessendpoint.Status{}, endpointError(err)
	}
	return status, nil
}

// completeEndpointInput fills what the client leaves open. The auth style is
// never asked: the endpoint's model listing is tried with each style the
// harness supports, and the first it accepts is saved. The default model is
// not asked on the form either: a save that keeps the saved base URL keeps the
// saved model, and otherwise the first model the endpoint lists becomes the
// default until the user picks another in Models. A save that keeps the saved
// URL and key keeps the style that already works, without asking. Without a
// key yet, Save reports the missing key.
func (s *Service) completeEndpointInput(ctx context.Context, agentID string, in *harnessendpoint.Input) error {
	spec, _ := harnessendpoint.Lookup(agentID)
	needStyle := in.AuthStyle == ""
	needModel := strings.TrimSpace(in.Model) == "" && spec.ModelEnv != ""
	if !needStyle && !needModel {
		return nil
	}
	target, ok, err := s.endpoints().ProbeTarget(agentID, *in)
	if err != nil {
		return endpointError(err)
	}
	if !ok {
		return nil
	}
	if needStyle && target.SavedStyle != "" {
		in.AuthStyle, needStyle = target.SavedStyle, false
	}
	if needModel && target.SavedModel != "" {
		in.Model, needModel = target.SavedModel, false
	}
	var models []string
	switch {
	case needStyle:
		detection, err := harnessendpoint.Detect(ctx, s.endpointProbeClient, agentID, target.BaseURL, target.Key)
		if err != nil {
			return endpointProbeError(err)
		}
		in.AuthStyle, models = detection.Style, detection.Models
	case needModel:
		// The style is known, given by the client or kept from a save the
		// endpoint accepted. The listing is only asked for a default model: a
		// key may run requests without being allowed to list models, so a
		// listing the endpoint refuses or cannot give leaves the harness on
		// its own default model instead of blocking the save.
		models, err = harnessendpoint.ListModels(ctx, s.endpointProbeClient, agentID, target.BaseURL, in.AuthStyle, target.Key)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			models = nil
		}
	}
	if needModel && len(models) > 0 {
		in.Model = models[0]
	}
	return nil
}

func endpointProbeError(err error) error {
	if errors.Is(err, harnessendpoint.ErrKeyRejected) {
		return apierr.Invalid("AGENT_ENDPOINT_KEY_REJECTED", "The endpoint rejected this key. Check the key and the base URL.", nil)
	}
	return err
}

func (s *Service) requireEndpointTarget(agentID string) error {
	if _, ok := planByAgentID[agentID]; !ok {
		return apierr.Invalid("AGENT_AUTH_TARGET_UNKNOWN", fmt.Sprintf("unknown agent authentication target %q", agentID), nil)
	}
	if _, ok := harnessendpoint.Lookup(agentID); !ok {
		return apierr.Invalid("AGENT_ENDPOINT_UNSUPPORTED", "This harness does not support a custom API endpoint.", nil)
	}
	if s.dataDir == "" {
		return apierr.Internal("AGENT_ENDPOINT_UNAVAILABLE", "Custom endpoint storage is unavailable.")
	}
	return nil
}

func (s *Service) invalidateEndpointAuthentication(agentID string) {
	if invalidator, ok := s.agents.(authenticationInvalidator); ok {
		invalidator.InvalidateAgentAuthentication(agentID)
	}
}

func endpointError(err error) error {
	var validation *harnessendpoint.ValidationError
	if errors.As(err, &validation) {
		return apierr.Invalid("AGENT_ENDPOINT_INVALID", validation.Message, nil)
	}
	if errors.Is(err, harnessendpoint.ErrUnsupported) {
		return apierr.Invalid("AGENT_ENDPOINT_UNSUPPORTED", "This harness does not support a custom API endpoint.", nil)
	}
	return apierr.Internal("AGENT_ENDPOINT_STORE_FAILED", "Could not update the custom endpoint.")
}
