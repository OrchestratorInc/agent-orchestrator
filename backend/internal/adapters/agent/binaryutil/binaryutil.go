package binaryutil

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type BinarySpec struct {
	Label string

	Names    []string
	WinNames []string

	UnixPaths     []string
	UnixHomePaths [][]string

	WinPaths []WinPath

	NodeManaged bool

	ValidateIdentity func(ctx context.Context, path string) bool
}

type WinBase int

const (
	WinAppData WinBase = iota
	WinLocalAppData
	WinHome
)

type WinPath struct {
	Base  WinBase
	Parts []string
}

func ResolveBinary(ctx context.Context, spec BinarySpec) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	names := spec.Names
	if runtime.GOOS == "windows" {
		names = spec.WinNames
	}
	if managed, ok := ManagedBinary(names...); ok &&
		(spec.ValidateIdentity == nil || spec.ValidateIdentity(ctx, managed)) {
		return managed, nil
	}

	var pathHits map[string]struct{}
	if spec.ValidateIdentity != nil {
		pathHits = make(map[string]struct{})
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if path, err := exec.LookPath(name); err == nil && path != "" {
			if spec.ValidateIdentity == nil {
				return path, nil
			}
			pathHits[candidateKey(path)] = struct{}{}
			if spec.ValidateIdentity(ctx, path) {
				return path, nil
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
	}

	candidates, err := resolveBinaryCandidates(ctx, spec)
	if err != nil {
		return "", err
	}

	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if _, alreadyChecked := pathHits[candidateKey(candidate)]; alreadyChecked {
			continue
		}
		if !hookutil.IsExecutableFile(candidate) {
			continue
		}
		if spec.ValidateIdentity == nil || spec.ValidateIdentity(ctx, candidate) {
			return candidate, nil
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}

	return "", fmt.Errorf("%s: %w", spec.Label, ports.ErrAgentBinaryNotFound)
}

func resolveBinaryCandidates(ctx context.Context, spec BinarySpec) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var candidates []string
	for _, name := range namesForPlatform(spec) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if path, err := exec.LookPath(name); err == nil && path != "" {
			candidates = appendUniqueCandidate(candidates, path)
		}
	}

	fallbacks, err := binaryFallbackCandidates(ctx, spec)
	if err != nil {
		return nil, err
	}
	for _, candidate := range fallbacks {
		candidates = appendUniqueCandidate(candidates, candidate)
	}
	return candidates, nil
}

func binaryFallbackCandidates(ctx context.Context, spec BinarySpec) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var candidates []string
	if runtime.GOOS == "windows" {
		home, _ := os.UserHomeDir()
		appData := os.Getenv("APPDATA")
		localAppData := os.Getenv("LOCALAPPDATA")
		for _, wp := range spec.WinPaths {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			var base string
			switch wp.Base {
			case WinAppData:
				base = appData
			case WinLocalAppData:
				base = localAppData
			case WinHome:
				base = home
			}
			if base == "" {
				continue
			}
			candidates = append(candidates, filepath.Join(append([]string{base}, wp.Parts...)...))
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidates = append(candidates, WindowsPackageManagerBinCandidates(executableNames(spec)...)...)
		return candidates, nil
	}

	candidates = append(candidates, spec.UnixPaths...)
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, joinAll(home, spec.UnixHomePaths)...)
		candidates = append(candidates, UnixPackageManagerBinCandidates(home, spec.Names...)...)
		if spec.NodeManaged {
			nodeManagerCandidates, err := UnixNodeManagerBinCandidates(ctx, home, spec.Names...)
			if err != nil {
				return nil, err
			}
			candidates = append(candidates, nodeManagerCandidates...)
		}
	}
	return candidates, nil
}

func namesForPlatform(spec BinarySpec) []string {
	if runtime.GOOS == "windows" {
		return spec.WinNames
	}
	return spec.Names
}

func appendUniqueCandidate(candidates []string, candidate string) []string {
	if candidate == "" {
		return candidates
	}
	key := candidateKey(candidate)
	for _, existing := range candidates {
		if candidateKey(existing) == key {
			return candidates
		}
	}
	return append(candidates, candidate)
}

func candidateKey(candidate string) string {
	key := filepath.Clean(candidate)
	if runtime.GOOS == "windows" {
		return strings.ToLower(key)
	}
	return key
}

