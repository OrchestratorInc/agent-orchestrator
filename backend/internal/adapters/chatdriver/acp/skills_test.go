package acp

import (
	"context"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

func TestAvailableCommandsListEachNameOnce(t *testing.T) {
	conv := &conversation{}
	conv.replaceAvailableCommands([]acpsdk.AvailableCommand{
		{Name: "goal", Description: "built in"}, {Name: "compact"}, {Name: "goal", Description: "a skill"},
	})
	skills, err := conv.ListSkills(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 || skills[0].Description != "built in" || skills[1].Name != "compact" || !conv.compactAdvertised {
		t.Fatalf("skills = %+v", skills)
	}
}
