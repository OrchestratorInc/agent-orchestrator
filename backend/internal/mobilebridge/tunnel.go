package mobilebridge

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

var (
	quickTunnelURLRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)
	registeredRe     = regexp.MustCompile(`Registered tunnel connection`)
	locationRe       = regexp.MustCompile(`location=([a-z0-9]+)`)
	protocolRe       = regexp.MustCompile(`protocol=([a-z0-9]+)`)
)

type TunnelLog struct {
	URL         string
	Connections int
	Location    string
	Protocol    string
}

func (t *TunnelLog) Feed(line string) {
	if t.URL == "" {
		if m := quickTunnelURLRe.FindString(line); m != "" {
			t.URL = m
		}
	}
	if registeredRe.MatchString(line) {
		t.Connections++
		if m := locationRe.FindStringSubmatch(line); m != nil {
			t.Location = m[1]
		}
		if m := protocolRe.FindStringSubmatch(line); m != nil {
			t.Protocol = m[1]
		}
	}
}

func (t *TunnelLog) Ready() bool {
	return t.URL != "" && t.Connections > 0
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	out := strings.Split(s, "\n")
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

var MinCloudflaredVersion = CloudflaredVersion{2025, 8, 0}

type CloudflaredVersion [3]int

var cloudflaredVersionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

func ParseCloudflaredVersion(out string) (CloudflaredVersion, bool) {
	m := cloudflaredVersionRe.FindStringSubmatch(out)
	if m == nil {
		return CloudflaredVersion{}, false
	}
	var v CloudflaredVersion
	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return CloudflaredVersion{}, false
		}
		v[i] = n
	}
	return v, true
}

func (v CloudflaredVersion) AtLeast(floor CloudflaredVersion) bool {
	for i := range v {
		if v[i] != floor[i] {
			return v[i] > floor[i]
		}
	}
	return true
}

type CloudflaredSource string

const (
	CloudflaredFromEnv    CloudflaredSource = "env"
	CloudflaredManaged    CloudflaredSource = "managed"
	CloudflaredFromSystem CloudflaredSource = "system"
	CloudflaredAbsent     CloudflaredSource = "absent"
)

type CloudflaredLookup struct {
	EnvPath        string
	ManagedPath    string
	ManagedVersion CloudflaredVersion
	LookPath       func(file string) (string, error)
	Exists         func(path string) bool
	Version        func(path string) (CloudflaredVersion, bool)
}

type CloudflaredResolution struct {
	Path         string
	Source       CloudflaredSource
	NeedsInstall bool
	SystemPath   string
}

func ResolveCloudflared(l CloudflaredLookup) CloudflaredResolution {
	if l.EnvPath != "" {
		return CloudflaredResolution{Path: l.EnvPath, Source: CloudflaredFromEnv}
	}
	if l.ManagedPath != "" && l.Exists != nil && l.Exists(l.ManagedPath) && l.managedIsPinned() {
		return CloudflaredResolution{Path: l.ManagedPath, Source: CloudflaredManaged}
	}
	if l.LookPath != nil {
		if sys, err := l.LookPath("cloudflared"); err == nil && sys != "" {
			if v, ok := l.Version(sys); ok && v.AtLeast(MinCloudflaredVersion) {
				return CloudflaredResolution{Path: sys, Source: CloudflaredFromSystem}
			}
			return CloudflaredResolution{Source: CloudflaredAbsent, NeedsInstall: true, SystemPath: sys}
		}
	}
	return CloudflaredResolution{Source: CloudflaredAbsent, NeedsInstall: true}
}

func (l CloudflaredLookup) managedIsPinned() bool {
	if l.ManagedVersion == (CloudflaredVersion{}) {
		return true
	}
	if l.Version == nil {
		return false
	}
	v, ok := l.Version(l.ManagedPath)
	return !ok || v == l.ManagedVersion
}

func ManagedCloudflaredPath(dataDir string) string {
	name := "cloudflared"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dataDir, "bin", name)
}

func LocalCloudflaredLookup(dataDir string, pinned CloudflaredVersion) CloudflaredLookup {
	return CloudflaredLookup{
		EnvPath:        os.Getenv("AO_CLOUDFLARED_PATH"),
		ManagedPath:    ManagedCloudflaredPath(dataDir),
		ManagedVersion: pinned,
		LookPath:       exec.LookPath,
		Exists: func(p string) bool {
			fi, err := os.Stat(p)
			return err == nil && !fi.IsDir()
		},
		Version: func(p string) (CloudflaredVersion, bool) {
			ctx, cancel := context.WithTimeout(context.Background(), cloudflaredVersionTimeout)
			defer cancel()
			out, err := aoprocess.CommandContext(ctx, p, "--version").CombinedOutput()
			if err != nil {
				return CloudflaredVersion{}, false
			}
			return ParseCloudflaredVersion(string(out))
		},
	}
}

