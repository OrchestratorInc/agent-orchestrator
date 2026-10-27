package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
)

// runBrowser runs an agent-browser command in the sandbox's browser, the one
// the person watches in AO's Browser panel. The browser runs in this sandbox:
// it reads and saves files here itself, at paths relative to this directory.
// AO_BROWSER_SOCKET is the worker's socket to it.
func runBrowser(args []string) error {
	socket := os.Getenv("AO_BROWSER_SOCKET")
	if socket == "" {
		return errors.New("this sandbox has no browser")
	}
	if len(args) == 0 {
		return errors.New("usage: ao browser <agent-browser command> [args...]")
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"args": args, "dir": dir})
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
	// No timeout: a command may wait on the page as long as it was told to.
	response, err := client.Post("http://browser/agent", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("browser: %w", err)
	}
	defer response.Body.Close()
	var result struct {
		Output string `json:"output"`
		Error  string `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return fmt.Errorf("browser: %s", response.Status)
	}
	fmt.Print(result.Output)
	if result.Error != "" {
		return errors.New(result.Error)
	}
	return nil
}
