package procmem

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// System is the host's memory and CPU headroom at one instant. It answers
// "can the machine take more?" rather than "how much does AO hold?".
type System struct {
	TotalBytes     uint64
	AvailableBytes uint64
	// SwapTotalBytes and SwapUsedBytes describe configured swap; a host with
	// none reports zero for both.
	SwapTotalBytes uint64
	SwapUsedBytes  uint64
	// SwapPages counts pages ever paged in plus out. Its growth between two
	// readings is the swapping that makes a machine feel frozen.
	SwapPages uint64
	// CPUCount and Load1 give the one-minute load per core: above one, work
	// is queueing for CPU.
	CPUCount int
	Load1    float64
}

// ReadSystem reads host memory and load. It is Linux-only for now (parses
// /proc); other platforms return ErrUnsupported.
func ReadSystem() (System, error) {
	if runtime.GOOS != "linux" {
		return System{}, ErrUnsupported
	}
	sys := System{CPUCount: runtime.NumCPU()}
	if err := readMeminfo(&sys); err != nil {
		return System{}, err
	}
	// Swap activity and load are refinements; a host that hides them still
	// gets a memory reading.
	sys.SwapPages = readVMStatSwapPages()
	sys.Load1 = readLoad1()
	return sys, nil
}

func readMeminfo(sys *System) error {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return fmt.Errorf("procmem: open /proc/meminfo: %w", err)
	}
	defer f.Close()
	var swapFree uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, kib, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(kib)
		if len(fields) == 0 {
			continue
		}
		n, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			sys.TotalBytes = n * 1024
		case "MemAvailable":
			sys.AvailableBytes = n * 1024
		case "SwapTotal":
			sys.SwapTotalBytes = n * 1024
		case "SwapFree":
			swapFree = n * 1024
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("procmem: read /proc/meminfo: %w", err)
	}
	if sys.TotalBytes == 0 {
		return fmt.Errorf("procmem: /proc/meminfo missing MemTotal")
	}
	if sys.SwapTotalBytes >= swapFree {
		sys.SwapUsedBytes = sys.SwapTotalBytes - swapFree
	}
	return nil
}

// readVMStatSwapPages sums pswpin and pswpout from /proc/vmstat; zero when
// unreadable.
func readVMStatSwapPages() uint64 {
	f, err := os.Open("/proc/vmstat")
	if err != nil {
		return 0
	}
	defer f.Close()
	var pages uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), " ")
		if !ok || (key != "pswpin" && key != "pswpout") {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(val), 10, 64)
		if err == nil {
			pages += n
		}
	}
	return pages
}

// readLoad1 is the one-minute load average from /proc/loadavg; zero when
// unreadable.
func readLoad1() float64 {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}
	load, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return load
}
