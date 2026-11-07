// m5-codex 是桌面验收专用的离线 stdio Provider；不访问网络、配置、凭据或执行工具。
// 协议形状复用 codexappserver 测试，正式 Electron/Main/Daemon/Driver 保持原样。
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"slices"
)

// frame 仅接收有界 JSON-RPC 请求；未知操作明确拒绝，防止验收脚手架伪造能力成功。
type frame struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
	} `json:"params"`
}

// main 仅允许隔离环境显式启动；版本和认证结果只描述此离线 fixture，不涉及真实账号。
func main() {
	if os.Getenv("AO_M5_OFFLINE_PROVIDER") != "1" {
		fmt.Fprintln(os.Stderr, "M5 offline fixture requires explicit isolation")
		os.Exit(2)
	}
	if slices.Contains(os.Args[1:], "--version") {
		fmt.Println("codex-cli 0.146.0 M5-OFFLINE-FIXTURE")
		return
	}
	if slices.Contains(os.Args[1:], "login") && slices.Contains(os.Args[1:], "status") {
		fmt.Println("Logged in with offline fixture")
		return
	}
	if !slices.Contains(os.Args[1:], "app-server") || slices.Contains(os.Args[1:], "generate-json-schema") {
		fmt.Fprintln(os.Stderr, "unsupported offline fixture command")
		os.Exit(2)
	}
	if err := serve(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// serve 保留一个运行中 turn，使桌面能验证未发送草稿；中断会发出真实协议终态。
func serve() error {
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 4096), 1<<20)
	out := json.NewEncoder(os.Stdout)
	threadID := fmt.Sprintf("m5-offline-%d", os.Getpid())
	turnID := ""
	turns := 0
	for scan.Scan() {
		var f frame
		if err := json.Unmarshal(scan.Bytes(), &f); err != nil {
			return err
		}
		if len(f.ID) == 0 {
			continue
		}
		var result any
		switch f.Method {
		case "initialize":
			result = map[string]any{"userAgent": "ao/m5-offline-fixture"}
		case "model/list":
			result = map[string]any{"data": []any{map[string]any{"id": "offline-fixture", "displayName": "M5 Offline Fixture", "isDefault": true, "supportedReasoningEfforts": []any{}, "defaultReasoningEffort": "high"}}}
		case "config/read":
			result = map[string]any{"config": map[string]any{}}
		case "skills/list", "collaborationMode/list", "thread/list":
			result = map[string]any{"data": []any{}}
		case "thread/start", "thread/resume":
			if f.Params.ThreadID != "" {
				threadID = f.Params.ThreadID
			}
			result = map[string]any{"thread": map[string]any{"id": threadID}, "model": "offline-fixture", "reasoningEffort": "high"}
		case "turn/start":
			turns++
			turnID = fmt.Sprintf("m5-turn-%d", turns)
			result = map[string]any{"turn": map[string]any{"id": turnID, "status": "inProgress", "items": []any{}}}
		case "turn/interrupt":
			result = map[string]any{}
		default:
			if err := out.Encode(map[string]any{"id": f.ID, "error": map[string]any{"code": -32601, "message": "not implemented by M5 offline fixture"}}); err != nil {
				return err
			}
			continue
		}
		if err := out.Encode(map[string]any{"id": f.ID, "result": result}); err != nil {
			return err
		}
		if f.Method == "turn/start" {
			if err := out.Encode(map[string]any{"method": "turn/started", "params": map[string]any{"threadId": threadID, "turn": map[string]any{"id": turnID, "status": "inProgress", "items": []any{}}}}); err != nil {
				return err
			}
			if err := out.Encode(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"threadId": threadID, "turnId": turnID, "itemId": turnID + "-message", "delta": "M5离线协议输出：只验证桌面草稿与会话保留。"}}); err != nil {
				return err
			}
		}
		if f.Method == "turn/interrupt" && turnID != "" {
			if err := out.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": threadID, "turn": map[string]any{"id": turnID, "status": "interrupted", "items": []any{}}}}); err != nil {
				return err
			}
			turnID = ""
		}
	}
	return scan.Err()
}
