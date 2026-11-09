package settings

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// HarnessDefaultPatch 区分未提交字段和显式清空，模型偏好与 CLI 选择分别保存。
type HarnessDefaultPatch struct {
	DisabledModels      *[]string
	ExecutableDirectory *string
	ExecutablePath      *string
}

// SetHarnessExecutableChanged 在 daemon 组合根绑定所有解析、版本、认证和模型缓存的失效通知。
func (s *Service) SetHarnessExecutableChanged(changed func(domain.AgentHarness)) {
	s.harnessMu.Lock()
	s.executableChanged = changed
	s.harnessMu.Unlock()
}

// SetHarnessExecutableGuard 让保存入口与安装、更新和进程启动共享同一门禁。
func (s *Service) SetHarnessExecutableGuard(guard func(context.Context, domain.AgentHarness) (func(), error)) {
	s.harnessMu.Lock()
	s.executableGuard = guard
	s.harnessMu.Unlock()
}

// LoadHarnessExecutables 在任何启动探测前恢复持久化入口；失效的路径仍保留为显式选择。
func (s *Service) LoadHarnessExecutables(ctx context.Context) error {
	s.harnessMu.Lock()
	defer s.harnessMu.Unlock()
	snapshot, err := s.Get(ctx)
	if err != nil {
		return err
	}
	publishExecutables(snapshot)
	return nil
}

// publishExecutables 一次发布全量主机设置，不把用户选择写入系统 PATH。
func publishExecutables(snapshot Snapshot) {
	paths := make(map[string]string, len(snapshot.HarnessDefaults))
	for harness, defaults := range snapshot.HarnessDefaults {
		paths[harness] = defaults.ExecutablePath
	}
	binaryutil.SetSelectedBinaries(paths)
}

// PatchHarnessDefault 串行合并同一主机的更新，验证通过且落盘后才切换运行入口。
func (s *Service) PatchHarnessDefault(ctx context.Context, agent domain.AgentHarness, patch HarnessDefaultPatch) (Snapshot, error) {
	if !slices.Contains(domain.AllHarnesses, agent) {
		return Snapshot{}, fmt.Errorf("%w %q", ErrUnknownHarness, agent)
	}
	if patch.DisabledModels == nil && patch.ExecutableDirectory == nil && patch.ExecutablePath == nil {
		return Snapshot{}, fmt.Errorf("%w: no preference fields supplied", ErrInvalidHarnessDefault)
	}
	s.harnessMu.Lock()
	defer s.harnessMu.Unlock()
	snapshot, err := s.Get(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	value := snapshot.HarnessDefaults[string(agent)]
	if patch.DisabledModels != nil {
		disabled, ok := normalizeDisabledModels(*patch.DisabledModels)
		if !ok {
			return Snapshot{}, fmt.Errorf("%w: disabled models must be at most %d model ids of up to 256 characters", ErrInvalidHarnessDefault, maxDisabledModels)
		}
		value.DisabledModels = disabled
	}
	if patch.ExecutableDirectory != nil {
		value.ExecutableDirectory = strings.TrimSpace(*patch.ExecutableDirectory)
	}
	if patch.ExecutablePath != nil {
		value.ExecutablePath = strings.TrimSpace(*patch.ExecutablePath)
		if value.ExecutablePath == "" {
			value.ExecutableDirectory = ""
		}
	}
	if patch.ExecutableDirectory != nil || patch.ExecutablePath != nil {
		if value.ExecutablePath != "" {
			if !filepath.IsAbs(value.ExecutableDirectory) || !filepath.IsAbs(value.ExecutablePath) {
				return Snapshot{}, fmt.Errorf("%w: executable directory and path must be absolute", ErrInvalidHarnessDefault)
			}
			value.ExecutableDirectory = filepath.Clean(value.ExecutableDirectory)
			value.ExecutablePath = filepath.Clean(value.ExecutablePath)
			rel, err := filepath.Rel(value.ExecutableDirectory, value.ExecutablePath)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return Snapshot{}, fmt.Errorf("%w: executable must be inside the selected directory", ErrInvalidHarnessDefault)
			}
			if _, err := binaryutil.ProbeExecutable(ctx, string(agent), value.ExecutablePath); err != nil {
				return Snapshot{}, fmt.Errorf("%w: %w", ErrInvalidHarnessDefault, err)
			}
		} else if value.ExecutableDirectory != "" {
			return Snapshot{}, fmt.Errorf("%w: choose an executable before saving the directory", ErrInvalidHarnessDefault)
		}
		if s.executableGuard != nil {
			release, err := s.executableGuard(ctx, agent)
			if err != nil {
				return Snapshot{}, fmt.Errorf("%w: %w", ErrInvalidHarnessDefault, err)
			}
			defer release()
		}
	}
	if err := s.store.SetHarnessDefault(ctx, agent, value, s.now()); err != nil {
		return Snapshot{}, err
	}
	// 保存成功后使用合并值发布，后续读取失败也不能留下“数据库已切换、进程仍旧”的状态。
	defaults := make(map[string]domain.HarnessDefault, len(snapshot.HarnessDefaults)+1)
	for key, entry := range snapshot.HarnessDefaults {
		defaults[key] = entry
	}
	if value.IsZero() {
		delete(defaults, string(agent))
	} else {
		defaults[string(agent)] = value
	}
	snapshot.HarnessDefaults = defaults
	if patch.ExecutablePath != nil || patch.ExecutableDirectory != nil {
		publishExecutables(snapshot)
		if s.executableChanged != nil {
			s.executableChanged(agent)
		}
	}
	return s.Get(ctx)
}
