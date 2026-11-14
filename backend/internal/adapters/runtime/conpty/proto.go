package conpty

import (
	"encoding/binary"
	"fmt"
	"math"
)

const (
	conPTYHostProtocolVersion         = 4
	conPTYStyledOutputProtocolVersion = 2
	conPTYForegroundProtocolVersion   = 3
	conPTYTitleProtocolVersion        = 4
)

const (
	MsgTerminalData       byte = 0x01
	MsgTerminalInput      byte = 0x02
	MsgResize             byte = 0x03
	MsgGetOutputReq       byte = 0x04
	MsgGetOutputRes       byte = 0x05
	MsgStatusReq          byte = 0x06
	MsgStatusRes          byte = 0x07
	MsgKillReq            byte = 0x08
	MsgGetStyledOutputReq byte = 0x09
	MsgGetStyledOutputRes byte = 0x0a
	MsgWatchForegroundReq byte = 0x0b
	MsgForegroundState    byte = 0x0c
	MsgWatchTitleReq      byte = 0x0d
	MsgTitleState         byte = 0x0e
)

type ResizePayload struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

type StatusPayload struct {
	Alive           bool `json:"alive"`
	PID             int  `json:"pid"`
	ExitCode        *int `json:"exitCode,omitempty"`
	ProtocolVersion int  `json:"protocolVersion,omitempty"`
}

type ForegroundPayload struct {
	PGID     int      `json:"pgid"`
	Argv     []string `json:"argv,omitempty"`
	Busy     bool     `json:"busy"`
	EchoOnly bool     `json:"echoOnly,omitempty"`
}

type TitlePayload struct {
	Title string `json:"title"`
}

type GetOutputReq struct {
	Lines int `json:"lines"`
}

func EncodeMessage(msgType byte, payload []byte) ([]byte, error) {
	n := len(payload)
	if n > math.MaxUint32 {
		return nil, fmt.Errorf("conpty: payload too large (%d bytes, max %d)", n, math.MaxUint32)
	}
	payloadLen := uint32(n)
	frame := make([]byte, 5+n)
	frame[0] = msgType
	binary.BigEndian.PutUint32(frame[1:5], payloadLen)
	copy(frame[5:], payload)
	return frame, nil
}

type MessageParser struct {
	buf       []byte
	onMessage func(msgType byte, payload []byte)
}

func NewMessageParser(onMessage func(msgType byte, payload []byte)) *MessageParser {
	return &MessageParser{onMessage: onMessage}
}

func (p *MessageParser) Feed(chunk []byte) {
	p.buf = append(p.buf, chunk...)

	for len(p.buf) >= 5 {
		payloadLen := binary.BigEndian.Uint32(p.buf[1:5])
		frameLen := 5 + int(payloadLen)
		if len(p.buf) < frameLen {
			break
		}

		msgType := p.buf[0]
		payload := make([]byte, payloadLen)
		copy(payload, p.buf[5:frameLen])

		p.buf = p.buf[frameLen:]
		p.onMessage(msgType, payload)
	}
}
