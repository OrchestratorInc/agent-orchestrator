package mimocode

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func mimoSyntheticExport() ([]byte, string) {
	session := "ses_" + strings.Repeat("a", 26)
	user := "msg_" + strings.Repeat("b", 26)
	assistant := "msg_" + strings.Repeat("c", 26)
	part := "prt_" + strings.Repeat("d", 26)
	userPart := "prt_" + strings.Repeat("e", 26)
	const export = `{
		"info":{"id":"SID","slug":"synthetic-chat","projectID":"global","version":"synthetic","title":"Synthetic native history","time":{"created":0,"updated":2},"directory":"/source","workspaceID":"wrk_source","revert":{"messageID":"ASSISTANT","partID":"PART"}},
		"messages":[
			{"info":{"id":"ASSISTANT","sessionID":"SID","role":"assistant","parentID":"USER","time":{"created":1,"completed":2},"modelID":"synthetic","providerID":"synthetic","mode":"build","agent":"build","path":{"cwd":"/source","root":"/source"},"cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"finish":"stop"},"parts":[{"id":"PART","sessionID":"SID","messageID":"ASSISTANT","type":"text","text":"Synthetic reply"}]},
			{"info":{"id":"USER","sessionID":"SID","role":"user","time":{"created":0},"agent":"build","model":{"providerID":"synthetic","modelID":"synthetic"}},"parts":[{"id":"UPART","sessionID":"SID","messageID":"USER","type":"text","text":"Synthetic prompt"}]}
		]
	}`
	return []byte(strings.NewReplacer("SID", session, "USER", user, "ASSISTANT", assistant, "UPART", userPart, "PART", part).Replace(export)), session
}

