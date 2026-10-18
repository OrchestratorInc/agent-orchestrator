package domain

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Coder worker users an organization's bring-your-own Coder connection may
// select (OrgCoderConfig.WorkerUser).
const (
	// CoderWorkerUserAOWorker runs the worker and agent as a dedicated
	// ao-worker account AO creates in the workspace. It is the default.
	CoderWorkerUserAOWorker = "ao-worker"
	// CoderWorkerUserWorkspace runs the worker and agent as the workspace's
	// own login user (the user the Coder agent runs as), so the agent shares
	// that user's HOME, groups (for example docker) and environment.
	CoderWorkerUserWorkspace = "workspace"
)

// Bounds for the dev-environment fields of OrgCoderConfig.
const (
	MaxCoderReadyProbeLength             = 1000
	DefaultCoderReadyProbeTimeoutSeconds = 900
	MinCoderReadyProbeTimeoutSeconds     = 30
	MaxCoderReadyProbeTimeoutSeconds     = 3600

	MaxCoderWorkingDirectoryLength = 512
	// CoderWorkingDirectoryRepo is the placeholder a working directory may use
	// for the repository name (the last path segment of its URL, without .git).
	CoderWorkingDirectoryRepo = "{repo}"
)

// ErrCoderSettingRequiresWorkspaceUser marks a dev-environment setting that
// only works when the worker runs as the workspace's own login user.
var ErrCoderSettingRequiresWorkspaceUser = errors.New("coder setting requires the workspace worker user")

// Bounds for ProjectCoderConfig.Parameters.
const (
	MaxCoderTemplateParameters         = 50
	MaxCoderTemplateParameterNameBytes = 255
	MaxCoderTemplateParameterValueSize = 4 << 10
)

// normalizeDevEnvironment canonicalizes the worker-user, ready-probe and
// checkout-reuse fields so a GET returns explicit effective values.
func (c *OrgCoderConfig) normalizeDevEnvironment() {
	c.WorkerUser = strings.ToLower(strings.TrimSpace(c.WorkerUser))
	if c.WorkerUser == "" {
		c.WorkerUser = CoderWorkerUserAOWorker
	}
	c.ReadyProbe = strings.TrimSpace(c.ReadyProbe)
	if c.ReadyProbe != "" && c.ReadyProbeTimeoutSeconds == 0 {
		c.ReadyProbeTimeoutSeconds = DefaultCoderReadyProbeTimeoutSeconds
	}
	reuse := false
	if c.WorkerUser == CoderWorkerUserWorkspace {
		reuse = c.ReuseExistingCheckout == nil || *c.ReuseExistingCheckout
	}
	c.ReuseExistingCheckout = &reuse
	protect := c.ProtectSecretFiles == nil || *c.ProtectSecretFiles
	c.ProtectSecretFiles = &protect
	c.WorkingDirectory = strings.TrimSpace(c.WorkingDirectory)
}

// ReusesExistingCheckout reports whether sessions should adopt a checkout the
// template already cloned. It is only ever true for the workspace worker user.
func (c OrgCoderConfig) ReusesExistingCheckout() bool {
	return c.WorkerUser == CoderWorkerUserWorkspace &&
		(c.ReuseExistingCheckout == nil || *c.ReuseExistingCheckout)
}

// ProtectsSecretFiles reports whether sessions keep the coding agent out of
// secret environment files. Unset means yes.
func (c OrgCoderConfig) ProtectsSecretFiles() bool {
	return c.ProtectSecretFiles == nil || *c.ProtectSecretFiles
}

