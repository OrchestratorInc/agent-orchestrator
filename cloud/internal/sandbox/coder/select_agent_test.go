package coder

import "testing"

func agentsView(names ...string) workspace {
	var agents []workspaceAgent
	for _, name := range names {
		agents = append(agents, workspaceAgent{ID: name + "-id", Name: name})
	}
	return workspace{LatestBuild: workspaceBuild{Resources: []workspaceResource{{Agents: agents}}}}
}

// The configured agent name belongs to the deployment's default template. A
// template whose only agent has another name still boots instead of waiting
// for an agent that will never appear; with several agents the configured name
// must match.
func TestSelectAgentUsesTheOnlyAgentUnderAnotherName(t *testing.T) {
	t.Parallel()
	client := &Client{agentName: "main"}
	if agent, ok := client.selectAgent(agentsView("main", "sidecar")); !ok || agent.Name != "main" {
		t.Fatalf("named agent = %+v, %v", agent, ok)
	}
	if agent, ok := client.selectAgent(agentsView("platform")); !ok || agent.Name != "platform" {
		t.Fatalf("single agent = %+v, %v; want platform", agent, ok)
	}
	if _, ok := client.selectAgent(agentsView("platform", "sidecar")); ok {
		t.Fatal("picked an agent among several without the configured name")
	}
	if _, ok := client.selectAgent(agentsView()); ok {
		t.Fatal("picked an agent from a build without agents")
	}
	if names := agentNames(agentsView("platform", "sidecar")); len(names) != 2 || names[0] != "platform" {
		t.Fatalf("agent names = %v", names)
	}
}
