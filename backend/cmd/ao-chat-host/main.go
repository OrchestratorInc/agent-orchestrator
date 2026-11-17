package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
)

func main() {
	if err := persistenthost.RunCommand(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, persistenthost.ErrCommandUsage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
