package providerkeys

import (
	"context"
	"reflect"
	"testing"
)

func TestAToolKeyNeverReachesASessionsEnvironment(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	if err := Save(ctx, dir, "GMI_API_KEY", "gmi-secret"); err != nil {
		t.Fatal(err)
	}
	if err := Save(ctx, dir, "OPENAI_API_KEY", "oai"); err != nil {
		t.Fatal(err)
	}
	inject, withhold, err := ForSession(dir, "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := inject["GMI_API_KEY"]; ok {
		t.Fatal("the tool key was injected into a session's environment")
	}
	if inject["OPENAI_API_KEY"] != "oai" {
		t.Fatalf("inject = %v, want the harness key", inject)
	}
	if !reflect.DeepEqual(withhold, []string{"GMI_API_KEY"}) {
		t.Fatalf("withhold = %v, want the tool key", withhold)
	}
	// The key is AO's own copy in the daemon's environment, so a session never
	// sees the credential: what it gets is the user's own export, or nothing.
	if err := ExportForProbes("GMI_API_KEY", "gmi-secret"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = RestoreInherited("GMI_API_KEY") })
	withheld := Withheld(dir, "claude-code")
	value, ok := withheld["GMI_API_KEY"]
	if !ok {
		t.Fatalf("withheld = %v, want the tool key listed", withheld)
	}
	if value == "gmi-secret" {
		t.Fatal("the session would hold AO's copy of the tool key")
	}
}

func TestToolKeysAreListed(t *testing.T) {
	if !IsToolKey("GMI_API_KEY") {
		t.Fatal("GMI_API_KEY is not a tool key")
	}
	if IsToolKey("OPENAI_API_KEY") {
		t.Fatal("OPENAI_API_KEY is not a tool key")
	}
	if ToolKeyLabel("GMI_API_KEY") == "" {
		t.Fatal("the tool key has no display name")
	}
	keys := ToolKeys()
	if len(keys) == 0 || keys[0] != "GMI_API_KEY" {
		t.Fatalf("ToolKeys = %v, want the GMI key", keys)
	}
}
