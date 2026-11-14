package ports

import (
	"context"
	"errors"
	"io"
	"time"
)

type ExecutableFinder interface {
	LookPath(file string) (string, error)
}

type CommandRunner interface {
	Run(ctx context.Context, argv []string, stdout, stderr io.Writer) error
}

type InstallCommand struct {
	Argv     []string
	Env      []string
	Dir      string
	Isolated bool
}

type InstallCommandRunner interface {
	RunInstall(ctx context.Context, command InstallCommand, stdout, stderr io.Writer) error
}

type InstallScriptCommand struct {
	URL         string
	Interpreter []string
	Args        []string
	Env         []string
	Isolated    bool
}

type InstallScriptResult struct {
	SHA256 string
}

type InstallScriptRunner interface {
	RunInstallScript(ctx context.Context, command InstallScriptCommand, stdout, stderr io.Writer) (InstallScriptResult, error)
}

var ErrDownloadRejected = errors.New("download rejected")

type ManagedBinaryDownload struct {
	URL           string
	SHA256        string
	ArchiveMember string
	Name          string
}

type ManagedBinaryInstaller interface {
	InstallManagedBinary(ctx context.Context, download ManagedBinaryDownload) (string, error)
}

type NPMInstallCapabilities struct {
	NodeVersion    string
	NPMVersion     string
	GlobalPrefix   string
	PrefixWritable bool
	RuntimeErr     error
	Err            error
}

type HomebrewInstallCapabilities struct {
	Prefix         string
	PrefixWritable bool
	Formulae       map[string]bool
	Casks          map[string]bool
	Err            error
}

type InstallCapabilities struct {
	NPM      NPMInstallCapabilities
	Homebrew HomebrewInstallCapabilities
}

type InstallCapabilityProbe interface {
	Probe(ctx context.Context) (InstallCapabilities, error)
}

type PathWritableProbe interface {
	PathWritable(ctx context.Context, path string) (bool, error)
}

type AgentInstallJobRecord struct {
	Target              string
	Status              string
	Method              string
	Command             string
	ExpectedDestination string
	Output              string
	Error               string
	StartedAt           time.Time
	FinishedAt          *time.Time
	UpdatedAt           time.Time
}

type AgentInstallJobStore interface {
	UpsertAgentInstallJob(ctx context.Context, job AgentInstallJobRecord) error
	GetAgentInstallJob(ctx context.Context, target string) (AgentInstallJobRecord, bool, error)
	ListAgentInstallJobs(ctx context.Context) ([]AgentInstallJobRecord, error)
	InterruptActiveAgentInstallJobs(ctx context.Context, interruptedAt time.Time) error
}
