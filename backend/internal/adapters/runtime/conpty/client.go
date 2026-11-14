package conpty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/inputdelay"
)

const (
	ptyInputChunkRunes = 512
	ptyInputChunkDelay = 15 * time.Millisecond
	ptyInputEnterDelay = 300 * time.Millisecond

	dialTimeout      = 3 * time.Second
	getOutputTimeout = 3 * time.Second
	isAliveTimeout   = 2 * time.Second
)

func dialHost(addr string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("tcp", addr, timeout)
}

func dialHostContext(ctx context.Context, addr string, timeout time.Duration) (net.Conn, error) {
	dialer := net.Dialer{Timeout: timeout}
	return dialer.DialContext(ctx, "tcp", addr)
}

func armClientDeadline(ctx context.Context, conn net.Conn, timeout time.Duration) func() {
	deadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	return func() { _ = stop() }
}

func clientSendMessage(addr, message string) error {
	conn, err := dialHost(addr, dialTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	runes := []rune(message)
	for i := 0; i < len(runes); i += ptyInputChunkRunes {
		end := i + ptyInputChunkRunes
		if end > len(runes) {
			end = len(runes)
		}
		chunk := string(runes[i:end])
		frame, err := EncodeMessage(MsgTerminalInput, []byte(chunk))
		if err != nil {
			return err
		}
		if _, err := conn.Write(frame); err != nil {
			return err
		}
		if end < len(runes) {
			time.Sleep(ptyInputChunkDelay)
		}
	}

	if delay := inputdelay.ForMessage(message, ptyInputEnterDelay); delay > 0 {
		time.Sleep(delay)
	}
	frame, err := EncodeMessage(MsgTerminalInput, []byte("\r"))
	if err != nil {
		return err
	}
	_, err = conn.Write(frame)
	return err
}

func clientSendInput(addr, input string) error {
	conn, err := dialHost(addr, dialTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	frame, err := EncodeMessage(MsgTerminalInput, []byte(input))
	if err != nil {
		return err
	}
	_, err = conn.Write(frame)
	return err
}

func clientGetOutput(ctx context.Context, addr string, lines int) (string, error) {
	return clientReadOutput(ctx, addr, lines, MsgGetOutputReq, MsgGetOutputRes)
}

func clientGetStyledOutput(ctx context.Context, addr string, lines int) (string, error) {
	return clientReadOutput(ctx, addr, lines, MsgGetStyledOutputReq, MsgGetStyledOutputRes)
}

func clientReadOutput(ctx context.Context, addr string, lines int, requestType, responseType byte) (string, error) {
	conn, err := dialHostContext(ctx, addr, getOutputTimeout)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", nil
	}
	defer func() { _ = conn.Close() }()
	stopCancellation := armClientDeadline(ctx, conn, getOutputTimeout)
	defer stopCancellation()

	req, _ := json.Marshal(GetOutputReq{Lines: lines})
	reqFrame, _ := EncodeMessage(requestType, req)
	if _, err := conn.Write(reqFrame); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", nil
	}

	resultC := make(chan string, 1)
	parser := NewMessageParser(func(msgType byte, payload []byte) {
		if msgType == responseType {
			select {
			case resultC <- string(payload):
			default:
			}
		}
	})

	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			parser.Feed(buf[:n])
		}
		select {
		case text := <-resultC:
			return text, nil
		default:
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return "", ctxErr
			}
			break
		}
	}
	select {
	case text := <-resultC:
		return text, nil
	default:
		return "", nil
	}
}

func clientIsAlive(addr string) (alive bool, transientErr error) {
	_, hostAlive, err := clientStatus(addr)
	return hostAlive, err
}

func clientStatus(addr string) (status StatusPayload, hostAlive bool, transientErr error) {
	return clientStatusContext(context.Background(), addr)
}

func clientStatusContext(ctx context.Context, addr string) (status StatusPayload, hostAlive bool, transientErr error) {
	conn, err := dialHostContext(ctx, addr, isAliveTimeout)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return StatusPayload{}, false, ctxErr
		}
		if isTimeout(err) {
			return StatusPayload{}, false, err
		}
		if isConnRefused(err) {
			return StatusPayload{}, false, nil
		}
		return StatusPayload{}, false, err
	}
	defer func() { _ = conn.Close() }()
	stopCancellation := armClientDeadline(ctx, conn, isAliveTimeout)
	defer stopCancellation()

	statusReqFrame, _ := EncodeMessage(MsgStatusReq, nil)
	if _, err := conn.Write(statusReqFrame); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return StatusPayload{}, false, ctxErr
		}
		return StatusPayload{}, false, err
	}

	type statusResult struct {
		payload StatusPayload
		err     error
	}
	statusC := make(chan statusResult, 1)
	parser := NewMessageParser(func(msgType byte, payload []byte) {
		if msgType == MsgStatusRes {
			var sp StatusPayload
			decodeErr := json.Unmarshal(payload, &sp)
			select {
			case statusC <- statusResult{payload: sp, err: decodeErr}:
			default:
			}
		}
	})

	buf := make([]byte, 4096)
	var lastErr error
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			parser.Feed(buf[:n])
		}
		select {
		case result := <-statusC:
			if result.err != nil {
				return StatusPayload{}, false, result.err
			}
			return result.payload, true, nil
		default:
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return StatusPayload{}, false, ctxErr
			}
			lastErr = err
			break
		}
	}
	select {
	case result := <-statusC:
		if result.err != nil {
			return StatusPayload{}, false, result.err
		}
		return result.payload, true, nil
	default:
		return StatusPayload{}, false, lastErr
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func isConnRefused(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	const wsaeconnrefused = syscall.Errno(10061)
	return errors.Is(err, wsaeconnrefused)
}

func clientKill(addr string) error {
	conn, err := dialHost(addr, isAliveTimeout)
	if err != nil {
		if isConnRefused(err) {
			return nil
		}
		return fmt.Errorf("dial pty-host for kill: %w", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(isAliveTimeout))
	killFrame, _ := EncodeMessage(MsgKillReq, nil)
	if _, err := conn.Write(killFrame); err != nil {
		return fmt.Errorf("write pty-host kill request: %w", err)
	}
	return nil
}

func clientWatchForeground(ctx context.Context, addr string, fn func(ForegroundPayload)) error {
	return clientWatchFrames(ctx, addr, MsgWatchForegroundReq, MsgForegroundState, func(payload []byte) {
		var state ForegroundPayload
		if json.Unmarshal(payload, &state) == nil {
			fn(state)
		}
	})
}

func clientWatchTitle(ctx context.Context, addr string, fn func(TitlePayload)) error {
	return clientWatchFrames(ctx, addr, MsgWatchTitleReq, MsgTitleState, func(payload []byte) {
		var state TitlePayload
		if json.Unmarshal(payload, &state) == nil {
			fn(state)
		}
	})
}

func clientWatchFrames(ctx context.Context, addr string, request, response byte, fn func(payload []byte)) error {
	conn, err := dialHostContext(ctx, addr, dialTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	frame, err := EncodeMessage(request, nil)
	if err != nil {
		return err
	}
	if _, err := conn.Write(frame); err != nil {
		return err
	}
	parser := NewMessageParser(func(msgType byte, payload []byte) {
		if msgType == response {
			fn(payload)
		}
	})
	buf := make([]byte, 32*1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			parser.Feed(buf[:n])
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return err
		}
	}
}
