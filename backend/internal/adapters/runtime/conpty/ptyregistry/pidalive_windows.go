//go:build windows

package ptyregistry

import "github.com/aoagents/agent-orchestrator/backend/internal/processalive"

// defaultPidAlive 复用共享进程观察，注册记录只在确认死亡后才允许清理。
func defaultPidAlive(pid int) (bool, error) {
	return processalive.Probe(pid)
}
