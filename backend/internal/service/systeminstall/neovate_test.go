package systeminstall

import (
	"reflect"
	"testing"
)

func TestNeovateUsesPinnedOfficialPackage(t *testing.T) {
	plan := newTestService("darwin", "npm").planAgent(TargetNeovate)
	want := []string{"npm", "install", "-g", "@neovate/code@0.28.5"}
	if plan.Unsupported || plan.Method != "npm" || !reflect.DeepEqual(plan.Command, want) {
		t.Fatalf("install plan = %#v", plan)
	}
}
