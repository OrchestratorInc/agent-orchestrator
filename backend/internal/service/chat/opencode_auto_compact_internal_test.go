package chat

import "testing"

func TestOpenCodeConfigWithoutAutoCompactKeepsTheProjectsConfig(t *testing.T) {
	got, err := openCodeConfigWithoutAutoCompact(`{"model":"a/b","compaction":{"prune":true,"auto":true}}`)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"compaction":{"auto":false,"prune":true},"model":"a/b"}`; got != want {
		t.Fatalf("config = %s, want %s", got, want)
	}
	if got, err := openCodeConfigWithoutAutoCompact(""); err != nil || got != `{"compaction":{"auto":false}}` {
		t.Fatalf("empty config = %s, %v", got, err)
	}
	if _, err := openCodeConfigWithoutAutoCompact("[1]"); err == nil {
		t.Fatal("a non-object config was accepted")
	}
}

func TestOpenCodeConfigWithAutoCompactRestoredUndoesOnlyTheOff(t *testing.T) {
	for in, want := range map[string]string{
		`{"compaction":{"auto":false}}`:              `{}`,
		`{"compaction":{"auto":false,"prune":true}}`: `{"compaction":{"prune":true}}`,
		`{"compaction":{"auto":true}}`:               `{"compaction":{"auto":true}}`,
		`not json`:                                   `not json`,
	} {
		if got := openCodeConfigWithAutoCompactRestored(in); got != want {
			t.Errorf("restore(%s) = %s, want %s", in, got, want)
		}
	}
}
