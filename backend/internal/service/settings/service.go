package settings

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type Store interface {
	GetAppSettings(ctx context.Context) (Snapshot, error)
	SetDefaultSessionMode(ctx context.Context, mode domain.SessionMode, now time.Time) error
	SetHarnessDefault(ctx context.Context, agent domain.AgentHarness, value domain.HarnessDefault, now time.Time) error
	SetVersionRefresh(ctx context.Context, enabled bool, now time.Time) error
	SetComputerUse(ctx context.Context, enabled bool, now time.Time) error
	SetSessionRetentionDays(ctx context.Context, days int, now time.Time) error
	SetAccountHandoff(ctx context.Context, enabled bool, now time.Time) error
}

type Snapshot struct {
	HarnessDefaults        map[string]domain.HarnessDefault
	DefaultSessionMode     domain.SessionMode
	ChatHibernationEnabled bool
	VersionRefresh         bool
	ComputerUse            bool
	SessionRetentionDays   int
	AccountHandoff         bool
	UpdatedAt              time.Time
}

type Offering struct {
	Client               string
	LocalEnabled         bool
	CloudOffering        bool
	CloudControlPlaneURL string
	TrackerIntakeEnabled bool
}

func OfferingFromConfig(cfg config.Config) Offering {
	return Offering{
		Client:               cfg.Client,
		LocalEnabled:         cfg.LocalOffering,
		CloudOffering:        cfg.CloudOffering,
		CloudControlPlaneURL: cfg.CloudControlPlaneURL,
		TrackerIntakeEnabled: cfg.TrackerIntake,
	}
}

func (o Offering) CloudEnabled() bool {
	return o.CloudOffering && o.CloudControlPlaneURL != ""
}

type ChatCapability interface {
	SupportsChat(harness domain.AgentHarness) bool
}

type Service struct {
	store           Store
	chat            ChatCapability
	offering        Offering
	now             func() time.Time
	chatHibernation atomic.Bool
	versionRefresh  atomic.Int32
	cuaInstalled    atomic.Pointer[func(context.Context) (bool, error)]
}

var errCuaDriverMissing = apierr.Conflict("COMPUTER_USE_NOT_INSTALLED", "Install Cua Driver in Settings > Computer use agents before letting agents use this computer.", nil)

func (s *Service) SetCuaDriverInstalled(installed func(context.Context) (bool, error)) {
	s.cuaInstalled.Store(&installed)
}

func New(store Store, chat ChatCapability, offering Offering, now func() time.Time) *Service {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{store: store, chat: chat, offering: offering, now: now}
}

func (s *Service) Offering() Offering {
	return s.offering
}

func (s *Service) Get(ctx context.Context) (Snapshot, error) {
	snapshot, err := s.store.GetAppSettings(ctx)
	snapshot.ChatHibernationEnabled = s.chatHibernation.Load()
	if err == nil {
		s.cacheVersionRefresh(snapshot.VersionRefresh)
	}
	return snapshot, err
}

func (s *Service) AccountHandoffEnabled(ctx context.Context) bool {
	snapshot, err := s.store.GetAppSettings(ctx)
	return err == nil && snapshot.AccountHandoff
}

func (s *Service) SetAccountHandoff(ctx context.Context, enabled bool) (Snapshot, error) {
	if err := s.store.SetAccountHandoff(ctx, enabled, s.now()); err != nil {
		return Snapshot{}, err
	}
	return s.Get(ctx)
}

func (s *Service) cacheVersionRefresh(enabled bool) {
	if enabled {
		s.versionRefresh.Store(1)
	} else {
		s.versionRefresh.Store(2)
	}
}

func (s *Service) VersionRefreshEnabled(ctx context.Context) bool {
	switch s.versionRefresh.Load() {
	case 1:
		return true
	case 2:
		return false
	}
	snapshot, err := s.store.GetAppSettings(ctx)
	if err != nil {
		return false
	}
	s.cacheVersionRefresh(snapshot.VersionRefresh)
	return snapshot.VersionRefresh
}

func (s *Service) SetVersionRefresh(ctx context.Context, enabled bool) (Snapshot, error) {
	if err := s.store.SetVersionRefresh(ctx, enabled, s.now()); err != nil {
		return Snapshot{}, err
	}
	s.cacheVersionRefresh(enabled)
	return s.Get(ctx)
}

