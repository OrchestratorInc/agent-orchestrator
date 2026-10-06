package systeminstall

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// UpdateStatus describes observed version availability, not whether AO knows
// an update command. Unknown is deliberately distinct from current.
type UpdateStatus string

const (
	// UpdateStatusUnknown means AO could not establish update availability.
	UpdateStatusUnknown UpdateStatus = "unknown"
	// UpdateStatusCurrent means the installed release matches the package source.
	UpdateStatusCurrent UpdateStatus = "current"
	// UpdateStatusBehindLatest means a newer package release is available.
	UpdateStatusBehindLatest UpdateStatus = "behind_latest"
)

// UpdateAdvisory is the daemon's non-mutating comparison for one harness.
type UpdateAdvisory struct {
	AgentID        string       `json:"agentId"`
	Status         UpdateStatus `json:"status"`
	CurrentVersion string       `json:"currentVersion,omitempty"`
	LatestVersion  string       `json:"latestVersion,omitempty"`
	Source         string       `json:"source,omitempty"`
	CheckedAt      time.Time    `json:"checkedAt"`
}

type updateAdvisoryCall struct {
	done     chan struct{}
	advisory UpdateAdvisory
	err      error
}

// UpdateAdvisory probes the adapter-selected binary and checks the npm or
// Homebrew package that owns it, whether or not AO installed it. Binaries no
// package manager owns are compared with the vendor's release channel. A
// harness with neither, and failed probes, remain unknown; they never
// masquerade as up-to-date.
func (s *Service) UpdateAdvisory(ctx context.Context, target Target) (UpdateAdvisory, error) {
	if !IsAgentTarget(target) {
		return UpdateAdvisory{}, fmt.Errorf("systeminstall: unknown harness %q", target)
	}
	if err := ctx.Err(); err != nil {
		return UpdateAdvisory{}, err
	}
	now := time.Now().UTC()
	s.mu.Lock()
	cached, found := s.updateAdvisories[target]
	if found {
		ttl := time.Hour
		if cached.Status == UpdateStatusUnknown {
			ttl = 5 * time.Minute
		}
		if now.Sub(cached.CheckedAt) < ttl {
			s.mu.Unlock()
			return cached, nil
		}
	}
	call := s.updateAdvisoryCalls[target]
	if call == nil {
		if s.stopping {
			s.mu.Unlock()
			return UpdateAdvisory{}, context.Canceled
		}
		call = &updateAdvisoryCall{done: make(chan struct{})}
		if s.updateAdvisoryCalls == nil {
			s.updateAdvisoryCalls = make(map[Target]*updateAdvisoryCall)
		}
		s.updateAdvisoryCalls[target] = call
		s.workers.Add(1)
		go s.runUpdateAdvisory(target, call)
	}
	s.mu.Unlock()
	select {
	case <-call.done:
		return call.advisory, call.err
	case <-ctx.Done():
		return UpdateAdvisory{}, ctx.Err()
	}
}

func (s *Service) runUpdateAdvisory(target Target, call *updateAdvisoryCall) {
	defer s.workers.Done()
	advisory, err := s.computeUpdateAdvisory(s.backgroundContext, target)
	s.mu.Lock()
	call.advisory, call.err = advisory, err
	if s.updateAdvisoryCalls[target] == call {
		delete(s.updateAdvisoryCalls, target)
		if err == nil && s.backgroundContext.Err() == nil {
			if s.updateAdvisories == nil {
				s.updateAdvisories = make(map[Target]UpdateAdvisory)
			}
			s.updateAdvisories[target] = advisory
		}
	}
	close(call.done)
	s.mu.Unlock()
}

