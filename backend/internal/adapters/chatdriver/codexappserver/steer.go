package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.ChatSteerer = (*conversation)(nil)

func (c *conversation) Steer(
	ctx context.Context,
	providerTurnID string,
	msg ports.ChatUserMessage,
) (ports.ChatTurnRef, error) {
	providerTurnID = c.nativeID(providerTurnID)
	if strings.TrimSpace(msg.Text) == "" {
		return ports.ChatTurnRef{}, errors.New("steer message text is empty")
	}

	if providerTurnID == "" {
		c.mu.Lock()
		providerTurnID = c.activeTurn
		c.mu.Unlock()
	}
	if providerTurnID == "" {
		return ports.ChatTurnRef{}, ports.ErrChatNoSteerableTurn
	}

	input, err := steerInput(msg)
	if err != nil {
		return ports.ChatTurnRef{}, err
	}
	params := codexproto.TurnSteerParams{
		ThreadID:       c.threadID,
		ExpectedTurnID: providerTurnID,
		Input:          input,
	}
	if msg.ClientMessageID != "" {
		params.ClientUserMessageID = &msg.ClientMessageID
	}

	var resp codexproto.TurnSteerResponse
	if err := c.conn.request(ctx, codexproto.MethodTurnSteer, params, &resp); err != nil {
		if refusal := steerRefusal(err); refusal != nil {
			return ports.ChatTurnRef{}, refusal
		}
		return ports.ChatTurnRef{}, fmt.Errorf("%s: %w", codexproto.MethodTurnSteer, err)
	}

	turn := resp.TurnID
	if turn == "" {
		turn = providerTurnID
	}
	c.mu.Lock()
	c.activeTurn = turn
	c.mu.Unlock()

	return ports.ChatTurnRef{ProviderTurnID: c.scopedID(turn)}, nil
}

func steerInput(msg ports.ChatUserMessage) ([]codexproto.UserInput, error) {
	input := make([]codexproto.UserInput, 0, 1+len(msg.Content))
	if strings.TrimSpace(msg.Text) != "" {
		text := msg.Text
		input = append(input, codexproto.UserInput{Type: codexproto.UserInputTypeText, Text: &text})
	}
	for _, item := range msg.Content {
		switch item.Type {
		case "image":
			if item.Data == "" || !strings.HasPrefix(strings.ToLower(item.MIMEType), "image/") {
				return nil, fmt.Errorf("%w: image requires data and image MIME type", ports.ErrChatSteerContentUnsupported)
			}
			url := "data:" + item.MIMEType + ";base64," + item.Data
			input = append(input, codexproto.UserInput{Type: codexproto.UserInputTypeImage, URL: &url})
		default:
			return nil, fmt.Errorf("%w: %s", ports.ErrChatSteerContentUnsupported, item.Type)
		}
	}
	if len(input) == 0 {
		return nil, errors.New("steer message has no content")
	}
	return input, nil
}

type steerErrorData struct {
	CodexErrorInfo struct {
		ActiveTurnNotSteerable *struct {
			TurnKind string `json:"turnKind"`
		} `json:"activeTurnNotSteerable"`
	} `json:"codexErrorInfo"`
}

func steerRefusal(err error) error {
	var rpcErr *rpcError
	if !errors.As(err, &rpcErr) {
		return nil
	}

	if len(rpcErr.Data) > 0 {
		var data steerErrorData
		if json.Unmarshal(rpcErr.Data, &data) == nil {
			if kind := data.CodexErrorInfo.ActiveTurnNotSteerable; kind != nil {
				return fmt.Errorf("%w: the provider is running a %s turn",
					ports.ErrChatTurnNotSteerable, kind.TurnKind)
			}
		}
	}

	if isNoActiveTurn(err) {
		return ports.ErrChatNoSteerableTurn
	}
	return nil
}
