package ptyexec

import "time"

const killGrace = 2 * time.Second

func waitForExit(done <-chan struct{}, grace time.Duration) {
	select {
	case <-done:
	case <-time.After(grace):
	}
}
