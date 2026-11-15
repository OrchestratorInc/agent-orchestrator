package conpty

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.Attacher = (*Runtime)(nil)

func (r *Runtime) Attach(ctx context.Context, handle ports.RuntimeHandle, rows, cols uint16) (ports.Stream, error) {
	sess, err := r.resolveWithEvidence(ctx, handle.ID)
	if err != nil {
		return nil, fmt.Errorf("conpty: resolve session %q for attach: %w", handle.ID, err)
	}
	if sess == nil {
		return nil, fmt.Errorf("conpty: session %q not found", handle.ID)
	}
	conn, err := dialHost(sess.addr, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("conpty: dial host for %q: %w", handle.ID, err)
	}

	pipe := newBytePipe()
	s := &loopbackStream{conn: conn, pr: pipe, pw: pipe}

	go s.pump()

	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()

	if rows > 0 && cols > 0 {
		if err := s.Resize(rows, cols); err != nil {
			_ = s.Close()
			return nil, err
		}
	}
	statusFrame, _ := EncodeMessage(MsgStatusReq, nil)
	if _, err := conn.Write(statusFrame); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

type loopbackStream struct {
	conn io.ReadWriteCloser
	pr   *bytePipe
	pw   *bytePipe

	closeOnce sync.Once
}

func (s *loopbackStream) pump() {
	processExited := false
	parser := NewMessageParser(func(msgType byte, payload []byte) {
		switch msgType {
		case MsgTerminalData:
			_, _ = s.pw.Write(payload)
		case MsgStatusRes:
			var status StatusPayload
			if json.Unmarshal(payload, &status) == nil && !status.Alive {
				processExited = true
			}
		}
	})
	buf := make([]byte, 32*1024)
	for {
		n, err := s.conn.Read(buf)
		if n > 0 {
			parser.Feed(buf[:n])
			if processExited {
				_ = s.conn.Close()
				_ = s.pw.CloseWithError(ports.ErrRuntimeProcessExited)
				return
			}
		}
		if err != nil {
			_ = s.pw.CloseWithError(err)
			return
		}
	}
}

func (s *loopbackStream) Read(p []byte) (int, error) { return s.pr.Read(p) }

func (s *loopbackStream) Write(p []byte) (int, error) {
	frame, err := EncodeMessage(MsgTerminalInput, p)
	if err != nil {
		return 0, err
	}
	if _, err := s.conn.Write(frame); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *loopbackStream) Resize(rows, cols uint16) error {
	payload, _ := json.Marshal(ResizePayload{Cols: int(cols), Rows: int(rows)})
	frame, err := EncodeMessage(MsgResize, payload)
	if err != nil {
		return err
	}
	_, err = s.conn.Write(frame)
	return err
}

func (s *loopbackStream) Close() error {
	var err error
	s.closeOnce.Do(func() {
		err = s.conn.Close()
		_ = s.pw.Close()
		_ = s.pr.Close()
	})
	return err
}