func (s *Service) computeUpdateAdvisory(ctx context.Context, target Target) (UpdateAdvisory, error) {
	advisory := UpdateAdvisory{AgentID: string(target), Status: UpdateStatusUnknown, CheckedAt: time.Now().UTC()}
	job, err := s.Status(ctx, target)
	if err != nil {
		return advisory, err
	}
	if s.verifier == nil {
		return advisory, nil
	}
	var plans []Plan
	recordedMethod := ""
	if s.ownsInstallation != nil && s.latestVersion != nil {
		if planner, err := s.newRequestPlanner(ctx); err == nil {
			if job.Status == StatusSucceeded {
				recordedMethod = job.Method
			}
			plans = planner.agentMethodPlans(target, AgentOperationInstall)
		}
	}
	if len(plans) == 0 && s.officialVersion == nil {
		return advisory, nil
	}
	verified, err := s.verifier.Verify(ctx, target)
	if err != nil {
		return advisory, nil //nolint:nilerr // An unverified binary cannot establish update availability.
	}
	sources := advisoryPackageSources(plans, recordedMethod, packageLayout(verified.ResolvedPath))
	// The binary's package-manager root decides the source, so harnesses the
	// user installed outside AO are covered as well as AO-installed ones.
	var source Plan
	for _, candidate := range sources {
		owned, err := s.ownsInstallation(ctx, verified.ResolvedPath, candidate.Method, packageWithoutLatest(candidate.Package), candidate.PackageCask)
		if err == nil && owned {
			source = candidate
			break
		}
	}
	if source.Package == "" && s.officialVersion == nil {
		return advisory, nil
	}
	current, ok := findUpdateVersion(verified.Output)
	if !ok {
		return advisory, nil
	}
	advisory.CurrentVersion = current.display
	var latest string
	if source.Package != "" {
		advisory.Source = source.Method
		latest, err = s.latestVersion(ctx, source.Method, packageWithoutLatest(source.Package), source.PackageCask)
	} else {
		// No package manager provably owns the binary, so compare against the
		// vendor's own release channel, unless the binary sits in another
		// tool's layout whose channel may differ from the vendor's.
		official, ok := officialSourceFor(target, s.goos, runtime.GOARCH)
		if !ok || !officialChannelFits(official.kind, packageLayout(verified.ResolvedPath)) {
			return advisory, nil
		}
		advisory.Source = officialReleaseSource
		latest, err = s.officialVersion(ctx, target)
	}
	if err != nil {
		return advisory, nil //nolint:nilerr // A failed latest-version lookup is not an update verdict.
	}
	parsedLatest, ok := parseUpdateVersion(latest)
	if !ok {
		return advisory, nil
	}
	comparison, comparable := compareUpdateVersions(current, parsedLatest)
	if !comparable {
		return advisory, nil
	}
	advisory.LatestVersion = parsedLatest.display
	switch {
	case comparison < 0:
		advisory.Status = UpdateStatusBehindLatest
	case comparison == 0:
		advisory.Status = UpdateStatusCurrent
	}
	return advisory, nil
}

func latestAvailableVersion(commands ports.CommandRunner) func(context.Context, string, string, bool) (string, error) {
	client := &http.Client{Timeout: 4 * time.Second}
	return func(ctx context.Context, method, pkg string, cask bool) (string, error) {
		switch method {
		case "npm":
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://registry.npmjs.org/"+url.PathEscape(pkg)+"/latest", http.NoBody)
			if err != nil {
				return "", err
			}
			response, err := client.Do(request)
			if err != nil {
				return "", err
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != http.StatusOK {
				return "", fmt.Errorf("npm registry status %d", response.StatusCode)
			}
			var metadata struct {
				Version string `json:"version"`
			}
			if err := json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&metadata); err != nil {
				return "", err
			}
			return metadata.Version, nil
		case "homebrew":
			if commands == nil {
				return "", fmt.Errorf("homebrew command runner unavailable")
			}
			output := &capturedOutput{max: maxOutputBytes}
			argv := []string{"brew", "info", "--json=v2"}
			if cask {
				argv = append(argv, "--cask")
			} else {
				argv = append(argv, "--formula")
			}
			argv = append(argv, pkg)
			probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if err := commands.Run(probeCtx, argv, output, output); err != nil {
				return "", err
			}
			var info struct {
				Formulae []struct {
					Versions struct {
						Stable string `json:"stable"`
					} `json:"versions"`
				} `json:"formulae"`
				Casks []struct {
					Version string `json:"version"`
				} `json:"casks"`
			}
			if err := json.Unmarshal([]byte(output.String()), &info); err != nil {
				return "", err
			}
			if cask && len(info.Casks) > 0 {
				return info.Casks[0].Version, nil
			}
			if !cask && len(info.Formulae) > 0 {
				return info.Formulae[0].Versions.Stable, nil
			}
			return "", fmt.Errorf("homebrew returned no version for %s", pkg)
		default:
			return "", fmt.Errorf("unsupported version source %s", method)
		}
	}
}
