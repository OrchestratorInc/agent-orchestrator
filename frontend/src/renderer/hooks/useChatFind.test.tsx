import { useRef } from "react";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useChatFind } from "./useChatFind";
import type { ConversationMessage, ConversationSnapshot } from "../types/conversation";
import { useKeybindingsStore } from "../stores/keybindings-store";

const get = vi.hoisted(() => vi.fn());
vi.mock("../lib/host-clients", () => ({ clientForSessionHost: () => ({ GET: get }) }));
vi.mock("./useConversation", () => ({
	toSnapshot: (value: unknown) => value,
	mergeConversationPages: (pages: ConversationSnapshot[]) => ({ ...pages[0], items: [...pages[1].items, ...pages[0].items] }),
}));

const source: ConversationSnapshot = {
	conversationId: "c", sessionId: "s", activeBranchId: "b", harness: "codex", mode: "chat", controller: { state: "ready" },
	latestSequence: 10, oldestSequence: 10, hasMoreBefore: true, turns: [], settings: {},
	items: [{ kind: "message", id: "m", role: "user", origin: "human", text: "hello hello", sequence: 10, revision: 1, streaming: false, createdAt: "2026-11-03T00:00:00Z" }],
};
const hits = [{ messageId: "m", revision: 1, sequence: 10, block: 0, start: 0, end: 5 }, { messageId: "m", revision: 1, sequence: 10, block: 0, start: 6, end: 11 }];
const reveal = vi.fn(), pause = vi.fn(), send = vi.fn(), interrupt = vi.fn();

function Harness({ snapshot = source }: { snapshot?: ConversationSnapshot }) {
	const root = useRef<HTMLDivElement>(null), viewport = useRef<HTMLDivElement>(null);
	const find = useChatFind({ source: snapshot, root, viewport, reveal, pauseFollow: pause });
	return <section data-chat-find-scope="" onKeyDown={(event) => { if (event.key === "Escape") interrupt(); }}>
		<div ref={root} data-chat-find-surface="">{find.bar}<div ref={viewport} tabIndex={-1}>{find.snapshot.items.map((item) => <div key={item.id} data-find-message-id={item.id}><p data-chat-message-body="">{item.kind === "message" ? item.text : ""}</p></div>)}</div></div>
		<textarea aria-label="Composer" defaultValue="draft" onKeyDown={(event) => { if (event.key === "Enter") send(); }} />
	</section>;
}

async function tick(ms = 180) { await act(async () => { await vi.advanceTimersByTimeAsync(ms); }); }
async function openAndSearch() {
	fireEvent.click(screen.getByRole("button", { name: "Find in chat" })); await tick(20);
	fireEvent.change(screen.getByRole("searchbox", { name: "Find in chat" }), { target: { value: "hello" } }); await tick();
}

