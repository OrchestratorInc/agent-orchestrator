package litmus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// agent-browser 0.38.1's wheel handler defaults x/y to zero. Its input_mouse
// handler already uses the daemon's current pointer, including clicks/hover.
func (e *Engine) browserCommand(ctx context.Context, args []string) (string, error) {
	for _, flag := range []string{"--help", "-h", "--version", "-V"} {
		if slices.Contains(args, flag) {
			return e.invoke(ctx, args...)
		}
	}
	words := slices.DeleteFunc(slices.Clone(args), func(s string) bool { return s == "--json" })
	if len(words) >= 2 && words[0] == "mouse" && words[1] == "wheel" {
		return e.wheel(ctx, words[2:], slices.Contains(args, "--json"))
	}
	if len(words) > 1 && words[0] == "batch" && slices.ContainsFunc(words[1:], func(s string) bool { return wheelCommand.MatchString(s) }) {
		jsonOutput := slices.Contains(args, "--json")
		var results []json.RawMessage
		var output strings.Builder
		var failed error
		for _, command := range words[1:] {
			if command == "--bail" {
				continue
			}
			var out string
			var err error
			if m := wheelCommand.FindStringSubmatch(command); m != nil {
				out, err = e.wheel(ctx, []string{unquoteWheel(m[1]), unquoteWheel(m[2])}, jsonOutput)
				if jsonOutput {
					var reply struct {
						Success bool
						Data    json.RawMessage
						Error   string
					}
					if decodeErr := json.Unmarshal([]byte(out), &reply); decodeErr != nil {
						reply.Error = errString(err)
					}
					data, _ := json.Marshal(map[string]any{"command": strings.Fields(command), "success": reply.Success, "result": reply.Data, "error": reply.Error})
					results = append(results, data)
				}
			} else {
				// Keep upstream's quoting, argument parsing and error codes.
				forward := []string{"batch", command}
				if jsonOutput {
					forward = append([]string{"--json"}, forward...)
				}
				out, err = e.invoke(ctx, forward...)
				if jsonOutput {
					var entries []json.RawMessage
					if decodeErr := json.Unmarshal([]byte(out), &entries); decodeErr != nil {
						if err == nil {
							err = fmt.Errorf("wheel batch response: %w", decodeErr)
						}
						data, _ := json.Marshal(map[string]any{"command": []string{command}, "success": false, "error": err.Error()})
						entries = []json.RawMessage{data}
					}
					results = append(results, entries...)
				}
			}
			output.WriteString(out)
			output.WriteByte('\n')
			if err != nil {
				failed = err
				if slices.Contains(words, "--bail") || ctx.Err() != nil {
					break
				}
			}
		}
		if slices.Contains(args, "--json") {
			data, err := json.Marshal(results)
			if err != nil {
				return "", err
			}
			return string(data), failed
		}
		return output.String(), failed
	}
	return e.invoke(ctx, args...)
}

// Only recognize mouse wheel; every other batch string stays with upstream.
var wheelCommand = regexp.MustCompile(`^\s*(?:mouse|"mouse"|'mouse')\s+(?:wheel|"wheel"|'wheel')(?:\s+("[^"\s]*"|'[^'\s]*'|[^\s'"\\]+))?(?:\s+("[^"\s]*"|'[^'\s]*'|[^\s'"\\]+))?\s*$`)

func unquoteWheel(s string) string { return strings.Trim(s, `"'`) }

func errString(err error) string {
	if err != nil {
		return err.Error()
	}
	return "invalid wheel response"
}

func (e *Engine) wheel(ctx context.Context, args []string, jsonOutput bool) (string, error) {
	delta := [2]int32{100, 0} // same defaults and dy, dx order as parse_mouse
	for i := 0; i < min(len(args), 2); i++ {
		if n, err := strconv.ParseInt(args[i], 10, 32); err == nil {
			delta[i] = int32(n)
		}
	}
	network, address := "unix", filepath.Join(e.SocketDir, "namespaces", e.Namespace, "run", e.Session+".sock")
	if runtime.GOOS == "windows" {
		data, err := os.ReadFile(strings.TrimSuffix(address, ".sock") + ".port")
		if err != nil {
			return "", err
		}
		network, address = "tcp", "127.0.0.1:"+strings.TrimSpace(string(data))
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()
	defer context.AfterFunc(ctx, func() { _ = conn.Close() })()
	if err := json.NewEncoder(conn).Encode(map[string]any{"id": "litmus-wheel", "action": "input_mouse", "type": "mouseWheel", "deltaX": delta[1], "deltaY": delta[0]}); err != nil {
		return "", err
	}
	var reply struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   string          `json:"error,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(conn, outputCap)).Decode(&reply); err != nil {
		return "", err
	}
	data, err := json.Marshal(reply)
	if err != nil {
		return "", err
	}
	if !reply.Success {
		return string(data), errors.New(reply.Error)
	}
	if jsonOutput {
		return string(data), nil
	}
	return "Scrolled", nil
}
