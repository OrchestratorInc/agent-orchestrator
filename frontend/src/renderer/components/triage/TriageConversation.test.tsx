import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import type { TriageRun } from "../../../shared/triage";
import type { AttachableTerminal } from "../../hooks/useTerminalSession";
import { useTriageStore } from "../../stores/triage-store";
import { TriageConversation } from "./TriageConversation";

const mock = vi.hoisted(() => ({ get: vi.fn(), cancel: vi.fn(), terminalInput: vi.fn(), terminalResize: vi.fn(), subscribeTerminal: vi.fn(), write: vi.fn(),
	reply: vi.fn(), send: undefined as ((text: string, attachments?: { mimeType: string; data: string }[]) => unknown) | undefined,
	input: undefined as ((data: string) => boolean | void) | undefined }));
vi.mock("../../lib/bridge", () => ({ aoBridge: { ...window.ao, triage: mock } }));
vi.mock("../chat/ChatWorkspace", () => ({ ChatWorkspace: ({ onSend }: { onSend: typeof mock.send }) => { mock.send = onSend; return null; } }));
vi.mock("../XtermTerminal", () => ({ XtermTerminal: ({ onReady, onVisibleSize }: import("../XtermTerminal").XtermTerminalProps) => {
	const terminal: AttachableTerminal = {
		cols: 80, rows: 24, hasMeasuredGrid: true, write: mock.write, writeln: () => {}, showLatestOutput: () => {},
		prepareForActivation: async () => {}, requestActivationFocus: () => {}, notifyCursorColorScheme: () => {}, sendUserInput: () => true,
		onUserInput: (listener) => { mock.input = (data) => listener(data, "keyboard"); return { dispose: () => { mock.input = undefined; } }; },
		onResize: () => ({ dispose: () => {} }),
	};
	return <button onClick={() => { onReady?.(terminal); onVisibleSize?.(80, 24); }}>Attach terminal</button>;
} }));
const run: TriageRun = { id: "terminal", version: 1, createdAt: "2026-10-19T00:00:00Z", updatedAt: "2026-10-19T00:00:00Z",
	request: { harness: "amp", publishAuthorized: false }, state: "working", activity: "Investigating", messages: [], transport: "terminal" };
beforeEach(() => {
	vi.clearAllMocks();
	useTriageStore.setState(useTriageStore.getInitialState(), true);
	useTriageStore.getState().receive(run);
	mock.terminalInput.mockResolvedValue(undefined); mock.terminalResize.mockResolvedValue(undefined);
	mock.cancel.mockResolvedValue({ ...run, state: "cancelled" });
});

it("subscribes before loading replay, forwards raw input and resize, and keeps Stop available", async () => {
	const order: string[] = [];
	let finish!: (value: TriageRun) => void;
	let output!: (data: string, sequence: number) => void;
	const snapshot = new Promise<TriageRun>((resolve) => { finish = resolve; });
	mock.get.mockImplementation(() => { order.push("get"); return snapshot; });
	const unsubscribe = vi.fn();
	mock.subscribeTerminal.mockImplementation((_id, listener) => { order.push("subscribe"); output = listener; return unsubscribe; });
	const view = render(<TriageConversation runId={run.id} />);
	expect(order[0]).toBe("subscribe");
	act(() => output(btoa("cdef"), 6));
	fireEvent.click(screen.getByRole("button", { name: "Attach terminal" }));
	await act(async () => finish({ ...run, terminalReplay: btoa("abcd"), terminalSequence: 4 }));
	expect(mock.write.mock.calls.map(([bytes]) => new TextDecoder().decode(bytes)).join("")).toBe("abcdef");
	act(() => mock.input?.("\u001b[A\r\0"));
	await waitFor(() => expect(mock.terminalInput).toHaveBeenCalledWith(run.id, "\u001b[A\r\0"));
	expect(mock.terminalResize).toHaveBeenCalledWith(run.id, 24, 80);
	act(() => useTriageStore.setState({ pending: { [run.id]: true } }));
	fireEvent.click(screen.getByRole("button", { name: "Stop" }));
	await waitFor(() => expect(mock.cancel).toHaveBeenCalledWith(run.id));
	view.unmount();
	expect(unsubscribe).toHaveBeenCalledOnce();
	expect(mock.input).toBeUndefined();
});

it("rejects attachments instead of silently sending only their accompanying text", async () => {
	const chat: TriageRun = { ...run, transport: "chat", state: "completed" };
	mock.get.mockResolvedValue(chat);
	useTriageStore.getState().receive(chat);
	render(<TriageConversation runId={chat.id} />);
	expect(mock.send).toBeDefined();
	await expect(mock.send!("See this screenshot", [{ mimeType: "image/png", data: "aW1hZ2U=" }])).rejects.toThrow("Reconnect AO to send files");
	expect(mock.reply).not.toHaveBeenCalled();
});
