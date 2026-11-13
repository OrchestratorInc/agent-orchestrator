import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
const artifactSubscriptions = vi.hoisted(() => [] as string[]);
vi.mock("../../../lib/artifact-file-events", () => ({ subscribeArtifactFileChanges: (sessionId: string) => { artifactSubscriptions.push(sessionId); return () => {}; } }));

const openAccount = vi.fn();
beforeEach(() => {
	useHarnessTroubleshooting.setState({ entries: {} }); terminalCallbacks.length = 0; artifactSubscriptions.length = 0; openAccount.mockClear();
	vi.spyOn(troubleshooting, "fetchTroubleshootingFindings").mockResolvedValue({ status: "missing" });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

function setup(agents = [agentReadiness("claude-code"), agentReadiness("codex")]) {
	return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><HarnessTroubleshootingPanel agentId="codex" agents={agents} installationBusy={false} onOpenAccount={openAccount} /></QueryClientProvider>);
}
async function collect() {
	fireEvent.click(screen.getByRole("button", { name: "Check installation and sign-in" }));
	await screen.findByRole("button", { name: "Copy troubleshooting prompt" });
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
	expect(screen.queryByRole("button", { name: "Troubleshoot with agent" })).not.toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Open Claude Code setup" }));
	expect(openAccount).toHaveBeenCalledWith("claude-code");
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
	await screen.findByText("Your setup checks passed");
	checks.mockRejectedValueOnce(new Error("Probe unavailable"));
	fireEvent.click(screen.getByRole("button", { name: "Verify again" }));
	await screen.findByRole("alert");
	fireEvent.click(screen.getByRole("button", { name: "Copy troubleshooting prompt" }));
	await screen.findByText("Copied to clipboard");
	expect(screen.queryByText("Your setup checks passed")).not.toBeInTheDocument();
	expect(screen.getByText("We could not finish checking")).toBeInTheDocument();
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
	await screen.findByText("We could not finish checking");
	view.unmount();
	setup([]);
	fireEvent.click(screen.getByRole("button", { name: "Retry helper launch" }));
	await screen.findByTestId("helper-terminal");
	expect(post.mock.calls[1]).toEqual(post.mock.calls[0]);
	expect(post).toHaveBeenCalledTimes(2);
});

it("releases a launch the daemon definitively rejected so a changed request gets a new identity", async () => {
	mockChecks();
	const post = mockSessionPost().mockResolvedValueOnce({ error: { code: "SPAWN_INTERNAL", message: "Spawn failed" } });
	setup();
	await collect();
	fireEvent.click(screen.getByRole("button", { name: "Troubleshoot with agent" }));
	await screen.findByRole("alert");
	expect(screen.queryByRole("button", { name: "Retry helper launch" })).not.toBeInTheDocument();
	const symptom = screen.getByRole("combobox", { name: "What are you experiencing?" });
	expect(symptom).toBeEnabled();
	fireEvent.change(symptom, { target: { value: "installation" } });
	fireEvent.click(screen.getByRole("button", { name: "Troubleshoot with agent" }));
	await screen.findByTestId("helper-terminal");
	const launches = post.mock.calls.filter(([path]) => path === "/api/v1/sessions");
	expect(launches).toHaveLength(2);
	expect(launches[1][1]?.body?.clientRequestId).not.toBe(launches[0][1]?.body?.clientRequestId);
	expect(launches[1][1]?.body?.prompt).toContain("I installed this harness");
});

it("keeps Retry for an unfinished earlier launch and offers a safe new launch", async () => {
	mockChecks();
	const post = mockSessionPost()
		.mockResolvedValueOnce({ error: { code: "CLIENT_REQUEST_INCOMPLETE", message: "Prior launch did not finish" } })
		.mockResolvedValueOnce({ error: { code: "CLIENT_REQUEST_INCOMPLETE", message: "Prior launch did not finish" } });
	setup();
	await collect();
	fireEvent.click(screen.getByRole("button", { name: "Troubleshoot with agent" }));
	await screen.findByText(/could not confirm that the earlier helper launch finished/);
	expect(screen.getByRole("combobox", { name: "What are you experiencing?" })).toBeDisabled();
	fireEvent.click(screen.getByRole("button", { name: "Retry helper launch" }));
	await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
	expect(post.mock.calls[1]).toEqual(post.mock.calls[0]);
	await screen.findByRole("button", { name: "Start a new helper instead" });
	fireEvent.click(screen.getByRole("button", { name: "Start a new helper instead" }));
	expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	expect(screen.getByRole("combobox", { name: "What are you experiencing?" })).toBeEnabled();
	fireEvent.click(screen.getByRole("button", { name: "Troubleshoot with agent" }));
	await screen.findByTestId("helper-terminal");
	expect(post).toHaveBeenCalledTimes(3);
	expect(post.mock.calls[2][1]?.body?.clientRequestId).not.toBe(post.mock.calls[0][1]?.body?.clientRequestId);
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
	let changeStage: ((stage: troubleshooting.TroubleshootingCheckStage) => void) | undefined;
	checks.mockImplementationOnce((_id, _client, _version, _platform, onStage) => { changeStage = onStage; return new Promise((resolve) => { finish = resolve; }); });
	setup();
	fireEvent.click(screen.getByRole("button", { name: "Check installation and sign-in" }));
	await screen.findByText("Checking installation…");
	act(() => changeStage?.("authentication"));
	await screen.findByText("Checking sign-in…");
	act(() => changeStage?.("results"));
	await screen.findByText("Preparing your results…");
	expect(screen.getByRole("button", { name: "Check installation and sign-in" })).toBeDisabled();
	expect(screen.queryByText("We could not finish checking")).not.toBeInTheDocument();
	finish(snapshot);
	await screen.findByRole("button", { name: "Copy troubleshooting prompt" });
	expect(screen.queryByText("Preparing your results…")).not.toBeInTheDocument();
});

