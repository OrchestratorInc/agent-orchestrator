import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { typeInLexicalEditor } from "../../test/lexical";
import { beforeEach, expect, it, vi } from "vitest";
import type { TriageChoice, TriageRun } from "../../../shared/triage";
import { useTriageStore } from "../../stores/triage-store";
import { readTaskComposerPreferences } from "../../lib/task-composer-preferences";
import { STANDALONE_WORKSPACE_ID } from "../../types/workspace";
import { queryClient } from "../../lib/query-client";
import { TriageConversation } from "../triage/TriageConversation";
import { ReportProblemContent } from "./ReportProblemContent";
import { SessionTopbarProvider } from "../SessionTopbarPortal";

const mock = vi.hoisted(() => ({ catalog: vi.fn(), configure: vi.fn(), list: vi.fn(), get: vi.fn(), prepare: vi.fn(), start: vi.fn(), reply: vi.fn(), cancel: vi.fn(), subscribe: vi.fn() }));
vi.mock("../../lib/bridge", () => ({ aoBridge: { ...window.ao, triage: mock } }));
const configured: TriageChoice = { harness: "codex", label: "Codex", available: true, sessionMode: "chat", models: [{ id: "gpt", label: "GPT", default: true }] };
function run(overrides: Partial<TriageRun> = {}): TriageRun {
	return { id: "report-1", version: 1, createdAt: "2026-10-19T10:00:00Z", updatedAt: "2026-10-19T10:00:00Z",
		request: { harness: "codex", model: "gpt", sessionMode: "chat", publishAuthorized: false }, state: "running", activity: "Reading AO diagnostics", messages: [], transport: "chat", ...overrides };
}
beforeEach(() => {
	vi.clearAllMocks();
	useTriageStore.setState(useTriageStore.getInitialState(), true);
	queryClient.clear(); localStorage.clear(); window.location.hash = "/";
	mock.catalog.mockResolvedValue([configured]); mock.configure.mockResolvedValue(undefined);
	mock.get.mockResolvedValue(run()); mock.start.mockResolvedValue(run()); mock.reply.mockResolvedValue(run());
	mock.prepare.mockResolvedValue(run({ id: "preparation", preparation: true, state: "prepared", request: { harness: "", publishAuthorized: false }, activity: "Diagnostics ready" }));
	mock.cancel.mockResolvedValue(run({ state: "cancelled" }));
});

it("collects while the chooser loads and authorizes the selected agent only on Start", async () => {
	let resolveCatalog!: (value: TriageChoice[]) => void;
	mock.catalog.mockReturnValue(new Promise<TriageChoice[]>((resolve) => { resolveCatalog = resolve; }));
	render(<ReportProblemContent active />);
	fireEvent.click(screen.getByRole("button", { name: "Report a problem" }));
	await screen.findByText("Diagnostics ready");
	expect(mock.prepare).toHaveBeenCalledOnce();
	expect(mock.start).not.toHaveBeenCalled();
	expect(mock.prepare.mock.calls[0][0]).not.toHaveProperty("publishAuthorized");
	await act(async () => resolveCatalog([configured]));
	await waitFor(() => expect(screen.getByRole("button", { name: "Support agent" })).toHaveTextContent("Codex"));
	expect(screen.queryByRole("button", { name: "Agent settings" })).toBeNull();
	fireEvent.click(screen.getByRole("button", { name: "Start investigation" }));
	await waitFor(() => expect(mock.start).toHaveBeenCalledWith(expect.objectContaining({ harness: "codex", model: "gpt", sessionMode: "chat", preparationId: "preparation", publishAuthorized: true })));
	await waitFor(() => expect(window.location.hash).toBe("#/triage?run=report-1"));
});

it("cancels a dismissed preparation whose acknowledgement arrives late", async () => {
	let finish!: (value: TriageRun) => void;
	mock.prepare.mockReturnValue(new Promise<TriageRun>((resolve) => { finish = resolve; }));
	render(<ReportProblemContent active />);
	fireEvent.click(screen.getByRole("button", { name: "Report a problem" }));
	await screen.findByText("Collecting diagnostics…");
	fireEvent.click(screen.getByRole("button", { name: "Close" }));
	await act(async () => finish(run({ id: "late", preparation: true })));
	await waitFor(() => expect(mock.cancel).toHaveBeenCalledWith("late"));
	expect(mock.start).not.toHaveBeenCalled();
});

it("replays an uncertain launch unchanged after its models disappear", async () => {
	const pending = run({ execution: "session", sessionLaunchStarted: true, transport: undefined,
		request: { harness: "codex", model: "removed", accountId: "old-account", sessionMode: "tui", preparationId: "report-1", publishAuthorized: false } });
	useTriageStore.getState().receive(pending);
	mock.catalog.mockResolvedValue([]);
	mock.start.mockResolvedValue({ ...pending, sessionId: "normal-session" });
	render(<ReportProblemContent active />);
	fireEvent.click(screen.getByRole("button", { name: "Report a problem" }));
	await waitFor(() => expect(screen.getByRole("button", { name: "Start investigation" })).toBeEnabled());
	fireEvent.click(screen.getByRole("button", { name: "Start investigation" }));
	await waitFor(() => expect(mock.start).toHaveBeenCalledExactlyOnceWith(pending.request));
	expect(mock.prepare).not.toHaveBeenCalled();
	expect(mock.cancel).not.toHaveBeenCalled();
});

