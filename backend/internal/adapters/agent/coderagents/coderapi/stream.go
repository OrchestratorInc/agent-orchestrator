package coderapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// streamReadLimit matches codersdk.StreamChat.
const streamReadLimit = 1 << 22

// Stream is an open GET /api/v2/chats/{chat}/stream WebSocket. The server
// sends JSON arrays of events: a snapshot first, then live updates.
type Stream struct {
	conn *websocket.Conn
}

// StreamChat opens the chat's event stream. afterID > 0 limits the initial
// snapshot to messages after that ID, so a reconnect does not replay history.
func (c *Client) StreamChat(ctx context.Context, chatID string, afterID int64) (*Stream, error) {
	query := url.Values{}
	if afterID > 0 {
		query.Set("after_id", strconv.FormatInt(afterID, 10))
	}
	target := c.endpoint("/api/v2/chats/"+url.PathEscape(chatID)+"/stream", query)
	header := http.Header{}
	header.Set(SessionTokenHeader, c.token)
	conn, res, err := websocket.Dial(ctx, target.String(), &websocket.DialOptions{
		HTTPClient: c.http, HTTPHeader: header,
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		if errors.Is(err, ErrCrossOriginRedirect) {
			return nil, ErrCrossOriginRedirect
		}
		if res != nil && res.StatusCode >= 400 {
			return nil, &APIError{StatusCode: res.StatusCode, Message: "open chat stream"}
		}
		return nil, err
	}
	conn.SetReadLimit(streamReadLimit)
	return &Stream{conn: conn}, nil
}

// Next reads the next batch of events.
func (s *Stream) Next(ctx context.Context) ([]StreamEvent, error) {
	var batch []StreamEvent
	if err := wsjson.Read(ctx, s.conn, &batch); err != nil {
		return nil, err
	}
	return batch, nil
}

// Close closes the WebSocket.
func (s *Stream) Close() error {
	return s.conn.Close(websocket.StatusNormalClosure, "")
}
