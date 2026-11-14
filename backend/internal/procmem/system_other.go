//go:build !linux && !darwin && !windows

package procmem

func ReadSystem() (System, error) { return System{}, ErrUnsupported }
