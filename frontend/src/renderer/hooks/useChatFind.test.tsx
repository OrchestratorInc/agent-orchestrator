import { useRef } from "react";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useChatFind } from "./useChatFind";
import type { ConversationMessage, ConversationSnapshot } from "../types/conversation";
import { useKeybindingsStore } from "../stores/keybindings-store";
import { aoBridge } from "../lib/bridge";
import { ChatMarkdown } from "../components/chat/ChatMarkdown";

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

function Harness({ snapshot = source, markdown = false }: { snapshot?: ConversationSnapshot; markdown?: boolean }) {
	const root = useRef<HTMLDivElement>(null), viewport = useRef<HTMLDivElement>(null);
	const find = useChatFind({ source: snapshot, root, viewport, reveal, pauseFollow: pause });
	return <section data-chat-find-scope="" onKeyDown={(event) => { if (event.key === "Escape") interrupt(); }}>
		<div ref={root} data-chat-find-surface="">{find.bar}<div ref={viewport} tabIndex={-1}>{find.snapshot.items.map((item) => <div key={item.id} data-find-message-id={item.id}>{markdown && item.kind === "message" && item.role === "assistant" ? <div data-chat-message-body=""><ChatMarkdown text={item.text} streaming={item.streaming} /></div> : <p data-chat-message-body="">{item.kind === "message" ? item.text : ""}</p>}</div>)}</div></div>
		<textarea aria-label="Composer" defaultValue="draft" onKeyDown={(event) => { if (event.key === "Enter") send(); }} />
	</section>;
}

let openFind: ((value: string, anchorMessageId?: string) => void) | undefined;
function OpenWithHarness() {
	const root = useRef<HTMLDivElement>(null), viewport = useRef<HTMLDivElement>(null);
	const find = useChatFind({ source, root, viewport, reveal, pauseFollow: pause });
	openFind = find.openWith;
	return <section data-chat-find-scope=""><div ref={root} data-chat-find-surface="">{find.bar}<div ref={viewport} tabIndex={-1}>{find.snapshot.items.map((item) => <div key={item.id} data-find-message-id={item.id}><p data-chat-message-body="">{item.kind === "message" ? item.text : ""}</p></div>)}</div></div></section>;
}

