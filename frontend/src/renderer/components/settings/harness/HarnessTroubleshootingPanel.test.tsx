import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { apiClient } from "../../../lib/api-client";
import { aoBridge } from "../../../lib/bridge";
import { agentReadiness } from "../../../test/agent-readiness-fixtures";
import * as troubleshooting from "./harness-troubleshooting";
import { HarnessTroubleshootingPanel, useHarnessTroubleshooting } from "./HarnessTroubleshootingPanel";

vi.mock("../../TerminalPane", () => ({ TerminalPane: () => <div data-testid="helper-terminal" /> }));
vi.mock("../../../hooks/useWorkspaceQuery", () => ({
	workspaceQueryKey: ["workspaces"],
	toWorkspaceSession: (session: { id: string; displayName: string }) => ({ id: session.id, title: session.displayName }),
}));

beforeEach(() => { useHarnessTroubleshooting.setState({ entries: {} }); });
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

function setup(agents = [agentReadiness("claude-code"), agentReadiness("codex")]) {
	return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><HarnessTroubleshootingPanel agentId="codex" agents={agents} installationBusy={false} /></QueryClientProvider>);
}
async function collect() {
	fireEvent.click(screen.getByRole("button", { name: "Collect fresh diagnostics" }));
	await screen.findByText("Initial checks collected. Review the diagnostics before starting a helper.");
}
function mockChecks() {
	vi.spyOn(aoBridge.app, "getVersion").mockResolvedValue("0.13.0");
	const snapshot = troubleshooting.troubleshootingSnapshot(agentReadiness("codex"), "0.13.0", "win32");
	snapshot.installationVerification = "succeeded";
	return vi.spyOn(troubleshooting, "collectTroubleshootingSnapshot").mockResolvedValue(snapshot);
}

it("launches a standalone helper with the reviewed evidence and embeds its terminal", async () => {
	mockChecks();
	const post = vi.spyOn(apiClient, "POST").mockResolvedValue({ data: { session: { id: "helper-1", displayName: "Troubleshoot Codex" } } } as never);
	setup();
	expect(post).not.toHaveBeenCalled();
	await collect();
	fireEvent.click(screen.getByRole("button", { name: "Troubleshoot with agent" }));
	await screen.findByTestId("helper-terminal");
	expect(post).toHaveBeenCalledWith("/api/v1/sessions", { body: expect.objectContaining({ kind: "worker", harness: "claude-code", mode: "tui", approvalMode: "accept-edits", clientRequestId: expect.any(String), prompt: expect.stringContaining('"host": "local"') }) });
	expect(post.mock.calls[0][1]?.body).not.toHaveProperty("projectId");
	fireEvent.click(screen.getByRole("button", { name: "Stop helper" }));
	await screen.findByRole("button", { name: "Helper stopped" });
	expect(post).toHaveBeenLastCalledWith("/api/v1/sessions/{sessionId}/kill", { params: { path: { sessionId: "helper-1" } } });
});

it("provides the prompt fallback and an editable local issue draft without a helper", async () => {
	mockChecks();
	const post = vi.spyOn(apiClient, "POST");
	const clipboard = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
	setup([]);
	await collect();
	expect(screen.getByRole("button", { name: "Troubleshoot with agent" })).toBeDisabled();
	fireEvent.click(screen.getByRole("button", { name: "Copy troubleshooting prompt" }));
	await waitFor(() => expect(clipboard).toHaveBeenCalledWith(expect.stringContaining("User observation")));
	fireEvent.click(screen.getByRole("button", { name: "Prepare issue draft" }));
	const draft = screen.getByRole("textbox", { name: "Review and edit issue draft" });
	fireEvent.change(draft, { target: { value: "My reviewed report" } });
	fireEvent.click(screen.getByRole("button", { name: "Copy issue draft" }));
	await waitFor(() => expect(clipboard).toHaveBeenLastCalledWith("My reviewed report"));
	expect(post).not.toHaveBeenCalled();
});

it("does not retain a success label after a failed recheck, even when another action succeeds", async () => {
	const checks = mockChecks();
	vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
	setup();
	await collect();
	fireEvent.click(screen.getByRole("button", { name: "Verify again" }));
	await screen.findByText(/AO's latest checks confirm/);
	checks.mockRejectedValueOnce(new Error("Probe unavailable"));
	fireEvent.click(screen.getByRole("button", { name: "Verify again" }));
	await screen.findByRole("alert");
	fireEvent.click(screen.getByRole("button", { name: "Copy troubleshooting prompt" }));
	await screen.findByText("Copied to clipboard");
	expect(screen.queryByText(/AO's latest checks confirm/)).not.toBeInTheDocument();
	expect(screen.getByText(/Verification is unresolved/)).toBeInTheDocument();
});

it("keeps the helper session when the panel is reopened and preserves launch retry identity", async () => {
	mockChecks();
	const post = vi.spyOn(apiClient, "POST").mockRejectedValueOnce(new Error("Connection lost")).mockResolvedValueOnce({ data: { session: { id: "helper-1", displayName: "Troubleshoot Codex" } } } as never);
	const view = setup();
	await collect();
	fireEvent.click(screen.getByRole("button", { name: "Troubleshoot with agent" }));
	await screen.findByRole("alert");
	fireEvent.click(screen.getByRole("button", { name: "Troubleshoot with agent" }));
	await screen.findByTestId("helper-terminal");
	expect(post.mock.calls[0][1]?.body?.clientRequestId).toBe(post.mock.calls[1][1]?.body?.clientRequestId);
	view.unmount();
	setup();
	expect(screen.getByTestId("helper-terminal")).toBeInTheDocument();
	expect(post).toHaveBeenCalledTimes(2);
});