// ValidateDevEnvironment checks the worker-user, ready-probe and timeout
// fields of a normalized config. The messages are shown to the admin saving
// the connection.
func (c OrgCoderConfig) ValidateDevEnvironment() error {
	switch c.WorkerUser {
	case CoderWorkerUserAOWorker, CoderWorkerUserWorkspace:
	default:
		return fmt.Errorf("The Coder worker user must be %q or %q.", CoderWorkerUserAOWorker, CoderWorkerUserWorkspace)
	}
	if utf8.RuneCountInString(c.ReadyProbe) > MaxCoderReadyProbeLength {
		return fmt.Errorf("The workspace ready check must be at most %d characters.", MaxCoderReadyProbeLength)
	}
	if strings.ContainsRune(c.ReadyProbe, 0) || !utf8.ValidString(c.ReadyProbe) {
		return errors.New("The workspace ready check contains invalid characters.")
	}
	if c.ReadyProbeTimeoutSeconds != 0 &&
		(c.ReadyProbeTimeoutSeconds < MinCoderReadyProbeTimeoutSeconds ||
			c.ReadyProbeTimeoutSeconds > MaxCoderReadyProbeTimeoutSeconds) {
		return fmt.Errorf("The workspace ready check timeout must be between %d and %d seconds.",
			MinCoderReadyProbeTimeoutSeconds, MaxCoderReadyProbeTimeoutSeconds)
	}
	if c.WorkerUser != CoderWorkerUserWorkspace && c.WorkingDirectory != "" {
		return fmt.Errorf("%w: the working directory needs AO to run as the workspace user", ErrCoderSettingRequiresWorkspaceUser)
	}
	return ValidateCoderWorkingDirectory(c.WorkingDirectory)
}

// ValidateCoderWorkingDirectory checks a working directory template: empty,
// or an absolute path or one under home ("~/" or "$HOME/"), at most
// MaxCoderWorkingDirectoryLength characters, without control characters,
// ".." segments or placeholders other than CoderWorkingDirectoryRepo.
func ValidateCoderWorkingDirectory(value string) error {
	if value == "" {
		return nil
	}
	if utf8.RuneCountInString(value) > MaxCoderWorkingDirectoryLength || !utf8.ValidString(value) {
		return fmt.Errorf("The working directory must be at most %d characters.", MaxCoderWorkingDirectoryLength)
	}
	if strings.IndexFunc(value, func(character rune) bool { return character < ' ' || character == 0x7f }) >= 0 {
		return errors.New("The working directory contains invalid characters.")
	}
	rest, ok := strings.CutPrefix(value, "~/")
	if !ok {
		rest, ok = strings.CutPrefix(value, "$HOME/")
	}
	if !ok {
		rest, ok = strings.CutPrefix(value, "/")
	}
	if !ok || strings.Trim(rest, "/") == "" {
		return errors.New("The working directory must be an absolute path or start with ~/ or $HOME/, and name a directory below it.")
	}
	for _, segment := range strings.Split(rest, "/") {
		if segment == ".." || segment == "." {
			return errors.New("The working directory must not contain . or .. segments.")
		}
	}
	if strings.ContainsAny(strings.ReplaceAll(rest, CoderWorkingDirectoryRepo, ""), "{}$`\\") {
		return fmt.Errorf("The working directory may only use %s as a placeholder.", CoderWorkingDirectoryRepo)
	}
	return nil
}

// ValidateCoderTemplateParameterValues checks a project's template parameter
// values for shape only: at most MaxCoderTemplateParameters entries, non-empty
// names without surrounding whitespace or control characters, and values of
// at most MaxCoderTemplateParameterValueSize bytes. Whether each name is one
// the template declares is checked separately, against the template.
func ValidateCoderTemplateParameterValues(parameters map[string]string) error {
	if len(parameters) > MaxCoderTemplateParameters {
		return fmt.Errorf("at most %d Coder template parameters are allowed", MaxCoderTemplateParameters)
	}
	for name, value := range parameters {
		if name == "" || strings.TrimSpace(name) != name || len(name) > MaxCoderTemplateParameterNameBytes ||
			strings.IndexFunc(name, func(character rune) bool { return character < ' ' || character == 0x7f }) >= 0 {
			return fmt.Errorf("Coder template parameter name %q is invalid", name)
		}
		if len(value) > MaxCoderTemplateParameterValueSize {
			return fmt.Errorf("Coder template parameter %q must be at most 4 KB", name)
		}
		if strings.ContainsRune(value, 0) || !utf8.ValidString(value) {
			return fmt.Errorf("Coder template parameter %q contains invalid characters", name)
		}
	}
	return nil
}

// UnknownCoderTemplateParameters returns the names in parameters that the
// template does not declare, sorted, or nil when all are declared.
func UnknownCoderTemplateParameters(parameters map[string]string, declared []string) []string {
	known := make(map[string]bool, len(declared))
	for _, name := range declared {
		known[name] = true
	}
	var unknown []string
	for name := range parameters {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return unknown
}
