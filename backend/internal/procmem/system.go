package procmem

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

type System struct {
	TotalBytes     uint64
	AvailableBytes uint64
	SwapTotalBytes uint64
	SwapUsedBytes  uint64
	SwapPages      uint64
	SwapPageBytes  uint64
	CPUCount       int
	Load1          float64
	CPUBusyTicks   uint64
	CPUTotalTicks  uint64
	PressureRaw    float64
	PressureSource string
}

const (
	PressureSourcePSI          = "psi"
	PressureSourceAvailablePct = "available_pct"
	PressureSourceMemoryStatus = "memorystatus"
)

type Pressure struct {
	Raw    float64
	Source string
}

func availablePressure(sys System) float64 {
	if sys.TotalBytes == 0 {
		return 0
	}
	return 100 - float64(sys.AvailableBytes)/float64(sys.TotalBytes)*100
}

func ParsePSISome10(contents string) (float64, bool) {
	for _, line := range strings.Split(contents, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "some" {
			continue
		}
		for _, field := range fields[1:] {
			if val, ok := strings.CutPrefix(field, "avg10="); ok {
				n, err := strconv.ParseFloat(val, 64)
				return n, err == nil
			}
		}
	}
	return 0, false
}

func ParseCPUTicks(contents string) (busy, total uint64) {
	for _, line := range strings.Split(contents, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		for i, f := range fields[1:] {
			if i == 8 || i == 9 {
				continue
			}
			n, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				return 0, 0
			}
			total += n
			if i != 3 && i != 4 {
				busy += n
			}
		}
		return busy, total
	}
	return 0, 0
}

type VMStat struct {
	PageSize   uint64
	Anonymous  uint64
	Purgeable  uint64
	Wired      uint64
	Compressor uint64
	PageIns    uint64
	PageOuts   uint64
	SwapIns    uint64
	SwapOuts   uint64
}

func (v VMStat) AvailableBytes(total uint64) uint64 {
	used := (v.Anonymous - min(v.Purgeable, v.Anonymous) + v.Wired + v.Compressor) * v.PageSize
	return total - min(used, total)
}

func ParseVMStat(contents string) (VMStat, error) {
	stat := VMStat{}
	for line := range strings.Lines(contents) {
		line = strings.TrimSpace(line)
		if stat.PageSize == 0 {
			if _, rest, ok := strings.Cut(line, "page size of "); ok {
				size, _, _ := strings.Cut(rest, " ")
				if n, err := strconv.ParseUint(size, 10, 64); err == nil {
					stat.PageSize = n
				}
			}
		}
		label, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(value), "."), 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(label) {
		case "Anonymous pages":
			stat.Anonymous = n
		case "Pages purgeable":
			stat.Purgeable = n
		case "Pages wired down":
			stat.Wired = n
		case "Pages occupied by compressor":
			stat.Compressor = n
		case "Pageins":
			stat.PageIns = n
		case "Pageouts":
			stat.PageOuts = n
		case "Swapins":
			stat.SwapIns = n
		case "Swapouts":
			stat.SwapOuts = n
		}
	}
	if stat.PageSize == 0 {
		return VMStat{}, fmt.Errorf("procmem: vm_stat did not name a page size")
	}
	return stat, nil
}

func parseSwapUsage(raw []byte) (total, used uint64) {
	if len(raw) < 24 {
		return 0, 0
	}
	return binary.LittleEndian.Uint64(raw[0:8]), binary.LittleEndian.Uint64(raw[16:24])
}

func parseLoadavg(raw []byte) float64 {
	if len(raw) < 24 {
		return 0
	}
	scale := binary.LittleEndian.Uint64(raw[16:24])
	if scale == 0 {
		return 0
	}
	return float64(binary.LittleEndian.Uint32(raw[0:4])) / float64(scale)
}
