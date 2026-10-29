import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { components } from "../../../../api/schema";
import { apiClient } from "../../../lib/api-client";
import { aoBridge } from "../../../lib/bridge";
import { agentReadiness } from "../../../test/agent-readiness-fixtures";
import * as troubleshooting from "./harness-troubleshooting";
import { HarnessTroubleshootingPanel, useHarnessTroubleshooting } from "./HarnessTroubleshootingPanel";

const terminalCallbacks = vi.hoisted(() => [] as Array<(state: "exited") => void>);
vi.mock("../../TerminalPane", () => ({ TerminalPane: ({ session, onTerminalStateChange }: { session: { id: string }; onTerminalStateChange: (state: "exited") => void }) => {
	terminalCallbacks.push(onTerminalStateChange);
	return <div data-testid="helper-terminal" data-session-id={session.id}><button onClick={() => onTerminalStateChange("exited")}>End helper terminal</button></div>;
} }));
vi.mock("../../../hooks/useWorkspaceQuery", () => ({
	workspaceQueryKey: ["workspaces"],
	toWorkspaceSession: (session: { id: string; displayName: string }) => ({ id: session.id, title: session.displayName }),
}));

beforeEach(() => { useHarnessTroubleshooting.setState({ entries: {} }); terminalCallbacks.length = 0; });
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

function mockSessionPost() {
	// openapi-fetch's generic method has no single argument tuple for mock.calls.
	// Capture the request through a typed mock rather than inspecting the generic spy.
	const post = vi.fn<(path: string, options?: { body?: components["schemas"]["SpawnSessionRequest"] }) => Promise<unknown>>()
		.mockResolvedValue({ data: { session: { id: "helper-1", displayName: "Troubleshoot Codex" } } });
	vi.spyOn(apiClient, "POST").mockImplementation(post as never);
	return post;
}

it("launches a standalone helper with the reviewed evidence and embeds its terminal", async () => {
	mockChecks();
	const post = mockSessionPost();
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
	const post = mockSessionPost().mockRejectedValueOnce(new Error("Connection lost"));
	const view = setup();
	await collect();
	fireEvent.click(screen.getByRole("button", { name: "Troubleshoot with agent" }));
	await screen.findByRole("alert");
	fireEvent.click(screen.getByRole("button", { name: "Retry helper launch" }));
	await screen.findByTestId("helper-terminal");
	expect(post.mock.calls[0]).toEqual(post.mock.calls[1]);
	view.unmount();
	setup();
	expect(screen.getByTestId("helper-terminal")).toBeInTheDocument();
	expect(post).toHaveBeenCalledTimes(2);
});

it("retries the exact pending payload after rechecks and reopening, even if no helper is currently ready", async () => {
	const checks = mockChecks();
	const post = mockSessionPost().mockRejectedValueOnce(new Error("Response lost"));
	const view = setup();
	await collect();
	fireEvent.click(screen.getByRole("button", { name: "Troubleshoot with agent" }));
	await screen.findByRole("alert");
	expect(screen.getByRole("combobox", { name: "What are you experiencing?" })).toBeDisabled();
	expect(screen.queryByRole("combobox", { name: "Helper agent" })).not.toBeInTheDocument();
	const next = troubleshooting.troubleshootingSnapshot(agentReadiness("codex", "Codex", { authentication: "unauthorized" }), "0.14.0", "linux");
	checks.mockResolvedValueOnce(next);
	fireEvent.click(screen.getByRole("button", { name: "Verify again" }));
	await screen.findByText(/Verification is unresolved/);
	view.unmount();
	setup([]);
	fireEvent.click(screen.getByRole("button", { name: "Retry helper launch" }));
	await screen.findByTestId("helper-terminal");
	expect(post.mock.calls[1]).toEqual(post.mock.calls[0]);
	expect(post).toHaveBeenCalledTimes(2);
});

