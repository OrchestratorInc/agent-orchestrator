package main

import (
	"context"
	"flag"
	"github.com/aoagents/agent-orchestrator/proxy-host/internal/host"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	root := flag.String("data-dir", "", "AO-owned proxy directory")
	port := flag.Int("port", 0, "loopback listener port")
	build := flag.String("build", "", "the daemon's id for this helper binary, reported by /ao/status")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := host.Run(ctx, *root, *port, os.Getenv("AO_PROXY_CONTROL_KEY"), os.Getenv("AO_PROXY_INFERENCE_KEY"), *build); err != nil {
		log.Fatal(err)
	}
}