func (s *Service) ComputerUseEnabled(ctx context.Context) bool {
	snapshot, err := s.store.GetAppSettings(ctx)
	return err == nil && snapshot.ComputerUse
}

func (s *Service) SetComputerUse(ctx context.Context, enabled bool) (Snapshot, error) {
	if installed := s.cuaInstalled.Load(); enabled && installed != nil {
		ok, err := (*installed)(ctx)
		if err != nil {
			return Snapshot{}, err
		}
		if !ok {
			return Snapshot{}, errCuaDriverMissing
		}
	}
	if err := s.store.SetComputerUse(ctx, enabled, s.now()); err != nil {
		return Snapshot{}, err
	}
	return s.Get(ctx)
}

func (s *Service) DefaultSessionMode(ctx context.Context) domain.SessionMode {
	snapshot, err := s.store.GetAppSettings(ctx)
	if err != nil {
		return domain.DefaultSessionMode
	}
	return domain.NormalizeSessionMode(snapshot.DefaultSessionMode)
}

func (s *Service) SetDefaultSessionMode(ctx context.Context, mode domain.SessionMode) (Snapshot, error) {
	if !mode.Valid() {
		return Snapshot{}, fmt.Errorf("%w: %q", ports.ErrChatUnsupported, mode)
	}
	if err := s.store.SetDefaultSessionMode(ctx, mode, s.now()); err != nil {
		return Snapshot{}, err
	}
	return s.Get(ctx)
}

const maxSessionRetentionDays = 3650

func (s *Service) SetSessionRetentionDays(ctx context.Context, days int) (Snapshot, error) {
	if days < 0 || days > maxSessionRetentionDays {
		return Snapshot{}, apierr.Invalid("SESSION_RETENTION_INVALID", fmt.Sprintf("days must be between 0 and %d", maxSessionRetentionDays), nil)
	}
	if err := s.store.SetSessionRetentionDays(ctx, days, s.now()); err != nil {
		return Snapshot{}, err
	}
	return s.Get(ctx)
}

func (s *Service) ChatHibernationEnabled() bool { return s.chatHibernation.Load() }

func (s *Service) SetChatHibernationEnabled(ctx context.Context, enabled bool) (Snapshot, error) {
	if !enabled {
		s.chatHibernation.Store(false)
	}
	snapshot, err := s.store.GetAppSettings(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if enabled {
		s.chatHibernation.Store(true)
	}
	snapshot.ChatHibernationEnabled = enabled
	return snapshot, nil
}

func (s *Service) ChatHarnesses(candidates []domain.AgentHarness) []domain.AgentHarness {
	if s.chat == nil {
		return nil
	}
	var out []domain.AgentHarness
	for _, harness := range candidates {
		if s.chat.SupportsChat(harness) {
			out = append(out, harness)
		}
	}
	return out
}

const maxDisabledModels = 20000

func normalizeDisabledModels(ids []string) ([]string, bool) {
	if len(ids) > maxDisabledModels {
		return nil, false
	}
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || len(id) > 256 || strings.ContainsFunc(id, unicode.IsControl) {
			return nil, false
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, true
	}
	return out, true
}

var ErrInvalidHarnessDefault = errors.New("invalid harness default")

var ErrUnknownHarness = errors.New("unknown harness")

func (s *Service) SetHarnessDefault(ctx context.Context, agent domain.AgentHarness, value domain.HarnessDefault) (Snapshot, error) {
	if !slices.Contains(domain.AllHarnesses, agent) {
		return Snapshot{}, fmt.Errorf("%w %q", ErrUnknownHarness, agent)
	}
	disabled, ok := normalizeDisabledModels(value.DisabledModels)
	if !ok {
		return Snapshot{}, fmt.Errorf("%w: disabled models must be at most %d model ids of up to 256 characters", ErrInvalidHarnessDefault, maxDisabledModels)
	}
	value.DisabledModels = disabled
	if err := s.store.SetHarnessDefault(ctx, agent, value, s.now()); err != nil {
		return Snapshot{}, err
	}
	return s.Get(ctx)
}
