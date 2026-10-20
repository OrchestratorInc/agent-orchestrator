package procinventory

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ErrHostStatsUnsupported reports that the platform has no host memory
// collector.
var ErrHostStatsUnsupported = errors.New("procinventory: host memory stats unsupported on this platform")

// HostStats is one snapshot of host memory. Every field is bytes; the darwin
// kinds (wired/app/compressed/cached) are 0 where the platform does not
// distinguish them. Activity Monitor parity is approximate by nature — the
// vm_stat page buckets do not partition total exactly — so these numbers are
// for glanceable triage, not accounting.
type HostStats struct {
	TotalBytes  int64 // physical RAM (hw.memsize / MemTotal)
	UsedBytes   int64 // darwin: total-free-cached (excludes reclaimable file cache); linux: MemTotal-MemAvailable
	FreeBytes   int64 // darwin: free+speculative pages; linux: MemFree
	CachedBytes int64 // darwin: file-backed pages; linux: Cached+SReclaimable

	WiredBytes      int64 // darwin: pages wired down
	AppBytes        int64 // darwin: residual used-wired-compressed, floored at 0
	CompressedBytes int64 // darwin: pages occupied by compressor

	SwapTotalBytes int64
	SwapUsedBytes  int64
	SwapFreeBytes  int64
	// SwapMaxBytes is the honest ceiling swap can grow to: free disk on the
	// root volume (macOS swapfiles in /var/vm grow while the volume has room;
	// Bavail is the unprivileged view, and APFS container sharing makes it
	// approximate). On linux swap does not auto-grow, so this equals
	// SwapTotalBytes.
	SwapMaxBytes int64
	// PressureFreePercent is the OS free-memory estimate (darwin
	// `memory_pressure`); nil when unavailable.
	PressureFreePercent *int
}

type swapUsage struct {
	Total int64
	Used  int64
	Free  int64
}

// swapUsageRe matches one `total = 8192,00M` component: the decimal
// separator is locale-dependent (verified comma under ru_RU, dot under C).
var swapUsageRe = regexp.MustCompile(`(total|used|free)\s*=\s*(\d+)(?:[.,](\d+))?\s*([KMG])`)

// ParseSwapUsage parses `sysctl vm.swapusage` output:
// "total = 8192.00M  used = 7031.62M  free = 1160.38M  (encrypted)".
// The (encrypted) suffix and any extra text are ignored; a missing component
// is an error so silent zeros never masquerade as an empty swap.
func ParseSwapUsage(out string) (swapUsage, error) {
	var result swapUsage
	matches := swapUsageRe.FindAllStringSubmatch(out, -1)
	seen := map[string]bool{}
	for _, m := range matches {
		component := m[1]
		if seen[component] {
			continue
		}
		seen[component] = true
		value, err := parseLocaleNumber(m[2], m[3], m[4])
		if err != nil {
			return result, fmt.Errorf("swapusage %s: %w", component, err)
		}
		switch component {
		case "total":
			result.Total = value
		case "used":
			result.Used = value
		case "free":
			result.Free = value
		}
	}
	for _, required := range []string{"total", "used", "free"} {
		if !seen[required] {
			return result, fmt.Errorf("swapusage: missing %q component in %q", required, out)
		}
	}
	return result, nil
}

// parseLocaleNumber converts an integer part, an optional locale-decimal
// fraction (comma or dot), and a binary K/M/G suffix into bytes.
func parseLocaleNumber(intPart, fracPart, suffix string) (int64, error) {
	whole, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number %q", intPart+fracPart+suffix)
	}
	multiplier := int64(1)
	switch suffix {
	case "K":
		multiplier = 1 << 10
	case "M":
		multiplier = 1 << 20
	case "G":
		multiplier = 1 << 30
	}
	value := whole * multiplier
	if fracPart != "" {
		frac, err := strconv.ParseInt(fracPart, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid fraction %q", fracPart)
		}
		scale := int64(1)
		for range fracPart {
			scale *= 10
		}
		value += frac * multiplier / scale
	}
	return value, nil
}

type vmStat struct {
	PageSize    int64
	Free        int64
	Speculative int64
	Wired       int64
	FileBacked  int64
	Compressor  int64
}

var (
	vmStatPageSizeRe = regexp.MustCompile(`page size of (\d+) bytes`)
	vmStatLineRe     = regexp.MustCompile(`^([^:]+):\s*(\d+)`)
)

// vmStatRequiredKeys maps vm_stat labels onto vmStat fields. All are required
// — a missing key is an error so upstream format drift surfaces instead of
// reporting silent zeros. Values carry a trailing period ("25530."), and the
// value parser strips every non-digit defensively.
var vmStatRequiredKeys = []struct {
	label string
	apply func(s *vmStat, v int64)
}{
	{"Pages free", func(s *vmStat, v int64) { s.Free = v }},
	{"Pages speculative", func(s *vmStat, v int64) { s.Speculative = v }},
	{"Pages wired down", func(s *vmStat, v int64) { s.Wired = v }},
	{"File-backed pages", func(s *vmStat, v int64) { s.FileBacked = v }},
	{"Pages occupied by compressor", func(s *vmStat, v int64) { s.Compressor = v }},
}

