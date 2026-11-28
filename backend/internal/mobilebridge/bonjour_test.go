package mobilebridge

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func scutilRunner(out string, err error) func(ctx context.Context) ([]byte, error) {
	return func(context.Context) ([]byte, error) { return []byte(out), err }
}

func TestQueryBonjourNameAddsLocalSuffix(t *testing.T) {
	got := queryBonjourName(context.Background(), scutilRunner("Prasads-MacBook-Pro\n", nil))
	if got != "Prasads-MacBook-Pro.local" {
		t.Errorf("name = %q, want Prasads-MacBook-Pro.local", got)
	}
}

func TestQueryBonjourNameRejectsFailuresAndNonLabels(t *testing.T) {
	cases := map[string]struct {
		out string
		err error
	}{
		"command failed": {"", errors.New("exit 1")},
		"empty":          {"\n", nil},
		"not a label":    {"my mac.local", nil},
		"scutil error":   {"LocalHostName: not set", nil},
	}
	for name, c := range cases {
		if got := queryBonjourName(context.Background(), scutilRunner(c.out, c.err)); got != "" {
			t.Errorf("%s: name = %q, want empty", name, got)
		}
	}
}

func TestWithBonjourNameAppendsAfterAddresses(t *testing.T) {
	got := withBonjourName([]string{"192.168.1.4", "10.0.0.2"}, "Studio.local")
	want := []string{"192.168.1.4", "10.0.0.2", "Studio.local"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hosts = %v, want %v", got, want)
	}
}

// A machine with no LAN address is not on a network the name could reach it
// over, and a name alone would make the phone race a candidate that cannot work.
func TestWithBonjourNameNeedsALANAddress(t *testing.T) {
	if got := withBonjourName(nil, "Studio.local"); len(got) != 0 {
		t.Errorf("hosts = %v, want none", got)
	}
	got := withBonjourName([]string{"192.168.1.4"}, "")
	if !reflect.DeepEqual(got, []string{"192.168.1.4"}) {
		t.Errorf("hosts = %v, want the address alone", got)
	}
}