async function tick(ms = 180) { await act(async () => { await vi.advanceTimersByTimeAsync(ms); }); }
async function openAndSearch() {
	fireEvent.keyDown(document.activeElement ?? document.body, { key: "f", ctrlKey: true }); await tick(20);
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

	it("opens searching for a session search result's word, from the result's message", async () => {
		get.mockResolvedValue({ data: { conversationId: "c", transcriptRevision: "r", occurrences: hits, total: 2, offset: 0, initialIndex: 1 }, response: { status: 200 } });
		render(<OpenWithHarness />);
		act(() => openFind?.("hello", "m")); await tick(20); await tick();
		expect(screen.getByRole("searchbox")).toHaveValue("hello");
		expect(get.mock.calls[0][1].params.query).toMatchObject({ query: "hello", anchorMessageId: "m", anchorBlock: 0, anchorOffset: 0, fallbackToPrevious: false });
		expect(screen.getByText("2/2")).toBeInTheDocument();
		expect(reveal).toHaveBeenLastCalledWith(hits[1]);
		// Another result for the same word searches again from its own message
		// rather than keeping the active match.
		const calls = get.mock.calls.length;
		act(() => openFind?.("hello", "other")); await tick();
		expect(get.mock.calls.length).toBeGreaterThan(calls);
		expect(get.mock.calls.at(-1)?.[1].params.query).toMatchObject({ query: "hello", anchorMessageId: "other", fallbackToPrevious: false });
	});

	it("reveals and highlights a daemon occurrence after a rendered hard break without timing out", async () => {
		const registry = new Map<string, Set<Range>>();
		vi.stubGlobal("CSS", { highlights: registry });
		vi.stubGlobal("Highlight", class extends Set<Range> {});
		try {
			const occurrence = { ...hits[0], start: 6, end: 12 };
			get.mockResolvedValue({ data: { conversationId: "c", transcriptRevision: "break", occurrences: [occurrence], total: 1 }, response: { status: 200 } });
			const message: ConversationMessage = { ...source.items[0] as ConversationMessage, role: "assistant", origin: "provider", text: "hello  \nneedle" };
			render(<Harness markdown snapshot={{ ...source, items: [message] }} />);
			fireEvent.keyDown(document.body, { key: "f", ctrlKey: true });
			await tick(20);
			fireEvent.change(screen.getByRole("searchbox"), { target: { value: "needle" } });
			await tick(3500);
			expect(screen.getByText("1/1")).toBeInTheDocument();
			expect(screen.queryByRole("alert")).not.toBeInTheDocument();
			expect(reveal).toHaveBeenCalledWith(occurrence);
			expect([...registry.get("chat-find-active") ?? []].map((range) => range.toString())).toEqual(["needle"]);
			expect(HTMLElement.prototype.scrollTo).toHaveBeenCalled();
		} finally { cleanup(); vi.unstubAllGlobals(); }
	});

	it("finds both occurrences and navigates, wraps, closes and preserves the draft", async () => {
		render(<Harness />); const composer = screen.getByRole("textbox", { name: "Composer" }); composer.focus();
		await openAndSearch();
		expect(screen.getByText("1/2")).toBeInTheDocument();
		expect(screen.getByText("1 of 2", { selector: ".sr-only" })).toBeInTheDocument();
		const input = screen.getByRole("searchbox");
		fireEvent.keyDown(input, { key: "Enter" }); await tick(0); expect(screen.getByText("2/2")).toBeInTheDocument();
		fireEvent.keyDown(input, { key: "Enter" }); await tick(0); expect(screen.getByText("Wrapped to the first match.")).toBeInTheDocument();
		fireEvent.keyDown(input, { key: "Enter", shiftKey: true }); await tick(0); expect(screen.getByText("2/2")).toBeInTheDocument();
		fireEvent.keyDown(input, { key: "Escape" });
		expect(screen.queryByRole("searchbox")).not.toBeInTheDocument(); expect(composer).toHaveValue("draft"); expect(composer).toHaveFocus();
		expect(send).not.toHaveBeenCalled(); expect(interrupt).not.toHaveBeenCalled(); expect(pause).toHaveBeenCalled();
		fireEvent.keyDown(document.activeElement ?? document.body, { key: "f", ctrlKey: true }); expect(screen.getByRole("searchbox")).toHaveValue("hello");
	});

	it("debounces typing and waits for IME composition", async () => {
		render(<Harness />); fireEvent.keyDown(document.activeElement ?? document.body, { key: "f", ctrlKey: true });
		const input = screen.getByRole("searchbox"); fireEvent.compositionStart(input);
		fireEvent.change(input, { target: { value: "hello" } }); await tick(500); expect(get).not.toHaveBeenCalled();
		fireEvent.keyDown(input, { key: "Enter", isComposing: true }); expect(reveal).not.toHaveBeenCalled();
		fireEvent.compositionEnd(input); await tick(100); expect(get).not.toHaveBeenCalled(); await tick(60); expect(get).toHaveBeenCalledTimes(1);
	});

	it("distinguishes an error from no matches and supports retry", async () => {
		get.mockRejectedValueOnce(new Error("Offline")); render(<Harness />); await openAndSearch();
		expect(screen.getByText("Search incomplete")).toBeInTheDocument(); expect(screen.queryByText("No results")).not.toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Retry" })); await tick(); expect(screen.getByText("1/2")).toBeInTheDocument();
	});

	it("does not accept a late response for an old query", async () => {
		let resolve: (value: unknown) => void = () => {};
		get.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
		render(<Harness />); await openAndSearch();
		fireEvent.change(screen.getByRole("searchbox"), { target: { value: "new" } }); await tick();
		await act(async () => resolve({ data: { conversationId: "c", transcriptRevision: "old", occurrences: [], total: 0 }, response: { status: 200 } }));
		expect(screen.queryByText("No results")).not.toBeInTheDocument(); expect(screen.getByText("1/2")).toBeInTheDocument();
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
		expect(screen.queryByRole("searchbox")).not.toBeInTheDocument(); fireEvent.keyDown(document.activeElement ?? document.body, { key: "f", ctrlKey: true }); expect(screen.getByRole("searchbox")).toHaveValue("");
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
		const before = get.mock.calls.length;
		view.rerender(<Harness snapshot={{ ...source, items: [...source.items, streaming] }} />); await tick();
		expect(get).toHaveBeenCalledTimes(before); await tick(2000);
		expect(screen.getByText("1/3")).toBeInTheDocument(); expect(reveal).toHaveBeenCalledTimes(1);
		get.mockResolvedValueOnce({ data: { conversationId: "c", transcriptRevision: "stream2", occurrences: [...hits, ...hits.map((hit) => ({ ...hit, messageId: "stream", sequence: 11, revision: 2 }))], total: 4 }, response: { status: 200 } });
		view.rerender(<Harness snapshot={{ ...source, items: [...source.items, { ...streaming, text: "hello hello", revision: 2 }] }} />); await tick(2000);
		expect(screen.getByText("1/4")).toBeInTheDocument(); expect(reveal).toHaveBeenCalledTimes(1);
	});

	it("validates an over-limit query without truncating it", async () => {
		render(<Harness />); fireEvent.keyDown(document.activeElement ?? document.body, { key: "f", ctrlKey: true });
		const value = "x".repeat(513); fireEvent.change(screen.getByRole("searchbox"), { target: { value } }); await tick();
		expect(screen.getByRole("searchbox")).toHaveValue(value); expect(screen.getByText(/up to 512 characters/)).toBeInTheDocument(); expect(get).not.toHaveBeenCalled();
	});

	it("keeps the query open and refreshes after a branch switch", async () => {
		const view = render(<Harness />); await openAndSearch();
		view.rerender(<Harness snapshot={{ ...source, activeBranchId: "new-branch" }} />);
		expect(screen.getByRole("searchbox")).toHaveValue("hello"); await tick(); expect(screen.getByText("1/2")).toBeInTheDocument();
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
		expect(screen.getByText("200/1000000")).toBeInTheDocument(); expect(get).toHaveBeenCalledTimes(1);
		fireEvent.keyDown(screen.getByRole("searchbox"), { key: "Enter" }); await tick(0);
		expect(screen.getByText("201/1000000")).toBeInTheDocument(); expect(get).toHaveBeenCalledTimes(2); expect(get.mock.calls[1][1].params.query.cursor).toBe("r:200");
	});

	it("has no launch button and applies reassignment, alternatives and reset without remounting", async () => {
		render(<Harness />);
		expect(screen.queryByRole("button", { name: "Find in chat" })).not.toBeInTheDocument();
		const binding = { key: "j", ctrl: true, meta: false, shift: true, alt: false };
		const persist = vi.spyOn(aoBridge.keybindings, "set").mockImplementation(async (value) => value);
		await act(async () => { await useKeybindingsStore.getState().setBindings("find-in-chat", [binding]); });
		fireEvent.keyDown(document.body, { key: "f", ctrlKey: true }); expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
		fireEvent.keyDown(document.body, { key: "J", ctrlKey: true, shiftKey: true }); await tick(20);
		expect(screen.getByRole("searchbox")).toHaveFocus();
		fireEvent.keyDown(screen.getByRole("searchbox"), { key: "Escape" });
		await act(async () => { await useKeybindingsStore.getState().setBindings("find-in-chat", [binding, { ...binding, key: "f", shift: false }]); });
		fireEvent.keyDown(document.body, { key: "f", ctrlKey: true }); await tick(20); expect(screen.getByRole("searchbox")).toHaveFocus();
		fireEvent.keyDown(screen.getByRole("searchbox"), { key: "Escape" });
		await act(async () => { await useKeybindingsStore.getState().resetBinding("find-in-chat"); });
		fireEvent.keyDown(document.body, { key: "J", ctrlKey: true, shiftKey: true }); expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
		fireEvent.keyDown(document.body, { key: "f", ctrlKey: true }); await tick(20); expect(screen.getByRole("searchbox")).toHaveFocus();
		expect(persist).toHaveBeenLastCalledWith({});
	});

	it("does no search work while chatting and retains browser highlight objects until reactivated", async () => {
		const registry = new Map<string, unknown>(); const set = vi.spyOn(registry, "set");
		vi.stubGlobal("CSS", { highlights: registry }); vi.stubGlobal("Highlight", class extends Set<Range> {});
		try {
			const view = render(<Harness />); await openAndSearch();
			const existing = registry.get("chat-find-matches"), paints = set.mock.calls.length;
			expect(existing).toBeDefined();
			act(() => screen.getByRole("textbox", { name: "Composer" }).focus());
			await tick(0);
			expect(vi.getTimerCount()).toBe(0);
			fireEvent.pointerDown(screen.getByText("1/2")); fireEvent.click(screen.getByText("1/2"));
			const added = { ...source.items[0], id: "new", sequence: 11 };
			view.rerender(<Harness snapshot={{ ...source, items: [...source.items, added] }} />); await tick(1000);
			expect(get).toHaveBeenCalledTimes(1); expect(set).toHaveBeenCalledTimes(paints);
			expect(registry.get("chat-find-matches")).toBe(existing); expect(screen.getByText("1/2")).toBeInTheDocument();
			get.mockResolvedValueOnce({ data: { conversationId: "c", transcriptRevision: "new", occurrences: [...hits, { ...hits[0], messageId: "new", sequence: 11 }], total: 3 }, response: { status: 200 } });
			act(() => screen.getByRole("searchbox").focus()); await tick();
			expect(get).toHaveBeenCalledTimes(2); expect(screen.getByText("1/3")).toBeInTheDocument();
			expect(registry.get("chat-find-matches")).not.toBe(existing);
			fireEvent.keyDown(screen.getByRole("searchbox"), { key: "Escape" }); expect(registry.size).toBe(0);
		} finally { cleanup(); vi.unstubAllGlobals(); }
	});

	it("cancels and ignores an in-flight search on blur, then shows a loader only for a new query", async () => {
		let resolve: (value: unknown) => void = () => {};
		get.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
		render(<Harness />); await openAndSearch();
		const status = screen.getAllByRole("status").find((element) => element.querySelector(".animate-spin"))!;
		expect(status.querySelector("svg")).toHaveClass("size-4", "shrink-0", "motion-reduce:animate-none");
		expect(screen.getByText("Searching…")).toHaveClass("sr-only");
		act(() => screen.getByRole("textbox", { name: "Composer" }).focus());
		expect(get.mock.calls[0][1].signal.aborted).toBe(true); expect(status.querySelector(".animate-spin")).toBeNull();
		expect(screen.getByRole("search")).toHaveAttribute("aria-busy", "false");
		await act(async () => resolve({ data: { conversationId: "c", transcriptRevision: "late", occurrences: hits, total: 2 }, response: { status: 200 } }));
		expect(screen.queryByText("1/2")).not.toBeInTheDocument(); expect(reveal).not.toHaveBeenCalled();
		fireEvent.keyDown(screen.getByRole("textbox"), { key: "f", ctrlKey: true }); await tick();
		expect(screen.getByText("1/2")).toBeInTheDocument(); expect(get).toHaveBeenCalledTimes(2);
	});

	it("keeps the completed count during refresh and waits for it before navigating", async () => {
		render(<Harness />); await openAndSearch();
		act(() => screen.getByRole("textbox", { name: "Composer" }).focus());
		let resolve: (value: unknown) => void = () => {};
		get.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
		fireEvent.click(screen.getByRole("button", { name: "Next match" })); await tick();
		expect(screen.getByText("1/2")).toBeInTheDocument(); expect(reveal).toHaveBeenCalledTimes(1);
		expect(screen.queryByText(/updating|Loading matches/i)).not.toBeInTheDocument(); expect(document.querySelector(".animate-spin")).toBeNull();
		await act(async () => resolve({ data: { conversationId: "c", transcriptRevision: "new", occurrences: [...hits, { ...hits[0], start: 12, end: 17 }], total: 3 }, response: { status: 200 } })); await tick(0);
		expect(screen.getByText("2/3")).toBeInTheDocument(); expect(reveal).toHaveBeenLastCalledWith(hits[1]);
		expect(get).toHaveBeenCalledTimes(2);
	});

	it("treats focus between controls as active and stops when the window is blurred", async () => {
		const view = render(<Harness />); await openAndSearch();
		act(() => screen.getByRole("button", { name: "Next match" }).focus());
		view.rerender(<Harness snapshot={{ ...source }} />); await tick(); expect(get).toHaveBeenCalledTimes(2);
		fireEvent.blur(window); expect(vi.getTimerCount()).toBe(0);
		view.rerender(<Harness snapshot={{ ...source }} />); await tick(500); expect(get).toHaveBeenCalledTimes(2);
		fireEvent.pointerDown(screen.getByRole("searchbox")); await tick(); expect(get).toHaveBeenCalledTimes(3);
	});

	it("invalidates a paused branch without searching and refreshes on the next activation", async () => {
		const view = render(<Harness />); await openAndSearch();
		act(() => screen.getByRole("textbox", { name: "Composer" }).focus());
		view.rerender(<Harness snapshot={{ ...source, activeBranchId: "other" }} />); await tick(500);
		expect(screen.getByRole("searchbox")).toHaveValue("hello"); expect(get).toHaveBeenCalledTimes(1);
		expect(screen.queryByText("1/2")).not.toBeInTheDocument();
		act(() => screen.getByRole("searchbox").focus()); await tick(); expect(get).toHaveBeenCalledTimes(2);
	});

	it("ignores a late history-window response after the reader returns to the composer", async () => {
		let resolve: (value: unknown) => void = () => {};
		const old = { ...hits[0], messageId: "old", sequence: 1 };
		get.mockResolvedValueOnce({ data: { conversationId: "c", transcriptRevision: "r", occurrences: [old], total: 1 }, response: { status: 200 } });
		get.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
		render(<Harness />); await openAndSearch(); expect(get.mock.calls[1][0]).toContain("find-window");
		act(() => screen.getByRole("textbox", { name: "Composer" }).focus());
		expect(get.mock.calls[1][1].signal.aborted).toBe(true);
		await act(async () => resolve({ data: { ...source, items: [{ ...source.items[0], id: "old", sequence: 1 }] }, response: { status: 200 } }));
		expect(document.querySelector('[data-find-message-id="old"]')).toBeNull(); expect(reveal).not.toHaveBeenCalled();
	});

	it("cancels an occurrence page and queued navigation when focus leaves the bar", async () => {
		let resolve: (value: unknown) => void = () => {};
		get.mockResolvedValueOnce({ data: { conversationId: "c", transcriptRevision: "r", occurrences: hits, total: 3, initialIndex: 1, nextCursor: "r:2" }, response: { status: 200 } });
		get.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
		render(<Harness />); await openAndSearch();
		fireEvent.keyDown(screen.getByRole("searchbox"), { key: "Enter" });
		fireEvent.keyDown(screen.getByRole("searchbox"), { key: "Enter" }); await tick(0);
		expect(get).toHaveBeenCalledTimes(2);
		act(() => screen.getByRole("textbox", { name: "Composer" }).focus());
		await act(async () => resolve({ data: { conversationId: "c", transcriptRevision: "r", occurrences: [{ ...hits[0], start: 12, end: 17 }], total: 3, offset: 2 }, response: { status: 200 } })); await tick();
		expect(get.mock.calls[1][1].signal.aborted).toBe(true); expect(get).toHaveBeenCalledTimes(2);
		expect(screen.getByText("2/3")).toBeInTheDocument(); expect(reveal).toHaveBeenCalledTimes(1);
	});

	it("handles a missing focus destination and does not refresh merely on window focus", async () => {
		render(<Harness />); await openAndSearch();
		act(() => screen.getByRole("searchbox").blur()); await tick(0);
		expect(vi.getTimerCount()).toBe(0);
		fireEvent.focus(window); await tick(500); expect(get).toHaveBeenCalledTimes(1);
		fireEvent.keyDown(document.body, { key: "f", ctrlKey: true });
		fireEvent.keyDown(document.body, { key: "f", ctrlKey: true }); await tick();
		expect(get).toHaveBeenCalledTimes(2); expect(screen.getByRole("searchbox")).toHaveFocus();
	});
});
