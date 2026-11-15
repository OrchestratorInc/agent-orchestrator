package conpty

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
)

func RunHost(args []string, stdout io.Writer) int {
	opts, args := splitHostOptionArgs(args)
	if len(args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: ao pty-host [--start=attach] [--surface=lazy] [--size=COLSxROWS] <sessionId> <cwd> <shellCmd> [shellArg...]\n")
		return 1
	}

	sessionID := args[0]
	cwd := args[1]
	shellCmd := args[2]
	shellArgs := args[3:]
	if err := os.Chdir(cwd); err != nil {
		fmt.Fprintf(os.Stderr, "pty-host [%s]: chdir %s: %v\n", sessionID, cwd, err)
		return 1
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "pty-host [%s]: listen: %v\n", sessionID, err)
		return 1
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		fmt.Fprintf(os.Stderr, "pty-host [%s]: listener is not TCP\n", sessionID)
		return 1
	}
	port := tcpAddr.Port

	var pty ptyConn
	cols, rows, deferred := hostStartGrid(opts)
	if deferred {
		pty = newDeferredPTY(func(cols, rows uint16) (ptyConn, error) {
			conn, err := newConPTY(cwd, shellCmd, shellArgs, cols, rows)
			if err != nil {
				fmt.Fprintf(os.Stderr, "pty-host [%s]: newConPTY: %v\n", sessionID, err)
			}
			return conn, err
		})
	} else {
		pty, err = newConPTY(cwd, shellCmd, shellArgs, cols, rows)
		if err != nil {
			_ = ln.Close()
			fmt.Fprintf(os.Stderr, "pty-host [%s]: newConPTY: %v\n", sessionID, err)
			return 1
		}
	}

	_, _ = fmt.Fprintf(stdout, "READY:%d %d\n", pty.PID(), port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case sig := <-sigC:
			fmt.Fprintf(os.Stderr, "pty-host [%s]: signal %v, shutting down\n", sessionID, sig)
			cancel()
		case <-ctx.Done():
		}
	}()

	ring := NewRing()
	cfg := ServeConfig{
		SessionID:   sessionID,
		Listener:    ln,
		PTY:         pty,
		Ring:        ring,
		LazySurface: opts.LazySurface,
	}

	if err := Serve(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "pty-host [%s]: serve: %v\n", sessionID, err)
		return 1
	}
	return 0
}