describe("find in chat interactions", () => {
	beforeEach(() => {
		vi.useFakeTimers(); get.mockReset(); reveal.mockClear(); pause.mockClear(); send.mockClear(); interrupt.mockClear();
		get.mockResolvedValue({ data: { conversationId: "c", transcriptRevision: "revision", occurrences: hits, total: 2 }, response: { status: 200 } });
		vi.spyOn(HTMLElement.prototype, "getClientRects").mockReturnValue([{ top: 0, bottom: 100 }] as unknown as DOMRectList);
		Object.defineProperty(Range.prototype, "getBoundingClientRect", { configurable: true, value: () => ({ top: 20, height: 10 }) });
		Object.defineProperty(HTMLElement.prototype, "scrollTo", { configurable: true, value: vi.fn() });
		useKeybindingsStore.setState({ overrides: {} });
	});
	afterEach(() => { cleanup(); vi.clearAllTimers(); vi.useRealTimers(); vi.restoreAllMocks(); });

	it("finds both occurrences and navigates, wraps, closes and preserves the draft", async () => {
		render(<Harness />); const composer = screen.getByRole("textbox", { name: "Composer" }); composer.focus();
		await openAndSearch();
		expect(screen.getByText("1 of 2")).toBeInTheDocument();
		const input = screen.getByRole("searchbox");
		fireEvent.keyDown(input, { key: "Enter" }); await tick(0); expect(screen.getByText("2 of 2")).toBeInTheDocument();
		fireEvent.keyDown(input, { key: "Enter" }); await tick(0); expect(screen.getByText("Wrapped to the first match.")).toBeInTheDocument();
		fireEvent.keyDown(input, { key: "Enter", shiftKey: true }); await tick(0); expect(screen.getByText("2 of 2")).toBeInTheDocument();
		fireEvent.keyDown(input, { key: "Escape" });
		expect(screen.queryByRole("searchbox")).not.toBeInTheDocument(); expect(composer).toHaveValue("draft"); expect(composer).toHaveFocus();
		expect(send).not.toHaveBeenCalled(); expect(interrupt).not.toHaveBeenCalled(); expect(pause).toHaveBeenCalled();
		fireEvent.click(screen.getByRole("button", { name: "Find in chat" })); expect(screen.getByRole("searchbox")).toHaveValue("hello");
	});

	it("debounces typing and waits for IME composition", async () => {
		render(<Harness />); fireEvent.click(screen.getByRole("button", { name: "Find in chat" }));
		const input = screen.getByRole("searchbox"); fireEvent.compositionStart(input);
		fireEvent.change(input, { target: { value: "hello" } }); await tick(500); expect(get).not.toHaveBeenCalled();
		fireEvent.keyDown(input, { key: "Enter", isComposing: true }); expect(reveal).not.toHaveBeenCalled();
		fireEvent.compositionEnd(input); await tick(100); expect(get).not.toHaveBeenCalled(); await tick(60); expect(get).toHaveBeenCalledTimes(1);
	});

	it("distinguishes an error from no matches and supports retry", async () => {
		get.mockRejectedValueOnce(new Error("Offline")); render(<Harness />); await openAndSearch();
		expect(screen.getByText("Search incomplete")).toBeInTheDocument(); expect(screen.queryByText("No results")).not.toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Retry" })); await tick(); expect(screen.getByText("1 of 2")).toBeInTheDocument();
	});

	it("does not accept a late response for an old query", async () => {
		let resolve: (value: unknown) => void = () => {};
		get.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
		render(<Harness />); await openAndSearch();
		fireEvent.change(screen.getByRole("searchbox"), { target: { value: "new" } }); await tick();
		await act(async () => resolve({ data: { conversationId: "c", transcriptRevision: "old", occurrences: [], total: 0 }, response: { status: 200 } }));
		expect(screen.queryByText("No results")).not.toBeInTheDocument(); expect(screen.getByText("1 of 2")).toBeInTheDocument();
	});

	it("loads a target window instead of walking older pages", async () => {
		const oldHit = { ...hits[0], messageId: "old", sequence: 1 };
		get.mockResolvedValueOnce({ data: { conversationId: "c", transcriptRevision: "r", occurrences: [oldHit], total: 1 }, response: { status: 200 } });
		get.mockResolvedValueOnce({ data: { ...source, items: [{ ...source.items[0], id: "old", sequence: 1 }] }, response: { status: 200 } });
		render(<Harness />); await openAndSearch();
		expect(get.mock.calls[1][0]).toContain("find-window"); expect(get.mock.calls[1][1].params.query).toEqual({ sequence: 1 });
		expect(reveal).toHaveBeenCalledWith(oldHit);
	});

	it("clears search state when the conversation changes", async () => {
		const view = render(<Harness />); await openAndSearch(); view.rerender(<Harness snapshot={{ ...source, conversationId: "other" }} />);
		expect(screen.queryByRole("searchbox")).not.toBeInTheDocument(); fireEvent.click(screen.getByRole("button", { name: "Find in chat" })); expect(screen.getByRole("searchbox")).toHaveValue("");
	});

	it("does not capture find from a terminal or modal", async () => {
		render(<Harness />);
		const terminal = document.createElement("textarea"); terminal.className = "xterm"; document.body.append(terminal); terminal.focus();
		fireEvent.keyDown(terminal, { key: "f", ctrlKey: true }); expect(screen.queryByRole("searchbox")).not.toBeInTheDocument(); terminal.remove();
		const modal = document.createElement("div"); modal.setAttribute("role", "dialog"); document.body.append(modal);
		fireEvent.keyDown(screen.getByRole("textbox"), { key: "f", ctrlKey: true }); expect(screen.queryByRole("searchbox")).not.toBeInTheDocument(); modal.remove();
	});

	it("uses the configured shortcut from the composer", async () => {
		useKeybindingsStore.setState({ overrides: { "find-in-chat": [{ key: "j", ctrl: true, meta: false, shift: true, alt: false }] } });
		render(<Harness />); const composer = screen.getByRole("textbox"); composer.focus();
		fireEvent.keyDown(composer, { key: "f", ctrlKey: true }); expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
		fireEvent.keyDown(composer, { key: "J", ctrlKey: true, shiftKey: true }); await tick(20); expect(screen.getByRole("searchbox")).toHaveFocus();
		fireEvent.keyDown(composer, { key: "Escape" }); expect(interrupt).not.toHaveBeenCalled(); expect(composer).toHaveFocus();
	});

	it("routes find to the focused chat when two transcripts are mounted", async () => {
		render(<><Harness /><Harness snapshot={{ ...source, conversationId: "other" }} /></>);
		const composers = screen.getAllByRole("textbox"); composers[1].focus();
		fireEvent.keyDown(composers[1], { key: "f", ctrlKey: true }); await tick(20);
		expect(screen.getAllByRole("searchbox")).toHaveLength(1);
		expect(screen.getByRole("searchbox").closest("section")).toBe(composers[1].closest("section"));
	});

	it("does not replace a stable active result when streaming adds matches", async () => {
		const view = render(<Harness />); await openAndSearch();
		const streaming = { ...source.items[0] as ConversationMessage, id: "stream", role: "assistant" as const, origin: "provider" as const, text: "hello", streaming: true, sequence: 11 };
		get.mockResolvedValueOnce({ data: { conversationId: "c", transcriptRevision: "stream1", occurrences: [...hits, { ...hits[0], messageId: "stream", sequence: 11 }], total: 3 }, response: { status: 200 } });
		view.rerender(<Harness snapshot={{ ...source, items: [...source.items, streaming] }} />); await tick();
		expect(screen.getByText("1 of 3")).toBeInTheDocument(); expect(reveal).toHaveBeenCalledTimes(1);
		get.mockResolvedValueOnce({ data: { conversationId: "c", transcriptRevision: "stream2", occurrences: [...hits, ...hits.map((hit) => ({ ...hit, messageId: "stream", sequence: 11, revision: 2 }))], total: 4 }, response: { status: 200 } });
		view.rerender(<Harness snapshot={{ ...source, items: [...source.items, { ...streaming, text: "hello hello", revision: 2 }] }} />); await tick();
		expect(screen.getByText("1 of 4")).toBeInTheDocument(); expect(reveal).toHaveBeenCalledTimes(1);
	});

	it("validates an over-limit query without truncating it", async () => {
		render(<Harness />); fireEvent.click(screen.getByRole("button", { name: "Find in chat" }));
		const value = "x".repeat(513); fireEvent.change(screen.getByRole("searchbox"), { target: { value } }); await tick();
		expect(screen.getByRole("searchbox")).toHaveValue(value); expect(screen.getByText(/up to 512 characters/)).toBeInTheDocument(); expect(get).not.toHaveBeenCalled();
	});

	it("keeps the query open and refreshes after a branch switch", async () => {
		const view = render(<Harness />); await openAndSearch();
		view.rerender(<Harness snapshot={{ ...source, activeBranchId: "new-branch" }} />);
		expect(screen.getByRole("searchbox")).toHaveValue("hello"); await tick(); expect(screen.getByText("1 of 2")).toBeInTheDocument();
	});

	it("treats an empty new conversation as no results without an invalid API request", async () => {
		render(<Harness snapshot={{ ...source, conversationId: "", items: [], hasMoreBefore: false }} />); await openAndSearch();
		expect(screen.getByText("No results")).toBeInTheDocument(); expect(get).not.toHaveBeenCalled();
	});

	it("keeps a million-hit search bounded and loads only the next result page", async () => {
		const pageHits = (offset: number) => Array.from({ length: 200 }, (_, index) => ({ ...hits[0], start: (offset + index) * 6, end: (offset + index) * 6 + 5 }));
		get.mockResolvedValueOnce({ data: { conversationId: "c", transcriptRevision: "r", occurrences: pageHits(0), total: 1000000, offset: 0, initialIndex: 199, nextCursor: "r:200", lastCursor: "r:999800" }, response: { status: 200 } });
		get.mockResolvedValueOnce({ data: { conversationId: "c", transcriptRevision: "r", occurrences: pageHits(200), total: 1000000, offset: 200, initialIndex: 200, previousCursor: "r:0", nextCursor: "r:400", lastCursor: "r:999800" }, response: { status: 200 } });
		render(<Harness />); await openAndSearch();
		expect(screen.getByText("200 of 1000000")).toBeInTheDocument(); expect(get).toHaveBeenCalledTimes(1);
		fireEvent.keyDown(screen.getByRole("searchbox"), { key: "Enter" }); await tick(0);
		expect(screen.getByText("201 of 1000000")).toBeInTheDocument(); expect(get).toHaveBeenCalledTimes(2); expect(get.mock.calls[1][1].params.query.cursor).toBe("r:200");
	});
});