func UnixPackageManagerBinCandidates(home string, names ...string) []string {
	if home == "" {
		return nil
	}
	out := make([]string, 0, len(names)*7)
	for _, name := range names {
		out = append(out,
			filepath.Join(string(filepath.Separator), "home", "linuxbrew", ".linuxbrew", "bin", name),
			filepath.Join(string(filepath.Separator), "snap", "bin", name),
			filepath.Join(home, ".bun", "bin", name),
			filepath.Join(home, ".yarn", "bin", name),
			filepath.Join(home, ".config", "yarn", "global", "node_modules", ".bin", name),
			filepath.Join(home, ".local", "share", "mise", "shims", name),
			filepath.Join(home, ".asdf", "shims", name),
		)
	}
	return out
}

func WindowsPackageManagerBinCandidates(names ...string) []string {
	home, _ := os.UserHomeDir()
	appData := os.Getenv("APPDATA")
	localAppData := os.Getenv("LOCALAPPDATA")
	programFiles := os.Getenv("ProgramFiles")
	programFilesX86 := os.Getenv("ProgramFiles(x86)")
	programData := os.Getenv("ProgramData")
	if programData == "" {
		programData = os.Getenv("PROGRAMDATA")
	}
	voltaHome := os.Getenv("VOLTA_HOME")
	nvmSymlink := os.Getenv("NVM_SYMLINK")
	out := make([]string, 0, len(names)*8)
	for _, name := range names {
		if home != "" {
			out = append(out,
				filepath.Join(home, "scoop", "shims", name+".exe"),
				filepath.Join(home, "scoop", "shims", name+".cmd"),
				filepath.Join(home, ".local", "bin", name+".exe"),
				filepath.Join(home, ".local", "bin", name+".cmd"),
			)
		}
		if appData != "" {
			out = append(out,
				filepath.Join(appData, "npm", name+".cmd"),
				filepath.Join(appData, "npm", name+".exe"),
				filepath.Join(appData, "npm", name),
			)
		}
		if programData != "" {
			out = append(out,
				filepath.Join(programData, "chocolatey", "bin", name+".exe"),
				filepath.Join(programData, "chocolatey", "bin", name+".bat"),
				filepath.Join(programData, "chocolatey", "bin", name+".cmd"),
			)
		}
		if localAppData != "" {
			out = append(out,
				filepath.Join(localAppData, "npm", name+".cmd"),
				filepath.Join(localAppData, "npm", name+".exe"),
				filepath.Join(localAppData, "Microsoft", "WinGet", "Links", name+".exe"),
				filepath.Join(localAppData, "pnpm", name+".cmd"),
				filepath.Join(localAppData, "pnpm", name+".exe"),
				filepath.Join(localAppData, "Yarn", "bin", name+".cmd"),
				filepath.Join(localAppData, "Yarn", "bin", name+".exe"),
				filepath.Join(localAppData, "Volta", "bin", name+".cmd"),
				filepath.Join(localAppData, "Volta", "bin", name+".exe"),
				filepath.Join(localAppData, "mise", "shims", name+".exe"),
				filepath.Join(localAppData, "mise", "shims", name+".cmd"),
			)
		}
		if programFiles != "" {
			out = append(out, filepath.Join(programFiles, "WinGet", "Links", name+".exe"))
		}
		if programFilesX86 != "" {
			out = append(out, filepath.Join(programFilesX86, "WinGet", "Links", name+".exe"))
		}
		if voltaHome != "" {
			out = append(out,
				filepath.Join(voltaHome, "bin", name+".cmd"),
				filepath.Join(voltaHome, "bin", name+".exe"),
			)
		}
		if nvmSymlink != "" {
			out = append(out,
				filepath.Join(nvmSymlink, name+".cmd"),
				filepath.Join(nvmSymlink, name+".exe"),
			)
		}
	}
	return out
}