it.each(["configured", "unknown"] as const)("explains %s sign-in without claiming the user is signed out", async (authentication) => {
	const checks = mockChecks();
	const snapshot = troubleshooting.troubleshootingSnapshot(agentReadiness("codex", "Codex", { authentication }), "0.13.0", "win32");
	snapshot.installationVerification = "succeeded";
	checks.mockResolvedValue(snapshot);
	setup();
	await collect();
	expect(screen.getByText("Sign-in is not confirmed")).toBeVisible();
	expect(screen.getByText(/this does not prove you are signed out/)).toBeVisible();
	expect(screen.getByText(/keeps this diagnostic report on your computer/)).toBeVisible();
});

it("offers the target sign-in page when fresh authentication was rejected", async () => {
	const checks = mockChecks();
	const snapshot = troubleshooting.troubleshootingSnapshot(agentReadiness("codex", "Codex", { authentication: "unauthorized" }), "0.13.0", "win32");
	snapshot.installationVerification = "succeeded";
	checks.mockResolvedValue(snapshot);
	setup();
	await collect();
	expect(screen.getByText("Sign-in was not accepted")).toBeVisible();
	fireEvent.click(screen.getByRole("button", { name: "Open Codex setup" }));
	expect(openAccount).toHaveBeenCalledWith("codex");
});

it("offers setup when a fresh installation check cannot find the agent", async () => {
	const checks = mockChecks();
	const snapshot = troubleshooting.troubleshootingSnapshot(agentReadiness("codex", "Codex", { installation: "not_installed" }), "0.13.0", "win32");
	snapshot.installationVerification = "failed";
	checks.mockResolvedValue(snapshot);
	setup();
	await collect();
	expect(screen.getByText("AO cannot find this agent")).toBeVisible();
	fireEvent.click(screen.getByRole("button", { name: "Open Codex setup" }));
	expect(openAccount).toHaveBeenCalledWith("codex");
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

it("shows helper findings, recommends reporting a suspected AO bug and opens a prefilled issue", async () => {
	const checks = mockChecks();
	mockSessionPost();
	const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
	const findings = vi.mocked(troubleshooting.fetchTroubleshootingFindings);
	setup();
	await collect();
	fireEvent.click(screen.getByRole("button", { name: "Troubleshoot with agent" }));
	await screen.findByText("Your helper has not recorded findings yet. They appear here while it works.");
	expect(artifactSubscriptions).toEqual(["helper-1"]);
	expect(findings).toHaveBeenCalledWith("helper-1", expect.anything());
	findings.mockResolvedValue({ status: "ready", findings: {
		cause: "suspected_ao_bug", outcome: "not_fixed", nativeCliWorks: true, summary: "codex login status succeeds, but AO reports signed out.",
		evidence: ["codex login status exits 0"], repairs: [], uncertainty: "Whether the daemon reads a different home directory.",
	} });
	const failing = troubleshooting.troubleshootingSnapshot(agentReadiness("codex", "Codex", { authentication: "unauthorized" }), "0.13.0", "darwin");
	failing.installationVerification = "succeeded";
	checks.mockResolvedValue(failing);
	fireEvent.click(within(screen.getByTestId("helper-controls")).getByRole("button", { name: "Verify again" }));
	await screen.findByText("Likely cause: a problem in AO");
	expect(screen.getByText("codex login status succeeds, but AO reports signed out.")).toBeVisible();
	expect(screen.getByText("This looks like a problem in AO. Please report it so we can fix it.")).toBeVisible();
	fireEvent.click(screen.getByRole("button", { name: "Prepare issue draft" }));
	const draft = screen.getByRole("textbox", { name: "Review and edit issue draft" });
	expect((draft as HTMLTextAreaElement).value).toMatch(/^AO does not recognize a valid codex login/);
	expect((draft as HTMLTextAreaElement).value).toContain("Helper findings (written by");
	fireEvent.click(screen.getByRole("button", { name: "Open GitHub issue" }));
	await waitFor(() => expect(openExternal).toHaveBeenCalledTimes(1));
	const url = new URL(openExternal.mock.calls[0][0]);
	expect(url.hostname).toBe("github.com");
	expect(url.searchParams.get("title")).toBe("AO does not recognize a valid codex login");
	expect(url.searchParams.get("body")).toContain("codex login status exits 0");
});

it("does not recommend a report when checks pass and the helper found nothing", async () => {
	mockChecks();
	setup();
	await collect();
	expect(screen.getByText(/so a report is usually not needed/)).toBeVisible();
	expect(screen.getByRole("button", { name: "Prepare issue draft" })).toBeEnabled();
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