// ParseVmStat parses `vm_stat` output. The page size comes from the header
// ("(page size of 16384 bytes)" — 16384 on Apple Silicon, 4096 on Intel);
// the returned page values are raw page counts, scaled by the caller.
func ParseVmStat(out string) (vmStat, error) {
	var result vmStat
	pageSize := vmStatPageSizeRe.FindStringSubmatch(out)
	if pageSize == nil {
		return result, fmt.Errorf("vm_stat: page size header not found")
	}
	size, err := strconv.ParseInt(pageSize[1], 10, 64)
	if err != nil {
		return result, fmt.Errorf("vm_stat: invalid page size %q", pageSize[1])
	}
	result.PageSize = size

	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		m := vmStatLineRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		key := strings.TrimSpace(m[1])
		digits := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, m[2])
		if digits == "" {
			continue
		}
		for _, required := range vmStatRequiredKeys {
			if key != required.label || seen[key] {
				continue
			}
			seen[key] = true
			value, err := strconv.ParseInt(digits, 10, 64)
			if err != nil {
				return result, fmt.Errorf("vm_stat: invalid value for %q", key)
			}
			required.apply(&result, value)
		}
	}
	for _, required := range vmStatRequiredKeys {
		if !seen[required.label] {
			return result, fmt.Errorf("vm_stat: missing %q line", required.label)
		}
	}
	return result, nil
}

type memInfo struct {
	Total        int64
	Available    int64
	Free         int64
	Cached       int64
	SReclaimable int64
	SwapTotal    int64
	SwapFree     int64
}

// ParseMeminfo parses /proc/meminfo ("MemTotal:  25769803776 kB", values in
// kiB). MemTotal, MemAvailable, MemFree, Cached, SwapTotal and SwapFree are
// required; SReclaimable is optional (0 when absent).
func ParseMeminfo(out string) (memInfo, error) {
	var result memInfo
	seen := map[string]bool{}
	fields := map[string]*int64{
		"MemTotal":     &result.Total,
		"MemAvailable": &result.Available,
		"MemFree":      &result.Free,
		"Cached":       &result.Cached,
		"SReclaimable": &result.SReclaimable,
		"SwapTotal":    &result.SwapTotal,
		"SwapFree":     &result.SwapFree,
	}
	for _, line := range strings.Split(out, "\n") {
		m := vmStatLineRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		target, ok := fields[strings.TrimSpace(m[1])]
		if !ok || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		value, err := strconv.ParseInt(strings.TrimSpace(m[2]), 10, 64)
		if err != nil {
			return result, fmt.Errorf("meminfo: invalid value for %q", m[1])
		}
		*target = value * 1024
	}
	for name := range fields {
		if name == "SReclaimable" {
			continue
		}
		if !seen[name] {
			return result, fmt.Errorf("meminfo: missing %q line", name)
		}
	}
	return result, nil
}

// pressureFreePercentRe matches `memory_pressure`'s
// "System-wide memory free percentage: 44%" line.
var pressureFreePercentRe = regexp.MustCompile(`System-wide memory free percentage:\s*(\d+)%`)

// ParsePressureFreePercent returns the OS free-memory estimate; ok is false
// when the line is absent (never an error — pressure is best-effort).
func ParsePressureFreePercent(out string) (int, bool) {
	m := pressureFreePercentRe.FindStringSubmatch(out)
	if m == nil {
		return 0, false
	}
	value, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return value, true
}

// composeDarwinHostStats applies the darwin accounting on top of the parsed
// pieces: Free counts reclaimable speculative pages alongside truly free
// ones, Used excludes the reclaimable file cache (the Activity Monitor
// convention), and App is the residual.
func composeDarwinHostStats(total int64, swap swapUsage, vm vmStat, freeDisk int64, pressure *int) HostStats {
	pageSize := vm.PageSize
	free := (vm.Free + vm.Speculative) * pageSize
	cached := vm.FileBacked * pageSize
	wired := vm.Wired * pageSize
	compressed := vm.Compressor * pageSize
	used := max64(0, total-free-cached)
	return HostStats{
		TotalBytes:          total,
		UsedBytes:           used,
		FreeBytes:           free,
		CachedBytes:         cached,
		WiredBytes:          wired,
		AppBytes:            max64(0, used-wired-compressed),
		CompressedBytes:     compressed,
		SwapTotalBytes:      swap.Total,
		SwapUsedBytes:       swap.Used,
		SwapFreeBytes:       swap.Free,
		SwapMaxBytes:        swap.Total + freeDisk,
		PressureFreePercent: pressure,
	}
}

// composeLinuxHostStats applies the linux accounting: used is the kernel's
// own availability estimate subtracted from total, and swap does not
// auto-grow, so the ceiling is the current swap size.
func composeLinuxHostStats(m memInfo) HostStats {
	return HostStats{
		TotalBytes:     m.Total,
		UsedBytes:      max64(0, m.Total-m.Available),
		FreeBytes:      m.Free,
		CachedBytes:    m.Cached + m.SReclaimable,
		SwapTotalBytes: m.SwapTotal,
		SwapUsedBytes:  max64(0, m.SwapTotal-m.SwapFree),
		SwapFreeBytes:  m.SwapFree,
		SwapMaxBytes:   m.SwapTotal,
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