func TestMiMoExportRemapPreservesGraphAndContent(t *testing.T) {
	data, source := mimoSyntheticExport()
	cwd := t.TempDir()
	original := append([]byte(nil), data...)
	gen := mimoImportID{random: bytes.NewReader(make([]byte, 70))}
	checks := 0
	out, id, err := remapMiMoExport(data, source, cwd, gen.next, func(string) (bool, error) {
		checks++
		return false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "ses_fffffffffffffffe0000000000" || checks != 5 || !bytes.Equal(data, original) {
		t.Fatalf("identity=%q checks=%d source changed=%v", id, checks, !bytes.Equal(data, original))
	}
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	info := result["info"].(map[string]any)
	if info["directory"] != cwd || info["workspaceID"] != nil || info["id"] != id {
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

func TestMiMoExportRemapRefusesIdentityFailures(t *testing.T) {
	for _, name := range []string{"source collision", "store collision", "store error", "entropy failure", "foreign parent", "duplicate message"} {
		t.Run(name, func(t *testing.T) {
			data, source := mimoSyntheticExport()
			gen := mimoImportID{random: bytes.NewReader(make([]byte, 70))}
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
			if _, _, err := remapMiMoExport(data, source, t.TempDir(), next, exists); err == nil {
				t.Fatal("unsafe native import accepted")
			}
		})
	}
}

func TestMiMoExportRemapCompactionReference(t *testing.T) {
	data, source := mimoSyntheticExport()
	user := "msg_" + strings.Repeat("b", 26)
	assistant := "msg_" + strings.Repeat("c", 26)
	data = bytes.Replace(data, []byte(`"type":"text","text":"Synthetic reply"`), []byte(`"type":"compaction","auto":true,"tail_start_id":"`+user+`","projection":{"version":1,"summary_message_id":"`+assistant+`","summary":"Synthetic summary","trigger":"manual","tail_start_id":"`+user+`","tail_end_id":"`+assistant+`"}`), 1)
	gen := mimoImportID{random: bytes.NewReader(make([]byte, 70))}
	out, _, err := remapMiMoExport(data, source, t.TempDir(), gen.next, func(string) (bool, error) { return false, nil })
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
				Projection struct {
					Summary string `json:"summary_message_id"`
					Start   string `json:"tail_start_id"`
					End     string `json:"tail_end_id"`
				} `json:"projection"`
			} `json:"parts"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	part := result.Messages[0].Parts[0]
	if part.Tail != result.Messages[1].Info.ID || part.Tail == user || part.Projection.Summary != result.Messages[0].Info.ID || part.Projection.Start != result.Messages[1].Info.ID || part.Projection.End != result.Messages[0].Info.ID {
		t.Fatal("compaction retained an original message reference")
	}
}

func TestMiMoExportRemapBoundariesAndQueue(t *testing.T) {
	data, source := mimoSyntheticExport()
	user := "msg_" + strings.Repeat("b", 26)
	var export map[string]any
	if err := json.Unmarshal(data, &export); err != nil {
		t.Fatal(err)
	}
	export["queue"] = map[string]any{"sessionID": source, "receipts": []any{map[string]any{"input_message_id": "msg_pending_not_in_history"}}}
	info := export["info"].(map[string]any)
	info["contextWatermark"] = user
	messages := export["messages"].([]any)
	part := messages[0].(map[string]any)["parts"].([]any)[0].(map[string]any)
	part["type"], part["checkpointDir"], part["checkpointNumber"] = "checkpoint", "synthetic", 1
	part["coveredUpTo"], part["digestUpTo"], part["deferredInputs"] = user, user, []any{user}
	messages[1].(map[string]any)["info"].(map[string]any)["provenance"] = map[string]any{"source": "agent", "senderSessionID": source}
	data, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	gen := mimoImportID{random: bytes.NewReader(make([]byte, 70))}
	out, nativeID, err := remapMiMoExport(data, source, t.TempDir(), gen.next, func(string) (bool, error) { return false, nil })
	if err != nil {
		t.Fatal(err)
	}
	// Decode into a fresh map: encoding/json retains keys absent from an
	// existing map, which would make the removed queue appear to survive.
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	if _, exists := result["queue"]; exists {
		t.Fatal("native queue ownership was transferred")
	}
	messages = result["messages"].([]any)
	userInfo := messages[1].(map[string]any)["info"].(map[string]any)
	part = messages[0].(map[string]any)["parts"].([]any)[0].(map[string]any)
	if part["coveredUpTo"] != userInfo["id"] || part["digestUpTo"] != userInfo["id"] || part["deferredInputs"].([]any)[0] != userInfo["id"] || result["info"].(map[string]any)["contextWatermark"] != userInfo["id"] || userInfo["provenance"].(map[string]any)["senderSessionID"] != nativeID {
		t.Fatal("native boundary references were not rewritten")
	}
}

func TestMiMoExportRemapPreservesArbitraryPayloadIdentities(t *testing.T) {
	data, source := mimoSyntheticExport()
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	message := value["messages"].([]any)[0].(map[string]any)
	part := message["parts"].([]any)[0].(map[string]any)
	oldPart := part["id"]
	payload := map[string]any{"id": "msg_external", "sessionID": source, "nested": map[string]any{"messageID": message["info"].(map[string]any)["parentID"], "partID": oldPart}}
	part["type"], part["tool"], part["callID"] = "tool", "shell", "call_synthetic"
	state := map[string]any{"status": "completed", "input": payload, "output": "Synthetic output", "title": "Synthetic tool", "metadata": payload, "providerMetadata": payload, "providerOutput": payload, "time": map[string]any{"start": float64(0), "end": float64(1)}}
	part["state"], part["metadata"] = state, payload
	message["info"].(map[string]any)["structured"] = payload
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	gen := mimoImportID{random: bytes.NewReader(make([]byte, 70))}
	out, nativeID, err := remapMiMoExport(data, source, t.TempDir(), gen.next, func(string) (bool, error) { return false, nil })
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	message = got["messages"].([]any)[0].(map[string]any)
	part = message["parts"].([]any)[0].(map[string]any)
	if !reflect.DeepEqual(part["state"], state) || !reflect.DeepEqual(part["metadata"], payload) || !reflect.DeepEqual(message["info"].(map[string]any)["structured"], payload) {
		t.Fatal("arbitrary payload was treated as a native graph")
	}
	if part["id"] == oldPart || part["sessionID"] != nativeID || part["messageID"] != message["info"].(map[string]any)["id"] {
		t.Fatal("declared part identity was not rewritten")
	}
}