func executableNames(spec BinarySpec) []string {
	if len(spec.Names) > 0 {
		return spec.Names
	}
	out := make([]string, 0, len(spec.WinNames))
	for _, name := range spec.WinNames {
		name = strings.TrimSuffix(name, ".exe")
		name = strings.TrimSuffix(name, ".cmd")
		name = strings.TrimSuffix(name, ".bat")
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func joinAll(base string, entries [][]string) []string {
	out := make([]string, 0, len(entries))
	for _, parts := range entries {
		out = append(out, filepath.Join(append([]string{base}, parts...)...))
	}
	return out
}

func NodeManagedUnixHomePaths(binary string, extras ...[]string) [][]string {
	paths := make([][]string, 0, 3+len(extras))
	paths = append(paths,
		[]string{".npm-global", "bin", binary},
		[]string{".npm", "bin", binary},
		[]string{".local", "bin", binary},
	)
	paths = append(paths, extras...)
	return paths
}

func UnixNodeManagerBinCandidates(ctx context.Context, home string, names ...string) ([]string, error) {
	out := make([]string, 0, len(names)*3)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		volta, err := unixVoltaBinCandidates(ctx, home, name)
		if err != nil {
			return nil, err
		}
		out = append(out, volta...)
		nvm, err := unixNVMNodeBinCandidates(ctx, home, name)
		if err != nil {
			return nil, err
		}
		out = append(out, nvm...)
		fnm, err := unixFNMNodeBinCandidates(ctx, home, name)
		if err != nil {
			return nil, err
		}
		out = append(out, fnm...)
	}
	return out, nil
}

func unixVoltaBinCandidates(ctx context.Context, home, name string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	voltaHome := os.Getenv("VOLTA_HOME")
	if voltaHome == "" {
		voltaHome = filepath.Join(home, ".volta")
	}
	candidate := filepath.Join(voltaHome, "bin", name)
	if hookutil.IsExecutableFile(candidate) {
		return []string{candidate}, nil
	}
	return nil, ctx.Err()
}

func unixNVMNodeBinCandidates(ctx context.Context, home, name string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pattern := filepath.Join(home, ".nvm", "versions", "node", "*", "bin", name)
	matches, err := sortedVersionedBinMatches(ctx, pattern)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if defaultVersion, err := readVersionAlias(ctx, filepath.Join(home, ".nvm", "alias", "default")); err != nil {
		return nil, err
	} else if defaultVersion != "" {
		matches = preferVersion(matches, defaultVersion)
	}
	return matches, nil
}

func unixFNMNodeBinCandidates(ctx context.Context, home, name string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fnmDir := os.Getenv("FNM_DIR")
	if fnmDir == "" {
		fnmDir = defaultFNMDir(home, os.Getenv("XDG_DATA_HOME"), runtime.GOOS)
	}
	return sortedVersionedBinMatches(ctx, filepath.Join(fnmDir, "node-versions", "*", "installation", "bin", name))
}

func defaultFNMDir(home, xdgDataHome, goos string) string {
	if xdgDataHome != "" {
		return filepath.Join(xdgDataHome, "fnm")
	}
	if goos == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "fnm")
	}
	return filepath.Join(home, ".local", "share", "fnm")
}

func sortedVersionedBinMatches(ctx context.Context, pattern string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return compareNodeVersions(versionDirName(matches[i]), versionDirName(matches[j])) > 0
	})
	return matches, nil
}

func versionDirName(path string) string {
	dir := filepath.Dir(path)
	if filepath.Base(dir) == "bin" {
		dir = filepath.Dir(dir)
	}
	if filepath.Base(dir) == "installation" {
		dir = filepath.Dir(dir)
	}
	return filepath.Base(dir)
}

func readVersionAlias(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], nil
}

func preferVersion(paths []string, version string) []string {
	version = normalizeNodeVersion(version)
	if version == "" {
		return paths
	}
	for i, path := range paths {
		if normalizeNodeVersion(versionDirName(path)) != version {
			continue
		}
		out := make([]string, 0, len(paths))
		out = append(out, path)
		out = append(out, paths[:i]...)
		out = append(out, paths[i+1:]...)
		return out
	}
	return paths
}

func compareNodeVersions(a, b string) int {
	av := parseNodeVersion(a)
	bv := parseNodeVersion(b)
	for i := 0; i < 3; i++ {
		if av.parts[i] > bv.parts[i] {
			return 1
		}
		if av.parts[i] < bv.parts[i] {
			return -1
		}
	}
	if av.valid && !bv.valid {
		return 1
	}
	if !av.valid && bv.valid {
		return -1
	}
	return strings.Compare(a, b)
}

type nodeVersion struct {
	parts [3]int
	valid bool
}

func parseNodeVersion(version string) nodeVersion {
	version = normalizeNodeVersion(version)
	if version == "" {
		return nodeVersion{}
	}
	fields := strings.Split(version, ".")
	var parsed nodeVersion
	for i := 0; i < len(fields) && i < 3; i++ {
		n, err := strconv.Atoi(fields[i])
		if err != nil {
			return nodeVersion{}
		}
		parsed.parts[i] = n
	}
	parsed.valid = true
	return parsed
}

func normalizeNodeVersion(version string) string {
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "v")
	return version
}
