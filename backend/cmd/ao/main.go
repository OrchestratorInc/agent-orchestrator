package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/cli"
)

func main() {
	if strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe") == "gh" {
		os.Exit(cli.ExecuteGH())
	}
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(cli.ExitCode(err))
	}
}