const cloudflaredVersionTimeout = 5 * time.Second

func TunnelPIDPath(dataDir string) string {
	return filepath.Join(dataDir, "mobile", "tunnel.pid")
}

func WriteTunnelPID(path string, pid int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir mobile dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write tunnel pid: %w", err)
	}
	return nil
}

func ReadTunnelPID(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func ReapStaleTunnel(path string, isOurs func(pid int) bool, kill func(pid int) error) error {
	pid, ok := ReadTunnelPID(path)
	if !ok {
		return nil
	}
	defer func() { _ = os.Remove(path) }()

	if isOurs == nil || !isOurs(pid) {
		return nil
	}
	if err := kill(pid); err != nil {
		return fmt.Errorf("kill stale tunnel %d: %w", pid, err)
	}
	return nil
}

type TunnelStatus struct {
	Supported  bool   `json:"supported"`
	Installing bool   `json:"installing"`
	Running    bool   `json:"running"`
	Ready      bool   `json:"ready"`
	Fixed      bool   `json:"fixed"`
	Hostname   string `json:"hostname"`
	Location   string `json:"location"`
	LastError  string `json:"lastError"`
}

type TunnelRuntime struct {
	mu      sync.Mutex
	log     TunnelLog
	running bool
	settled bool
	fixed   bool
	lastErr string
}

func (r *TunnelRuntime) Started() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = TunnelLog{}
	r.running = true
	r.settled = false
	r.fixed = false
	r.lastErr = ""
}

func (r *TunnelRuntime) Line(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log.Feed(line)
}

func (r *TunnelRuntime) Exited(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.running = false
	if err != nil {
		r.lastErr = err.Error()
	}
}

func (r *TunnelRuntime) Endpoint() *TunnelEndpoint {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running || !r.log.Ready() || !r.settled {
		return nil
	}
	return &TunnelEndpoint{Ready: true, Hostname: hostnameOf(r.log.URL)}
}

func (r *TunnelRuntime) Settled() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settled = true
}

func (r *TunnelRuntime) NamedHostname(hostname string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log.URL = "https://" + hostname
	r.fixed = true
}

func (r *TunnelRuntime) UnsettledHostname() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running || !r.log.Ready() || r.settled {
		return ""
	}
	return hostnameOf(r.log.URL)
}

func (r *TunnelRuntime) Snapshot() TunnelStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return TunnelStatus{
		Running:   r.running,
		Ready:     r.running && r.log.Ready() && r.settled,
		Fixed:     r.fixed,
		Hostname:  hostnameOf(r.log.URL),
		Location:  r.log.Location,
		LastError: r.lastErr,
	}
}

func hostnameOf(rawURL string) string {
	return strings.TrimPrefix(strings.TrimPrefix(rawURL, "https://"), "http://")
}

const (
	BaseTunnelBackoff = 2 * time.Second
	MaxTunnelBackoff  = 2 * time.Minute
)

func TunnelBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	d := BaseTunnelBackoff
	for range attempt {
		d *= 2
		if d >= MaxTunnelBackoff {
			return MaxTunnelBackoff
		}
	}
	return d
}

func CloudflaredArgs(localPort, metricsPort int) []string {
	return []string{
		"tunnel",
		"--url", fmt.Sprintf("http://127.0.0.1:%d", localPort),
		"--metrics", fmt.Sprintf("127.0.0.1:%d", metricsPort),
		"--no-autoupdate",
	}
}

func NamedCloudflaredArgs(localPort, metricsPort int) []string {
	return append(CloudflaredArgs(localPort, metricsPort), "run")
}

var tunnelPIDLocks = struct {
	sync.Mutex
	byPath map[string]*sync.Mutex
}{byPath: make(map[string]*sync.Mutex)}

type tunnelPIDOwner struct {
	path string
	mu   *sync.Mutex
}

func newTunnelPIDOwner(path string) tunnelPIDOwner {
	clean := filepath.Clean(path)
	tunnelPIDLocks.Lock()
	defer tunnelPIDLocks.Unlock()
	lock := tunnelPIDLocks.byPath[clean]
	if lock == nil {
		lock = &sync.Mutex{}
		tunnelPIDLocks.byPath[clean] = lock
	}
	return tunnelPIDOwner{path: path, mu: lock}
}

func (o tunnelPIDOwner) claim(pid int) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return WriteTunnelPID(o.path, pid)
}

func (o tunnelPIDOwner) release(pid int) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return RemoveOwnTunnelPIDFile(o.path, pid)
}

func RemoveOwnTunnelPIDFile(path string, pid int) error {
	if recorded, ok := ReadTunnelPID(path); ok && recorded != pid {
		return nil
	}
	return RemoveTunnelPIDFile(path)
}

func RemoveTunnelPIDFile(path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