it("keeps ordinary text editable in the normal Chat composer and Stop calls cancel", async () => {
	const user = userEvent.setup();
	const current = run({ state: "completed" });
	mock.get.mockResolvedValue(current);
	useTriageStore.getState().receive(current);
	render(<SessionTopbarProvider><TriageConversation runId={current.id} /></SessionTopbarProvider>);
	const input = await screen.findByRole("combobox", { name: "Message the agent" });
	await typeInLexicalEditor(input, "The sidebar is blank");
	await user.keyboard("{Enter}");
	await waitFor(() => expect(mock.reply).toHaveBeenCalledWith(current.id, "The sidebar is blank", undefined));
	act(() => useTriageStore.getState().receive({ ...current, updatedAt: "2026-10-19T10:01:00Z", state: "working" }));
	fireEvent.click(await screen.findByRole("button", { name: "Stop" }));
	await waitFor(() => expect(mock.cancel).toHaveBeenCalledWith(current.id));
});

it("renders native approvals through the existing decision dock", async () => {
	const current = run({ state: "needs_input", pendingInput: { id: "approval", kind: "approval", summary: "Run command", detail: {
		command: "read diagnostics", decisions: [{ id: "allow_once", label: "Allow Once", kind: "allow_once" }, { id: "deny", label: "Deny", kind: "reject_once" }],
	} } });
	mock.get.mockResolvedValue(current);
	useTriageStore.getState().receive(current);
	render(<SessionTopbarProvider><TriageConversation runId={current.id} /></SessionTopbarProvider>);
	fireEvent.click(await screen.findByRole("button", { name: /Allow once/ }));
	await waitFor(() => expect(mock.reply).toHaveBeenCalledWith(current.id, "", { requestId: "approval", decisionId: "allow_once" }));
});

it("renders native questions through the existing form dock", async () => {
	const current = run({ state: "needs_input", pendingInput: { id: "question", kind: "user_input", summary: "What happened?", detail: {
		inputMode: "form", message: "What happened?", schema: { type: "object", properties: { symptom: { type: "string", title: "Symptom", oneOf: [{ const: "blank", title: "Blank window" }] } }, required: ["symptom"] },
	} } });
	mock.get.mockResolvedValue(current);
	useTriageStore.getState().receive(current);
	render(<SessionTopbarProvider><TriageConversation runId={current.id} /></SessionTopbarProvider>);
	fireEvent.click(await screen.findByRole("radio", { name: "Blank window" }));
	fireEvent.click(screen.getByRole("button", { name: "Continue" }));
	await waitFor(() => expect(mock.reply).toHaveBeenCalledWith(current.id, "", { requestId: "question", action: "accept", content: { symptom: "blank" } }));
});


it("keeps a successful ordinary session link when later helper snapshots are stale", () => {
	const linked = run({ execution: "session", sessionId: "normal-session" });
	useTriageStore.getState().receive(linked);
	useTriageStore.getState().receive({ ...linked, sessionId: undefined, updatedAt: "2026-10-19T10:01:00Z" });
	expect(useTriageStore.getState().runs[0].sessionId).toBe("normal-session");
	useTriageStore.getState().receive({ ...linked, sessionId: "another-session", updatedAt: "2026-10-19T10:02:00Z" });
	expect(useTriageStore.getState().runs[0].sessionId).toBe("normal-session");
});


it("preserves the message when the helper rejects a send", async () => {
	const user = userEvent.setup();
	const current = run({ state: "completed" });
	mock.get.mockResolvedValue(current);
	mock.reply.mockRejectedValueOnce(new Error("Helper disconnected"));
	useTriageStore.getState().receive(current);
	render(<SessionTopbarProvider><TriageConversation runId={current.id} /></SessionTopbarProvider>);
	const input = await screen.findByRole("combobox", { name: "Message the agent" });
	await typeInLexicalEditor(input, "Keep this observation");
	await user.keyboard("{Enter}");
	await waitFor(() => expect(mock.reply).toHaveBeenCalledOnce());
	await waitFor(() => expect(useTriageStore.getState().runErrors[current.id]).toBe("Helper disconnected"));
	await waitFor(() => expect(input).toHaveTextContent("Keep this observation"));
});


it("saves native mode choices in the normal standalone composer preference", async () => {
	queryClient.setQueryData(["agent-models", "amp", ""], { selectionMode: "mode", models: [{ id: "medium", label: "Medium" }] });
	useTriageStore.getState().setSelection({ harness: "amp", model: "medium", mode: "low", sessionMode: "tui" });
	mock.start.mockResolvedValue(run({ request: { harness: "amp", model: "medium", publishAuthorized: false }, transport: "terminal" }));
	await useTriageStore.getState().start();
	expect(readTaskComposerPreferences(STANDALONE_WORKSPACE_ID)?.agents.amp).toEqual({ model: "", mode: "medium" });
});
