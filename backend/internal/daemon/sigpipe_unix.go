//go:build !windows

package daemon

import (
	"os"
	"os/signal"
	"syscall"
)

var sigpipeSink = make(chan os.Signal, 1)

func ignoreBrokenPipeSignal() {
	signal.Notify(sigpipeSink, syscall.SIGPIPE)
}
