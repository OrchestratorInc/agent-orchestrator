package responses

import (
	"context"
	"testing"
)

func TestStreamDelimiterPreservedAfterData(t *testing.T) {
	for _, named := range []bool{false, true} {
		name := "data_only"
		if named {
			name = "named_event"
		}
		t.Run(name, func(t *testing.T) {
			var state any
			convert := func(line string) [][]byte {
				return ConvertCodexResponseToOpenAIResponses(t.Context(), "test-model", nil, nil, []byte(line), &state)
			}
			if named {
				convert("event: response.output_text.delta")
			}
			payload := `data: {"type":"response.output_text.delta","delta":"first"}`
			chunks := convert(payload)
			if len(chunks) != 1 || string(chunks[0]) != payload {
				t.Fatal("data event changed during translation")
			}
			chunks = convert("")
			if len(chunks) != 1 || string(chunks[0]) != "\n\n" {
				t.Fatalf("event delimiter = %q, want one complete delimiter", chunks)
			}
			for _, chunk := range convert("") {
				if len(chunk) != 0 {
					t.Fatal("empty event was committed after the delimiter")
				}
			}
		})
	}
}

func TestStreamLeadingEmptyLinesDoNotCommit(t *testing.T) {
	var state any
	for _, param := range []*any{nil, &state} {
		for range 2 {
			chunks := ConvertCodexResponseToOpenAIResponses(context.Background(), "test-model", nil, nil, nil, param)
			for _, chunk := range chunks {
				if len(chunk) != 0 {
					t.Fatal("leading empty event committed a stream")
				}
			}
		}
	}
}

func TestStreamDelimiterStateIsPerResponse(t *testing.T) {
	var first, second any
	convert := func(state *any, raw string) [][]byte {
		return ConvertCodexResponseToOpenAIResponses(t.Context(), "test-model", nil, nil, []byte(raw), state)
	}
	convert(&first, `data: {"type":"response.output_text.delta","delta":"first"}`)
	for _, chunk := range convert(&second, "") {
		if len(chunk) != 0 {
			t.Fatal("another response supplied this response's delimiter state")
		}
	}
	chunks := convert(&first, "")
	if len(chunks) != 1 || string(chunks[0]) != "\n\n" {
		t.Fatal("another response consumed this response's delimiter state")
	}
}
