// Package codexrollout forks and rolls back a Codex app-server thread. The
// desktop daemon and AO Cloud's worker share it, so a Codex conversation is
// forked at a turn, or rolled back to before one, the same way wherever it
// runs.
//
// Each wire shape was measured against a live `codex app-server` 0.146.0
// rather than read off the generated schema, because two of them do not do
// what their names suggest:
//
//   - thread/rollback takes {threadId, numTurns} and drops numTurns turns from
//     the END of the thread. It takes no turn id at all, which is why rolling
//     back to a turn has to read the thread's own turn list and count
//     backwards from it.
//   - thread/fork takes {threadId, lastTurnId?} and answers with a NEW thread
//     id. Omitting cwd makes the fork inherit the source thread's working
//     directory, which is the only directory whose files match the history
//     being copied.
//
// thread/rollback works on 0.146.0. Codex 0.162 replaces it with
// thread/revert, taking {threadId, beforeTurnId} and retaining the prefix
// before that turn in the same thread. Neither operation reverts workspace
// files. Only an unknown-method refusal is safe to retry through revert.
package codexrollout

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Requester sends one JSON-RPC request to a Codex app-server and decodes its
// result into result, which may be nil.
type Requester interface {
	Request(ctx context.Context, method string, params, result any) error
}

// RPCError is an error the app-server answered with. A Requester's errors
// should implement it, through wrapping too, so Rollback can tell a removed
// method from an ordinary refusal.
type RPCError interface {
	error
	RPCCode() int
	RPCMessage() string
}

// Turn is a thread turn as the app-server lists it.
type Turn struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// ErrTurnNotInHistory means the turn to roll back to isn't in the thread's
// own history: already discarded, or from before a resume.
var ErrTurnNotInHistory = errors.New("turn is not in the provider's history")

// ReadTurns is the thread's own turn list, oldest first.
func ReadTurns(ctx context.Context, conn Requester, threadID string) ([]Turn, error) {
	var resp struct {
		Thread struct {
			Turns []Turn `json:"turns"`
		} `json:"thread"`
	}
	if err := conn.Request(ctx, "thread/read", map[string]any{
		"threadId":     threadID,
		"includeTurns": true,
	}, &resp); err != nil {
		return nil, fmt.Errorf("thread/read: %w", err)
	}
	return resp.Thread.Turns, nil
}

// Fork branches the thread into a new one and returns its id. A nil
// lastTurnID copies the whole history; a turn id copies through that turn,
// inclusively. The original thread is unchanged, and the fork keeps its
// working directory.
func Fork(ctx context.Context, conn Requester, threadID string, lastTurnID *string) (string, error) {
	params := map[string]any{"threadId": threadID}
	if lastTurnID != nil {
		anchor := strings.TrimSpace(*lastTurnID)
		if anchor == "" {
			return "", errors.New("fork anchor must not be blank")
		}
		params["lastTurnId"] = anchor
	}
	var resp struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := conn.Request(ctx, "thread/fork", params, &resp); err != nil {
		return "", fmt.Errorf("thread/fork: %w", err)
	}
	if resp.Thread.ID == "" {
		return "", errors.New("thread/fork returned no thread id")
	}
	return resp.Thread.ID, nil
}

// Rollback discards the turn turnID and every turn after it. The caller must
// stop turns starting while it runs: the count it sends is only valid for the
// turn list it read. ErrTurnNotInHistory means the thread doesn't hold the
// turn; rather than guess a count, nothing is discarded.
func Rollback(ctx context.Context, conn Requester, threadID, turnID string) error {
	if strings.TrimSpace(turnID) == "" {
		return errors.New("rollback needs a provider turn id")
	}
	turns, err := ReadTurns(ctx, conn, threadID)
	if err != nil {
		return err
	}
	discard := 0
	for i, turn := range turns {
		if turn.ID == turnID {
			discard = len(turns) - i
			break
		}
	}
	if discard == 0 {
		return fmt.Errorf("%w: %q", ErrTurnNotInHistory, turnID)
	}
	err = conn.Request(ctx, "thread/rollback", map[string]any{"threadId": threadID, "numTurns": discard}, nil)
	if err == nil {
		return nil
	}
	if !methodRemoved(err) {
		return fmt.Errorf("thread/rollback: %w", err)
	}
	// Codex 0.162 replaced the count-based operation with an exclusive turn
	// anchor. Only an unknown-method refusal permits this fallback;
	// busy, validation and transport failures must never try another
	// mutation.
	if err := conn.Request(ctx, "thread/revert", map[string]any{"threadId": threadID, "beforeTurnId": turnID}, nil); err != nil {
		return fmt.Errorf("thread/revert: %w", err)
	}
	return nil
}

// methodRemoved reports an app-server that doesn't know thread/rollback.
func methodRemoved(err error) bool {
	var rpcErr RPCError
	if !errors.As(err, &rpcErr) {
		return false
	}
	return rpcErr.RPCCode() == -32601 ||
		(rpcErr.RPCCode() == -32600 && strings.Contains(rpcErr.RPCMessage(), "unknown variant `thread/rollback`"))
}
