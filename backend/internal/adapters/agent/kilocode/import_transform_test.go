package kilocode

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func kiloSyntheticExport() ([]byte, string) {
	session := "ses_" + strings.Repeat("a", 26)
	user := "msg_" + strings.Repeat("b", 26)
	assistant := "msg_" + strings.Repeat("c", 26)
	part := "prt_" + strings.Repeat("d", 26)
	userPart := "prt_" + strings.Repeat("e", 26)
	const export = `{
		"info":{"id":"SID","slug":"synthetic-chat","projectID":"global","version":"7.8.16","title":"Synthetic native history","time":{"created":0,"updated":2},"directory":"/source","workspaceID":"wrk_source","revert":{"messageID":"ASSISTANT","partID":"PART"}},
		"messages":[
			{"info":{"id":"ASSISTANT","sessionID":"SID","role":"assistant","parentID":"USER","time":{"created":1,"completed":2},"modelID":"synthetic","providerID":"synthetic","mode":"build","agent":"build","path":{"cwd":"/source","root":"/source"},"cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"finish":"stop"},"parts":[{"id":"PART","sessionID":"SID","messageID":"ASSISTANT","type":"text","text":"Synthetic reply"}]},
			{"info":{"id":"USER","sessionID":"SID","role":"user","time":{"created":0},"agent":"build","model":{"providerID":"synthetic","modelID":"synthetic"}},"parts":[{"id":"UPART","sessionID":"SID","messageID":"USER","type":"text","text":"Synthetic prompt"}]}
		]
	}`
	return []byte(strings.NewReplacer("SID", session, "USER", user, "ASSISTANT", assistant, "UPART", userPart, "PART", part).Replace(export)), session
}

func TestKiloExportRemapPreservesGraphAndContent(t *testing.T) {
	data, source := kiloSyntheticExport()
	original := append([]byte(nil), data...)
	gen := kiloImportID{random: bytes.NewReader(make([]byte, 70))}
	checks := 0
	out, id, err := remapKiloExport(data, source, "/work/ao", gen.next, func(string) (bool, error) {
		checks++
		return false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "ses_fffffffffffe00000000000000" || checks != 5 || !bytes.Equal(data, original) {
		t.Fatalf("identity=%q checks=%d source changed=%v", id, checks, !bytes.Equal(data, original))
	}
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	info := result["info"].(map[string]any)
	if info["directory"] != "/work/ao" || info["workspaceID"] != nil || info["id"] != id {
		t.Fatalf("worktree identity = %#v", info)
	}
	messages := result["messages"].([]any)
	assistant := messages[0].(map[string]any)
	user := messages[1].(map[string]any)["info"].(map[string]any)
	message := assistant["info"].(map[string]any)
	part := assistant["parts"].([]any)[0].(map[string]any)
	revert := info["revert"].(map[string]any)
	if message["sessionID"] != id || message["parentID"] != user["id"] || part["sessionID"] != id || part["messageID"] != message["id"] || revert["messageID"] != message["id"] || revert["partID"] != part["id"] || part["text"] != "Synthetic reply" {
		t.Fatalf("native references were not preserved: %s", out)
	}
	if user["id"].(string) >= message["id"].(string) {
		t.Fatal("native ascending message order changed with export array order")
	}
}

func TestKiloExportRemapRefusesIdentityFailures(t *testing.T) {
	for _, name := range []string{"source collision", "store collision", "store error", "entropy failure", "foreign parent", "duplicate message"} {
		t.Run(name, func(t *testing.T) {
			data, source := kiloSyntheticExport()
			gen := kiloImportID{random: bytes.NewReader(make([]byte, 70))}
			next := gen.next
			exists := func(string) (bool, error) { return false, nil }
			switch name {
			case "source collision":
				next = func(string) (string, error) { return source, nil }
			case "store collision":
				exists = func(string) (bool, error) { return true, nil }
			case "store error":
				exists = func(string) (bool, error) { return false, errors.New("synthetic store failure") }
			case "entropy failure":
				gen.random = bytes.NewReader(nil)
			case "foreign parent":
				data = bytes.Replace(data, []byte(`"parentID":"msg_`+strings.Repeat("b", 26)+`"`), []byte(`"parentID":"msg_foreign"`), 1)
			case "duplicate message":
				data = bytes.ReplaceAll(data, []byte("msg_"+strings.Repeat("c", 26)), []byte("msg_"+strings.Repeat("b", 26)))
			}
			if _, _, err := remapKiloExport(data, source, "/work/ao", next, exists); err == nil {
				t.Fatal("unsafe native import accepted")
			}
		})
	}
}

func TestKiloExportRemapCompactionReference(t *testing.T) {
	data, source := kiloSyntheticExport()
	user := "msg_" + strings.Repeat("b", 26)
	data = bytes.Replace(data, []byte(`"type":"text","text":"Synthetic reply"`), []byte(`"type":"compaction","auto":true,"tail_start_id":"`+user+`"`), 1)
	gen := kiloImportID{random: bytes.NewReader(make([]byte, 70))}
	out, _, err := remapKiloExport(data, source, "/work/ao", gen.next, func(string) (bool, error) { return false, nil })
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Messages []struct {
			Info struct {
				ID string `json:"id"`
			} `json:"info"`
			Parts []struct {
				Tail string `json:"tail_start_id"`
			} `json:"parts"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	if result.Messages[0].Parts[0].Tail != result.Messages[1].Info.ID || result.Messages[0].Parts[0].Tail == user {
		t.Fatal("compaction retained an original message reference")
	}
}
