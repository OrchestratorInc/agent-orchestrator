//go:build windows

package review

// freezeProcess is nil on Windows, which has no stop signal. Windows keeps a
// dead parent's pid on its children instead, so ForceStop still finds a
// descendant spawned during teardown by its parent pid.
var freezeProcess func(pid int) error
