package store

import (
	"database/sql"
	"testing"
)

func TestUnmarshalProjectConfigDegradesGracefully(t *testing.T) {
	if got := unmarshalProjectConfig(sql.NullString{}); !got.IsZero() {
		t.Fatalf("NULL config = %#v, want zero", got)
	}

	if got := unmarshalProjectConfig(sql.NullString{String: `{"defaultBranch":"develop"}`, Valid: true}); got.DefaultBranch != "develop" {
		t.Fatalf("valid config DefaultBranch = %q, want develop", got.DefaultBranch)
	}

	if got := unmarshalProjectConfig(sql.NullString{String: `{not json`, Valid: true}); !got.IsZero() {
		t.Fatalf("corrupt config = %#v, want zero (degraded)", got)
	}
}