it.each(["stop", "exit"])("allows a replacement helper with a new request after %s", async (ending) => {
	mockChecks();
	const post = mockSessionPost();
	setup();
	await collect();
	fireEvent.click(screen.getByRole("button", { name: "Troubleshoot with agent" }));
	await screen.findByTestId("helper-terminal");
	const previousTerminal = terminalCallbacks.at(-1);
	fireEvent.click(screen.getByRole("button", { name: ending === "stop" ? "Stop helper" : "End helper terminal" }));
	await screen.findByRole("button", { name: "Helper stopped" });
	fireEvent.change(screen.getByRole("combobox", { name: "What are you experiencing?" }), { target: { value: "installation" } });
	post.mockResolvedValueOnce({ data: { session: { id: "helper-2", displayName: "Replacement helper" } } });
	fireEvent.click(screen.getByRole("button", { name: "Troubleshoot with agent" }));
	await waitFor(() => expect(screen.getByTestId("helper-terminal")).toHaveAttribute("data-session-id", "helper-2"));
	const launches = post.mock.calls.filter(([path]) => path === "/api/v1/sessions");
	expect(launches).toHaveLength(2);
	expect(launches[1][1]?.body?.clientRequestId).not.toBe(launches[0][1]?.body?.clientRequestId);
	expect(launches[1][1]?.body?.prompt).toContain("I installed this harness");
	previousTerminal?.("exited");
	expect(screen.getByRole("button", { name: "Stop helper" })).toBeEnabled();
	expect(screen.queryByRole("button", { name: "Troubleshoot with agent" })).not.toBeInTheDocument();
});

it("shows which check is running and keeps old results from appearing unresolved during the check", async () => {
	const checks = mockChecks();
	const snapshot = troubleshooting.troubleshootingSnapshot(agentReadiness("codex"), "0.13.0", "win32");
	let finish: (value: typeof snapshot) => void = () => {};
	checks.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
	setup();
	fireEvent.click(screen.getByRole("button", { name: "Collect fresh diagnostics" }));
	await screen.findByText("Checking installation and sign-in…");
	expect(screen.getByRole("button", { name: "Collect fresh diagnostics" })).toBeDisabled();
	expect(screen.queryByText(/Verification is unresolved/)).not.toBeInTheDocument();
	finish(snapshot);
	await screen.findByText(/Initial checks collected/);
	expect(screen.queryByText("Checking installation and sign-in…")).not.toBeInTheDocument();
});

it("uses the installation symptom for a harness AO cannot find", async () => {
	mockChecks();
	const post = mockSessionPost();
	setup([agentReadiness("claude-code"), agentReadiness("codex", "Codex", { installation: "not_installed" })]);
	expect(screen.getByRole("combobox", { name: "What are you experiencing?" })).toHaveValue("installation");
	await collect();
	fireEvent.click(screen.getByRole("button", { name: "Troubleshoot with agent" }));
	await screen.findByTestId("helper-terminal");
	expect(post.mock.calls[0][1]?.body?.prompt).toContain("I installed this harness");
	expect(within(screen.getByTestId("helper-controls")).getByRole("button", { name: "Verify again" })).toBeVisible();
});

it("requires confirmation before replacing an edited issue draft", async () => {
	mockChecks();
	setup();
	await collect();
	fireEvent.click(screen.getByRole("button", { name: "Prepare issue draft" }));
	const draft = screen.getByRole("textbox", { name: "Review and edit issue draft" });
	expect(draft).toHaveFocus();
	fireEvent.change(draft, { target: { value: "My carefully edited report" } });
	fireEvent.change(screen.getByRole("combobox", { name: "What are you experiencing?" }), { target: { value: "other" } });
	expect(draft).toHaveValue("My carefully edited report");
	fireEvent.click(screen.getByRole("button", { name: "Replace draft with latest checks" }));
	const dialog = screen.getByRole("dialog", { name: "Replace your edited draft?" });
	expect(draft).toHaveValue("My carefully edited report");
	fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
	await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	expect(draft).toHaveValue("My carefully edited report");
	fireEvent.click(screen.getByRole("button", { name: "Replace draft with latest checks" }));
	fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Replace draft with latest checks" }));
	await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	expect(draft).not.toHaveValue("My carefully edited report");
});
