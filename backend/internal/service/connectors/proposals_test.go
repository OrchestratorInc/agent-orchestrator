package connectors

import (
	"context"
	"slices"
	"testing"
	"time"
)

func proposalService(t *testing.T) (*Service, *time.Time) {
	t.Helper()
	svc := New(t.TempDir(), "http://127.0.0.1:1")
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	svc.SetSessionLookup(func(_ context.Context, id string) (string, bool) { return "demo", id != "closed" })
	return svc, &now
}

func TestAProposalAddsNothingUntilTheUserApproves(t *testing.T) {
	svc, _ := proposalService(t)
	ctx := context.Background()
	proposal, err := svc.Propose(ctx, ProposalInput{
		SessionID: "ao-12", Name: "Sentry", Transport: TransportHTTP, URL: "https://mcp.sentry.dev/mcp", Reason: "To read the crash report.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if proposal.State != ProposalPending || proposal.Key != "sentry" || proposal.Auth != AuthOAuth || proposal.ProjectID != "demo" {
		t.Fatalf("proposal = %+v", proposal)
	}
	if all, _ := svc.List(ctx); len(all) != 0 {
		t.Fatalf("a proposal added a connector: %+v", all)
	}
	again, err := svc.Propose(ctx, ProposalInput{SessionID: "ao-12", Name: "Sentry", Transport: TransportHTTP, URL: "https://mcp.sentry.dev/mcp"})
	if err != nil || again.ID != proposal.ID {
		t.Fatalf("the same request made a second proposal: %+v, %v", again, err)
	}
	fixed, err := svc.Propose(ctx, ProposalInput{SessionID: "ao-12", Name: "Sentry", Transport: TransportHTTP, URL: "https://mcp.sentry.dev/v2/mcp"})
	if err != nil || fixed.ID == proposal.ID || fixed.URL != "https://mcp.sentry.dev/v2/mcp" {
		t.Fatalf("a corrected request = %+v, %v", fixed, err)
	}
	if got := svc.SessionProposals(ctx, "ao-12"); len(got) != 1 || got[0].ID != fixed.ID {
		t.Fatalf("session proposals after a correction = %+v", got)
	}
	if _, _, err := svc.ApproveProposal(ctx, proposal.ID, nil, nil); errCode(err) != "CONNECTOR_PROPOSAL_ANSWERED" {
		t.Fatalf("approval of the replaced request = %v", err)
	}
	if got := svc.SessionProposals(ctx, "ao-12"); len(got) != 1 || len(svc.SessionProposals(ctx, "ao-13")) != 0 {
		t.Fatalf("session proposals = %+v", got)
	}
	all := ""
	approved, created, err := svc.ApproveProposal(ctx, fixed.ID, &all, nil)
	if err != nil || approved.State != ProposalApproved || approved.ConnectorKey != "sentry" || created.ProjectID != "" || created.Auth != AuthOAuth {
		t.Fatalf("approve = %+v, %+v, %v", approved, created, err)
	}
	if _, _, err := svc.ApproveProposal(ctx, fixed.ID, nil, nil); err == nil {
		t.Fatal("an answered proposal was approved again")
	}
	if _, err := svc.Propose(ctx, ProposalInput{SessionID: "ao-12", Name: "Sentry", Transport: TransportHTTP, URL: "https://mcp.sentry.dev/mcp"}); err == nil {
		t.Fatal("a proposal for a connector that exists was accepted")
	}
}

func TestTheUserTypesTheValuesOfAProposal(t *testing.T) {
	svc, _ := proposalService(t)
	ctx := context.Background()
	proposal, err := svc.Propose(ctx, ProposalInput{
		SessionID: "ao-12", Key: "paper", Transport: TransportStdio, Command: "npx", Args: []string{"-y", "paper-mcp"}, EnvKeys: []string{"PAPER_KEY", "PAPER_HOME"},
	})
	if err != nil || !slices.Equal(proposal.EnvKeys, []string{"PAPER_HOME", "PAPER_KEY"}) {
		t.Fatalf("proposal = %+v, %v", proposal, err)
	}
	if _, _, err := svc.ApproveProposal(ctx, proposal.ID, nil, map[string]string{"PAPER_KEY": "k"}); err == nil {
		t.Fatal("approved without a value the proposal needs")
	}
	_, created, err := svc.ApproveProposal(ctx, proposal.ID, nil, map[string]string{"PAPER_KEY": "typed-key", "PAPER_HOME": "/p"})
	if err != nil || created.ProjectID != "demo" || created.Transport != TransportStdio {
		t.Fatalf("created = %+v, %v", created, err)
	}
	records, err := readFile(svc.path)
	if err != nil || len(records) != 1 || records[0].Env["PAPER_KEY"] != "typed-key" || records[0].Env["PAPER_HOME"] != "/p" {
		t.Fatalf("records = %+v, %v", records, err)
	}

	header, err := svc.Propose(ctx, ProposalInput{SessionID: "ao-12", Key: "docs", Transport: TransportHTTP, URL: "https://docs.example/mcp", HeaderKeys: []string{"Authorization"}})
	if err != nil || header.Auth != AuthHeader {
		t.Fatalf("header proposal = %+v, %v", header, err)
	}
}

func TestAProposalCanBeDeclinedAndExpires(t *testing.T) {
	svc, now := proposalService(t)
	ctx := context.Background()
	for _, in := range []ProposalInput{
		{SessionID: "closed", Key: "a", Transport: TransportHTTP, URL: "https://a.example/mcp"},
		{SessionID: "", Key: "a", Transport: TransportHTTP, URL: "https://a.example/mcp"},
		{SessionID: "ao-12", Key: "a", Transport: TransportHTTP, URL: "ftp://a.example/mcp"},
		{SessionID: "ao-12", Key: "ao", Transport: TransportHTTP, URL: "https://a.example/mcp"},
		{SessionID: "ao-12", Key: "a", Transport: TransportStdio, Command: "npx", EnvKeys: []string{"not a name"}},
	} {
		if _, err := svc.Propose(ctx, in); err == nil {
			t.Errorf("accepted %+v", in)
		}
	}
	first, err := svc.Propose(ctx, ProposalInput{SessionID: "ao-12", Key: "a", Transport: TransportHTTP, URL: "https://a.example/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if declined, err := svc.DeclineProposal(ctx, first.ID); err != nil || declined.State != ProposalDeclined {
		t.Fatalf("decline = %+v, %v", declined, err)
	}
	if _, _, err := svc.ApproveProposal(ctx, first.ID, nil, nil); err == nil {
		t.Fatal("a declined proposal was approved")
	}
	second, err := svc.Propose(ctx, ProposalInput{SessionID: "ao-12", Key: "a", Transport: TransportHTTP, URL: "https://a.example/mcp"})
	if err != nil || second.ID == first.ID {
		t.Fatalf("second = %+v, %v", second, err)
	}
	*now = now.Add(proposalLife + time.Minute)
	if got, err := svc.GetProposal(ctx, second.ID); err != nil || got.State != ProposalExpired {
		t.Fatalf("after its life = %+v, %v", got, err)
	}
	if _, _, err := svc.ApproveProposal(ctx, second.ID, nil, nil); err == nil {
		t.Fatal("an expired proposal was approved")
	}
	if got := svc.SessionProposals(ctx, "ao-12"); len(got) != 0 {
		t.Fatalf("pending after expiry = %+v", got)
	}
	if _, err := svc.GetProposal(ctx, "missing"); err == nil {
		t.Fatal("an unknown proposal was found")
	}
}
