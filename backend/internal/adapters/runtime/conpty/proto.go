// Package conpty implements the detached PTY-host runtime used by Windows and
// macOS agent sessions. This file contains its OS-agnostic framing protocol.
//
// Frame layout: [1-byte type][4-byte big-endian length][payload]
package conpty

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	conPTYHostProtocolVersion         = 4
	conPTYStyledOutputProtocolVersion = 2
	// Older hosts ignore MsgWatchForegroundReq and would stream raw output.
	conPTYForegroundProtocolVersion = 3
	// Older hosts ignore MsgWatchTitleReq and never report a title.
	conPTYTitleProtocolVersion = 4
)

// Message type constants. Values must match pty-host.ts MSG_* constants exactly.
const (
	MsgTerminalData       byte = 0x01 // host -> client: raw PTY output
	MsgTerminalInput      byte = 0x02 // client -> host: raw keystrokes
	MsgResize             byte = 0x03 // client -> host: JSON {cols, rows}
	MsgGetOutputReq       byte = 0x04 // client -> host: JSON {lines}
	MsgGetOutputRes       byte = 0x05 // host -> client: UTF-8 text
	MsgStatusReq          byte = 0x06 // client -> host: empty
	MsgStatusRes          byte = 0x07 // host -> client: JSON {alive, pid, exitCode?}
	MsgKillReq            byte = 0x08 // client -> host: empty
	MsgGetStyledOutputReq byte = 0x09 // client -> host: JSON {lines}
	MsgGetStyledOutputRes byte = 0x0a // host -> client: rendered UTF-8 text with ANSI styles
	MsgWatchForegroundReq byte = 0x0b // client -> host: empty; turns the connection into a foreground watcher
	MsgForegroundState    byte = 0x0c // host -> watcher: JSON ForegroundPayload, sent on change
	MsgWatchTitleReq      byte = 0x0d // client -> host: empty; turns the connection into a title watcher
	MsgTitleState         byte = 0x0e // host -> watcher: JSON TitlePayload, sent on change
)

// JSON payload structs shared with later tasks (kept minimal).

// ResizePayload is the JSON body for MsgResize.
type ResizePayload struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// StatusPayload is the JSON body for MsgStatusRes.
type StatusPayload struct {
	Alive           bool `json:"alive"`
	PID             int  `json:"pid"`
	ExitCode        *int `json:"exitCode,omitempty"`
	ProtocolVersion int  `json:"protocolVersion,omitempty"`
}

// ForegroundPayload is the MsgForegroundState body: the foreground process
// group, its leader's argv (capped), and whether it is drawing output.
type ForegroundPayload struct {
	PGID int      `json:"pgid"`
	Argv []string `json:"argv,omitempty"`
	Busy bool     `json:"busy"`
}

// TitlePayload is the MsgTitleState body: the normalized title the program last
// set. Empty means it has set none, or reset the one it had.
type TitlePayload struct {
	Title string `json:"title"`
}

// GetOutputReq is the JSON body for MsgGetOutputReq.
type GetOutputReq struct {
	Lines int `json:"lines"`
}

// EncodeMessage encodes a single frame into the binary protocol format.
// It allocates a fresh slice of exactly 5+len(payload) bytes.
// Returns an error if the payload exceeds MaxFramePayload, so the host never
// emits a frame its own parser would reject.
func EncodeMessage(msgType byte, payload []byte) ([]byte, error) {
	n := len(payload)
	if n > MaxFramePayload {
		return nil, fmt.Errorf("conpty: payload too large (%d bytes, max %d)", n, MaxFramePayload)
	}
	payloadLen := uint32(n) // safe: n <= MaxFramePayload
	frame := make([]byte, 5+n)
	frame[0] = msgType
	binary.BigEndian.PutUint32(frame[1:5], payloadLen)
	copy(frame[5:], payload)
	return frame, nil
}

// MaxFramePayload bounds one frame's payload on decode. The length field is a
// uint32 the peer controls, and nothing on the stream is legitimately larger
// than a screenful of styled output, so a well-formed-but-huge header must be
// rejected instead of allocated. On a 32-bit build 5+int(payloadLen) also
// overflows for lengths near 2^32, which would turn the bounds check below
// into a pass.
const MaxFramePayload = 64 << 20

// MessageParser is a streaming parser for the binary framing protocol.
// It accumulates arbitrary-sized chunks from a pipe/socket stream and fires
// onMessage exactly once per complete frame, regardless of chunk boundaries.
// Safe to call Feed from a single goroutine; not concurrency-safe itself.
type MessageParser struct {
	buf       []byte
	onMessage func(msgType byte, payload []byte)
	// overflow records a frame length above MaxFramePayload. The stream cannot
	// be resynchronized after that, so the parser stops dispatching instead of
	// waiting for and then allocating a payload it must reject.
	overflow bool
}

// NewMessageParser returns a parser that calls onMessage for each complete frame.
// onMessage receives a COPY of the payload so callers may retain it safely.
func NewMessageParser(onMessage func(msgType byte, payload []byte)) *MessageParser {
	return &MessageParser{onMessage: onMessage}
}

// ErrFrameTooLarge reports that the peer announced a frame longer than
// MaxFramePayload. The stream cannot be resynchronized, so the parser stops
// dispatching and callers should drop the connection.
var ErrFrameTooLarge = errors.New("conpty: frame length exceeds MaxFramePayload")

// Err reports a framing failure that ended parsing. It is nil while the parser
// is still usable.
func (p *MessageParser) Err() error {
	if p.overflow {
		return ErrFrameTooLarge
	}
	return nil
}

// Feed appends chunk to the internal buffer and dispatches all complete frames.
// It matches the semantics of MessageParser.feed in pty-host.ts exactly:
// arbitrary chunk boundaries and multiple frames per chunk are both handled.
func (p *MessageParser) Feed(chunk []byte) {
	if p.overflow {
		return
	}
	p.buf = append(p.buf, chunk...)

	for len(p.buf) >= 5 {
		payloadLen := binary.BigEndian.Uint32(p.buf[1:5])
		if payloadLen > MaxFramePayload {
			p.overflow, p.buf = true, nil
			return
		}
		frameLen := 5 + int(payloadLen)
		if len(p.buf) < frameLen {
			break
		}

		msgType := p.buf[0]
		// ponytail: explicit copy so callers that retain the slice are not
		// corrupted when p.buf grows/reallocates on a later Feed call.
		payload := make([]byte, payloadLen)
		copy(payload, p.buf[5:frameLen])

		p.buf = p.buf[frameLen:]
		p.onMessage(msgType, payload)
	}
}
