import { act, render, screen, waitFor, within } from "@testing-library/react";
import { useEffect } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import type { ChatModel } from "../../types/conversation";
import type { components } from "../../../api/schema";
import { TooltipProvider } from "../ui/tooltip";
import { useIndependentSideChats } from "./IndependentSideChats";
import { emptySideChatDraft, type SideChatDraft } from "./sideChatDraft";
import { INLINE_ATTACHMENT_BYTES } from "../../lib/attachment-uploads";

const { get, post, put, patch, capture } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), patch: vi.fn(), capture: vi.fn() }));
vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: get, POST: post, PUT: put, PATCH: patch },
	apiErrorMessage: (error: unknown) => error instanceof Error ? error.message : "Request failed",
	getApiBaseUrl: () => "", subscribeApiBaseUrl: () => () => {},
}));
vi.mock("../../lib/bridge", () => ({ aoBridge: {
	daemon: { getStatus: async () => ({ state: "ready", pid: 1, port: 3002 }), onStatus: () => () => {} },
	sideChats: { protocolVersion: 1, mode: "desktop", capture,
		getCloseState: async (sessionId: string) => ({ protocolVersion: 1, epoch: "daemon", revision: 0, recovery: "ready", sessionId, sides: [] }),
		onCloseStateChanged: () => () => {},
	},
} }));

type Snapshot = components["schemas"]["SideSnapshot"];
const at = "2026-11-15T00:00:00Z";
let snapshot: Snapshot;
let draft: SideChatDraft;
let models: ChatModel[];
let chat: ReturnType<typeof useIndependentSideChats>;
function Harness() {
	chat = useIndependentSideChats("session", models, [], async () => [], true);
	useEffect(() => { chat.show("side"); }, []);
	return <TooltipProvider>{chat.panel}</TooltipProvider>;
}
beforeEach(() => {
	vi.resetAllMocks();
	HTMLElement.prototype.scrollTo = vi.fn();
	snapshot = { side: { id: "side", sessionId: "session", mainConversationId: "main", label: "Side", state: "draft", contextMode: "", hasWork: false, createdAt: at, updatedAt: at }, turns: [], messages: [], activities: [], hasMore: false };
	draft = emptySideChatDraft();
	models = [];
	get.mockImplementation(async (path: string) => {
		if (path.endsWith("/side-chats")) return { data: { sides: [snapshot.side] } };
		if (path.endsWith("/draft")) return { data: { contentJson: JSON.stringify(draft) } };
		return { data: { snapshot: structuredClone(snapshot) } };
	});
	put.mockImplementation(async (_path, options) => { draft = JSON.parse(options.body.contentJson); return {}; });
	capture.mockResolvedValue(undefined);
	post.mockResolvedValue({});
});
afterEach(() => { vi.unstubAllGlobals(); });

function acceptOpening(clientMessageId: string, state = "queued") {
	snapshot = {
		...snapshot, side: { ...snapshot.side, state: state === "queued" ? "opening" : "ready", openingTurnId: "opening", openingStartedAt: at },
		turns: [{ id: "opening", sideId: "side", clientMessageId, text: "My first question", state, createdAt: at }],
		messages: [{ id: "opening-user", sideId: "side", turnId: "opening", role: "user", text: "My first question", sequence: 1, revision: 1, streaming: false, createdAt: at, updatedAt: at }],
	};
}

describe("deferred side opening", () => {
	it.each(["draft-read", "image-load"])("settles a new follow-up bubble when %s fails before dispatch", async failure => {
		acceptOpening("first", "completed");
		let finish!: (value: unknown) => void;
		const normal = get.getMockImplementation()!;
		if (failure === "draft-read") get.mockImplementation((path, options) => path.endsWith("/draft") ? new Promise(resolve => { finish = resolve; }) : normal(path, options));
		else {
			draft = { ...emptySideChatDraft(), attachments: [{ id: "image", path: "image.png", name: "image.png", mimeType: "image/png", bytes: 3 }] };
			vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("Image unavailable")));
		}
		render(<Harness />);
		await screen.findByText("My first question");
		if (failure === "image-load") await screen.findByRole("button", { name: "Remove image.png" });
		act(() => { chat.setQuestionDraft("side", "Unsent follow-up"); });
		let sending!: Promise<void>;
		act(() => { sending = chat.send("side", "Unsent follow-up"); });
		await act(async () => {
			if (failure === "draft-read") finish({ error: new Error("Draft unavailable") });
			await sending;
		});
		expect(post).not.toHaveBeenCalled();
		expect(within(screen.getByRole("log")).queryByText("Unsent follow-up")).not.toBeInTheDocument();
		expect(screen.getByRole("combobox", { name: "Side chat question" })).toHaveTextContent("Unsent follow-up");
		if (failure === "draft-read") get.mockImplementation(normal);
		else await userEvent.click(screen.getByRole("button", { name: "Remove image.png" }));
		let accept!: (value: unknown) => void;
		post.mockImplementationOnce(() => new Promise(resolve => { accept = resolve; }));
		act(() => { chat.setQuestionDraft("side", "Corrected follow-up"); });
		act(() => { sending = chat.send("side", "Corrected follow-up"); });
		expect(await within(screen.getByRole("log")).findByText("Corrected follow-up")).toBeInTheDocument();
		await waitFor(() => expect(post).toHaveBeenCalledOnce());
		await act(async () => { accept({}); await sending; });
	});

	it.each([false, true])("keeps a frozen follow-up identity when recovery capture fails before dispatch (remount: %s)", async remount => {
		acceptOpening("first", "completed");
		const view = render(<Harness />);
		await screen.findByText("My first question");
		capture.mockRejectedValueOnce(new Error("Capture unavailable"));
		await act(async () => { await chat.send("side", "Frozen follow-up"); });
		expect(post).not.toHaveBeenCalled();
		const original = draft.pendingDelivery!.request;
		expect(within(screen.getByRole("log")).getByText("Frozen follow-up")).toBeInTheDocument();
		expect(within(screen.getByRole("log")).getByText("Not delivered")).toBeInTheDocument();
		if (remount) {
			view.unmount();
			render(<Harness />);
			expect(await within(await screen.findByRole("log")).findByText("Not delivered")).toBeInTheDocument();
		}
		await act(async () => { await chat.send("side", ""); });
		expect(post.mock.calls[0][1].body).toEqual(original);
		expect(within(screen.getByRole("log")).getAllByText("Frozen follow-up")).toHaveLength(1);
		expect(within(screen.getByRole("log")).queryByText("Not delivered")).not.toBeInTheDocument();
	});

	it("keeps an acknowledged question delivered when saving its cleared draft fails", async () => {
		acceptOpening("first", "completed");
		render(<Harness />);
		await screen.findByText("My first question");
		let savedReceipt = false;
		put.mockImplementation(async (_path, options) => {
			const next = JSON.parse(options.body.contentJson);
			if (next.pendingDelivery) savedReceipt = true;
			else if (savedReceipt) { savedReceipt = false; return { error: new Error("Draft save failed") }; }
			draft = next;
			return {};
		});
		post.mockImplementation(async (_path, options) => ({ data: { turn: { id: "acknowledged", sideId: "side", clientMessageId: options.body.clientMessageId, text: "Acknowledged question", state: "queued", createdAt: at } } }));
		await act(async () => { await chat.send("side", "Acknowledged question"); });
		const log = screen.getByRole("log");
		expect(within(log).getByText("Acknowledged question")).toBeInTheDocument();
		expect(within(log).queryByText("Delivery uncertain — not retried automatically")).not.toBeInTheDocument();
		expect(within(log).queryByText("Not delivered")).not.toBeInTheDocument();
		expect(post).toHaveBeenCalledOnce();
	});

	it.each(["New replacement draft", ""])("preserves manually edited replacement text while copying recovery (text: %s)", async text => {
		acceptOpening("first", "failed");
		snapshot.side = { ...snapshot.side, state: "failed", recreateRequired: true };
		const file = { id: "file", path: "notes.txt", name: "notes.txt", mimeType: "text/plain", bytes: 12 };
		const sourceDraft = { ...emptySideChatDraft(), openingDelivery: { clientMessageId: "first", request: { clientMessageId: "first", text: "My first question" }, submitted: { text: "My first question", attachments: [file], references: [] } } };
		let finish!: (value: unknown) => void;
		const replacement = { ...snapshot.side, id: "replacement", state: "draft", recreateRequired: false, openingTurnId: undefined };
		get.mockImplementation(async (path, options) => {
			if (path.endsWith("/side-chats")) return { data: { sides: [snapshot.side] } };
			if (options?.params?.path.sideId === "replacement") return path.endsWith("/draft") ? new Promise(resolve => { finish = resolve; }) : { data: { snapshot: { side: replacement, turns: [], messages: [], activities: [], hasMore: false } } };
			return path.endsWith("/draft") ? { data: { contentJson: JSON.stringify(sourceDraft) } } : { data: { snapshot } };
		});
		post.mockResolvedValue({ data: { side: replacement } });
		render(<Harness />);
		await userEvent.click(within(await screen.findByTestId("session-startup")).getByRole("button", { name: "New side chat" }));
		await waitFor(() => expect(chat.sides.some(side => side.id === "replacement")).toBe(true));
		act(() => { chat.show("replacement"); });
		const editor = await screen.findByRole("combobox", { name: "Side chat question" });
		act(() => { chat.setQuestionDraft("replacement", "New replacement draft"); });
		if (!text) act(() => { chat.setQuestionDraft("replacement", ""); });
		await waitFor(() => expect(editor.textContent).toBe(text));
		await act(async () => { finish({ data: { contentJson: "" } }); });
		await waitFor(() => expect(put.mock.calls.some(([, options]) => options.params.path.sideId === "replacement" && JSON.parse(options.body.contentJson).attachments[0]?.path === file.path)).toBe(true));
		expect(editor.textContent).toBe(text);
		expect(put.mock.calls.filter(([, options]) => options.params.path.sideId === "replacement").every(([, options]) => JSON.parse(options.body.contentJson).text === text)).toBe(true);
	});

	it("leaves a manually submitted replacement untouched by a late recovery copy", async () => {
		acceptOpening("first", "failed");
		snapshot.side = { ...snapshot.side, state: "failed", recreateRequired: true };
		const sourceDraft = { ...emptySideChatDraft(), openingDelivery: { clientMessageId: "first", request: { clientMessageId: "first", text: "Original source question" }, submitted: { text: "Original source question", attachments: [{ id: "file", path: "notes.txt", name: "notes.txt", mimeType: "text/plain", bytes: 12 }], references: [] } } };
		let finish!: (value: unknown) => void;
		const replacement = { ...snapshot.side, id: "replacement", state: "draft", recreateRequired: false, openingTurnId: undefined };
		get.mockImplementation(async (path, options) => {
			if (path.endsWith("/side-chats")) return { data: { sides: [snapshot.side] } };
			if (options?.params?.path.sideId === "replacement") return path.endsWith("/draft") ? new Promise(resolve => { finish = resolve; }) : { data: { snapshot: { side: replacement, turns: [], messages: [], activities: [], hasMore: false } } };
			return path.endsWith("/draft") ? { data: { contentJson: JSON.stringify(sourceDraft) } } : { data: { snapshot } };
		});
		post.mockImplementation(async path => path.endsWith("/side-chats") ? { data: { side: replacement } } : {});
		render(<Harness />);
		await userEvent.click(within(await screen.findByTestId("session-startup")).getByRole("button", { name: "New side chat" }));
		await waitFor(() => expect(chat.sides.some(side => side.id === "replacement")).toBe(true));
		act(() => { chat.show("replacement"); chat.setQuestionDraft("replacement", "My own replacement question"); });
		let sending!: Promise<void>;
		act(() => { sending = chat.send("replacement", "My own replacement question"); });
		await act(async () => { finish({ data: { contentJson: "" } }); await sending; });
		expect(post.mock.calls.find(([path]) => path.endsWith("/messages"))?.[1].body).toMatchObject({ text: "My own replacement question", resources: [] });
		expect(put.mock.calls.filter(([, options]) => options.params.path.sideId === "replacement").every(([, options]) => !JSON.parse(options.body.contentJson).attachments.length && JSON.parse(options.body.contentJson).text !== "Original source question")).toBe(true);
	});

	it("preserves an acknowledged destination even when its creation response still says draft", async () => {
		acceptOpening("first", "failed");
		snapshot.side = { ...snapshot.side, state: "failed", recreateRequired: true };
		const receipt = { clientMessageId: "first", request: { clientMessageId: "first", text: "Original source question" }, submitted: { text: "Original source question", attachments: [], references: [] } };
		const sourceDraft = { ...emptySideChatDraft(), openingDelivery: receipt };
		const destinationDraft = { ...emptySideChatDraft(), text: "Newer destination draft", openingDelivery: { ...receipt, clientMessageId: "destination-first", request: { clientMessageId: "destination-first", text: "Already submitted there" } } };
		const replacement = { ...snapshot.side, id: "replacement", state: "draft", recreateRequired: false, openingTurnId: undefined };
		get.mockImplementation(async (path, options) => {
			if (path.endsWith("/side-chats")) return { data: { sides: [snapshot.side] } };
			if (path.endsWith("/draft")) return { data: { contentJson: JSON.stringify(options?.params?.path.sideId === "replacement" ? destinationDraft : sourceDraft) } };
			return { data: { snapshot } };
		});
		post.mockResolvedValue({ data: { side: replacement } });
		render(<Harness />);
		await userEvent.click(within(await screen.findByTestId("session-startup")).getByRole("button", { name: "New side chat" }));
		await waitFor(() => expect(get.mock.calls.some(([path, options]) => path.endsWith("/draft") && options.params.path.sideId === "replacement")).toBe(true));
		await act(async () => { await Promise.resolve(); });
		expect(put.mock.calls.filter(([, options]) => options.params.path.sideId === "replacement").every(([, options]) => JSON.parse(options.body.contentJson).text === "Newer destination draft")).toBe(true);
	});
	it("hydrates saved context before Send, blocks early autosaves, and preserves edits after Send", async () => {
		const file = { id: "notes", path: ".ao/attachments/notes.txt", name: "notes.txt", mimeType: "text/plain", bytes: 12 };
		const ref = { id: "excerpt", conversationId: "main", messageId: "message", revision: 1, text: "Saved excerpt", role: "assistant" as const };
		const saved = { ...emptySideChatDraft(), text: "Saved question", attachments: [file], references: [ref] };
		let finish!: (value: unknown) => void;
		const originalGet = get.getMockImplementation()!;
		get.mockImplementation((path, options) => path.endsWith("/draft") ? new Promise(resolve => { finish = resolve; }) : originalGet(path, options));
		render(<Harness />);
		await screen.findByRole("combobox", { name: "Side chat question" });
		act(() => { chat.setQuestionDraft("side", "Question at Send"); });
		await act(async () => { await new Promise(resolve => setTimeout(resolve, 400)); });
		expect(put).not.toHaveBeenCalled();
		let sending!: Promise<void>;
		act(() => { sending = chat.send("side", "Question at Send"); });
		act(() => { chat.setQuestionDraft("side", "Next question"); });
		expect(post).not.toHaveBeenCalled();
		await act(async () => { finish({ data: { contentJson: JSON.stringify(saved) } }); await sending; });
		expect(post).toHaveBeenCalledOnce();
		expect(post.mock.calls[0][1].body).toMatchObject({ text: "Question at Send", resources: [{ uri: file.path }], references: [{ messageId: ref.messageId, text: ref.text }] });
		expect(screen.getByRole("combobox", { name: "Side chat question" })).toHaveTextContent("Next question");
		expect(get.mock.calls.filter(([path]) => path.endsWith("/draft"))).toHaveLength(1);
	});

	it("retries a failed draft read without overwriting saved context or newer local edits", async () => {
		let finish!: (value: unknown) => void;
		const originalGet = get.getMockImplementation()!;
		let firstRead = true;
		get.mockImplementation((path, options) => {
			if (path.endsWith("/draft") && firstRead) { firstRead = false; return new Promise(resolve => { finish = resolve; }); }
			return originalGet(path, options);
		});
		draft = { ...emptySideChatDraft(), attachments: [{ id: "file", path: "notes.txt", name: "notes.txt", mimeType: "text/plain", bytes: 12 }] };
		render(<Harness />);
		await screen.findByRole("combobox", { name: "Side chat question" });
		act(() => { chat.setQuestionDraft("side", "My first question"); });
		let sending!: Promise<void>;
		act(() => { sending = chat.send("side", "My first question"); });
		await act(async () => { finish({ error: new Error("Draft unavailable") }); await sending; });
		expect(post).not.toHaveBeenCalled();
		expect(put).not.toHaveBeenCalled();
		act(() => { chat.setQuestionDraft("side", "Newer local question"); });
		await act(async () => { await chat.send("side", "My first question"); });
		expect(post.mock.calls[0][1].body.resources).toEqual([{ uri: "notes.txt", name: "notes.txt" }]);
		expect(screen.getByRole("combobox", { name: "Side chat question" })).toHaveTextContent("Newer local question");
	});

	it("offers Retry loading after a draft read fails, without sending a question", async () => {
		draft = { ...emptySideChatDraft(), attachments: [{ id: "file", path: "notes.txt", name: "notes.txt", mimeType: "text/plain", bytes: 12 }] };
		const originalGet = get.getMockImplementation()!;
		let firstRead = true;
		get.mockImplementation((path, options) => {
			if (path.endsWith("/draft") && firstRead) { firstRead = false; return Promise.resolve({ error: new Error("Draft unavailable") }); }
			return originalGet(path, options);
		});
		render(<Harness />);
		await userEvent.click(await screen.findByRole("button", { name: "Retry loading" }));
		expect(await screen.findByRole("button", { name: "Remove notes.txt" })).toBeInTheDocument();
		expect(post).not.toHaveBeenCalled();
	});

	it("removes only the rejected follow-up bubble and shows the corrected question immediately", async () => {
		acceptOpening("first", "completed");
		render(<Harness />);
		await screen.findByText("My first question");
		act(() => { chat.setQuestionDraft("side", "Rejected follow-up"); });
		post.mockResolvedValueOnce({ error: { code: "CHAT_SIDE_INVALID_REQUEST", message: "Invalid reference" }, response: { status: 400 } });
		await act(async () => { await chat.send("side", "Rejected follow-up"); });
		expect(within(screen.getByRole("log")).queryByText("Rejected follow-up")).not.toBeInTheDocument();
		expect(screen.getByRole("combobox", { name: "Side chat question" })).toHaveTextContent("Rejected follow-up");
		let finish!: (value: unknown) => void;
		post.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
		act(() => { chat.setQuestionDraft("side", "Corrected follow-up"); });
		let sending!: Promise<void>;
		act(() => { sending = chat.send("side", "Corrected follow-up"); });
		expect(await within(screen.getByRole("log")).findByText("Corrected follow-up")).toBeInTheDocument();
		expect(within(screen.getByRole("log")).getByText("My first question")).toBeInTheDocument();
		await act(async () => { finish({}); await sending; });
	});

	it.each([408, 429, 500, "CHAT_SIDE_IDEMPOTENCY_CONFLICT"])("retains uncertain follow-up identity after %s", async status => {
		acceptOpening("first", "completed");
		render(<Harness />);
		await screen.findByText("My first question");
		post.mockResolvedValueOnce({ error: { code: typeof status === "string" ? status : "ERROR" }, response: { status: typeof status === "number" ? status : 409 } });
		await act(async () => { await chat.send("side", "Unconfirmed question"); });
		const original = post.mock.calls[0][1].body;
		expect(draft.pendingDelivery?.failure?.kind).toBe("unconfirmed");
		expect(within(screen.getByRole("log")).getByText("Unconfirmed question")).toBeInTheDocument();
		await act(async () => { await chat.send("side", ""); });
		expect(post.mock.calls[1][1].body).toEqual(original);
	});

	it.each([false, true])("reconciles a conflicting receipt with an existing daemon turn before releasing it (opening: %s)", async opening => {
		if (!opening) acceptOpening("first", "completed");
		render(<Harness />);
		await screen.findByRole("combobox", { name: "Side chat question" });
		act(() => { chat.setQuestionDraft("side", "Keep my edited question"); });
		post.mockImplementationOnce(async (_path, options) => {
			snapshot = { ...snapshot, side: { ...snapshot.side, state: "ready", openingTurnId: opening ? "existing" : "opening" }, turns: [...snapshot.turns, { id: "existing", sideId: "side", clientMessageId: options.body.clientMessageId, text: "Previously accepted question", state: "completed", createdAt: at }], messages: [...snapshot.messages, { id: "existing-user", sideId: "side", turnId: "existing", role: "user", text: "Previously accepted question", sequence: 2, revision: 1, streaming: false, createdAt: at, updatedAt: at }] };
			return { error: { code: "CHAT_SIDE_IDEMPOTENCY_CONFLICT" }, response: { status: 409 } };
		});
		await act(async () => { await chat.send("side", "Keep my edited question"); });
		expect(draft.pendingDelivery).toBeUndefined();
		expect(await within(screen.getByRole("log")).findByText("Previously accepted question")).toBeInTheDocument();
		expect(within(screen.getByRole("log")).queryByText("Keep my edited question")).not.toBeInTheDocument();
		expect(screen.getByRole("combobox", { name: "Side chat question" })).toHaveTextContent("Keep my edited question");
	});

	it.each(["rejected", "unconfirmed", "legacy"] as const)("restores an actionable %s opening after a remount without dispatch", async kind => {
		const first = render(<Harness />);
		await screen.findByRole("combobox", { name: "Side chat question" });
		if (kind === "rejected") post.mockResolvedValueOnce({ error: new Error("Invalid opening"), response: { status: 400 } });
		else post.mockRejectedValueOnce(new Error("Connection lost"));
		await act(async () => { await chat.send("side", "My first question"); });
		if (kind === "legacy") { delete draft.pendingDelivery!.failure; delete draft.pendingDelivery!.recreateRequired; }
		else expect(draft.pendingDelivery?.failure?.kind).toBe(kind);
		first.unmount();
		const saved = JSON.stringify(draft);
		render(<Harness />);
		expect(await screen.findByTestId("session-startup")).toHaveAttribute("data-failed", "true");
		expect(screen.queryByTestId("lattice-loader")).not.toBeInTheDocument();
		expect(within(screen.getByTestId("session-startup")).getByRole("button", { name: kind === "rejected" ? "New side chat" : "Retry start" })).toBeInTheDocument();
		expect(post).toHaveBeenCalledOnce();
		expect(JSON.stringify(draft)).toBe(saved);
	});

	it("lets an accepted daemon turn override an older saved delivery failure", async () => {
		acceptOpening("first", "running");
		draft = { ...emptySideChatDraft(), pendingDelivery: { clientMessageId: "first", recreateRequired: true, failure: { kind: "rejected", message: "Stale error" }, request: { clientMessageId: "first", text: "My first question" }, submitted: { text: "My first question", attachments: [], references: [] } } };
		render(<Harness />);
		await screen.findByText("My first question");
		expect(screen.queryByTestId("session-startup")).not.toBeInTheDocument();
		expect(post).not.toHaveBeenCalled();
	});

	it("waits for the source draft before recreation and prevents repeated creation", async () => {
		acceptOpening("first", "failed");
		snapshot.side = { ...snapshot.side, state: "failed", recreateRequired: true };
		const file = { id: "file", path: "notes.txt", name: "notes.txt", mimeType: "text/plain", bytes: 12 };
		const saved = { ...emptySideChatDraft(), text: "Newer source draft", openingDelivery: { clientMessageId: "first", request: { clientMessageId: "first", text: "My first question" }, submitted: { text: "My first question", attachments: [file], references: [] } } };
		let finish!: (value: unknown) => void;
		const originalGet = get.getMockImplementation()!;
		get.mockImplementation((path, options) => {
			if (options?.params?.path.sideId === "replacement") return Promise.resolve(path.endsWith("/draft") ? { data: { contentJson: "" } } : { data: { snapshot: { ...snapshot, side: { ...snapshot.side, id: "replacement", state: "draft" }, turns: [], messages: [] } } });
			return path.endsWith("/draft") ? new Promise(resolve => { finish = resolve; }) : originalGet(path, options);
		});
		post.mockResolvedValue({ data: { side: { ...snapshot.side, id: "replacement", state: "draft" } } });
		render(<Harness />);
		const button = within(await screen.findByTestId("session-startup")).getByRole("button", { name: "New side chat" });
		await userEvent.dblClick(button);
		expect(post).not.toHaveBeenCalled();
		await act(async () => { finish({ data: { contentJson: JSON.stringify(saved) } }); });
		await waitFor(() => expect(put.mock.calls.some(([, options]) => options.params.path.sideId === "replacement" && JSON.parse(options.body.contentJson).text === "My first question" && JSON.parse(options.body.contentJson).attachments[0]?.path === file.path)).toBe(true));
		expect(post).toHaveBeenCalledOnce();
	});

	it("does not dispatch a waiting Send after the view is disposed", async () => {
		let finish!: (value: unknown) => void;
		const originalGet = get.getMockImplementation()!;
		get.mockImplementation((path, options) => path.endsWith("/draft") ? new Promise(resolve => { finish = resolve; }) : originalGet(path, options));
		const view = render(<Harness />);
		await screen.findByRole("combobox", { name: "Side chat question" });
		let sending!: Promise<void>;
		act(() => { sending = chat.send("side", "Do not dispatch"); });
		view.unmount();
		await act(async () => { finish({ data: { contentJson: "" } }); await sending; });
		expect(post).not.toHaveBeenCalled();
		expect(put).not.toHaveBeenCalled();
	});

	it("keeps recreation available after a draft read failure instead of creating an empty replacement", async () => {
		acceptOpening("first", "failed");
		snapshot.side = { ...snapshot.side, state: "failed", recreateRequired: true };
		const originalGet = get.getMockImplementation()!;
		get.mockImplementation((path, options) => path.endsWith("/draft") ? Promise.resolve({ error: new Error("Cannot load saved question") }) : originalGet(path, options));
		render(<Harness />);
		const startup = await screen.findByTestId("session-startup");
		await userEvent.click(within(startup).getByRole("button", { name: "New side chat" }));
		await waitFor(() => expect(within(startup).getByRole("button", { name: "New side chat" })).toBeEnabled());
		expect(post).not.toHaveBeenCalled();
		expect(screen.getAllByRole("alert").some(alert => alert.textContent?.includes("Cannot load saved question"))).toBe(true);
	});

	it("recreates a loaded question without stealing focus after a tab switch during the read", async () => {
		acceptOpening("first", "failed");
		snapshot.side = { ...snapshot.side, state: "failed", recreateRequired: true };
		const saved = { ...emptySideChatDraft(), openingDelivery: { clientMessageId: "first", request: { clientMessageId: "first", text: "My first question" }, submitted: { text: "My first question", attachments: [], references: [] } } };
		let finish!: (value: unknown) => void;
		const originalGet = get.getMockImplementation()!;
		get.mockImplementation((path, options) => {
			if (path.endsWith("/side-chats")) return Promise.resolve({ data: { sides: [snapshot.side, { ...snapshot.side, id: "other", state: "draft", label: "Other" }] } });
			if (options?.params?.path.sideId === "side" && path.endsWith("/draft")) return new Promise(resolve => { finish = resolve; });
			if (path.endsWith("/draft")) return Promise.resolve({ data: { contentJson: "" } });
			return originalGet(path, options);
		});
		post.mockResolvedValue({ data: { side: { ...snapshot.side, id: "replacement", state: "draft" } } });
		render(<Harness />);
		await userEvent.click(within(await screen.findByTestId("session-startup")).getByRole("button", { name: "New side chat" }));
		act(() => { chat.show("other"); });
		await act(async () => { finish({ data: { contentJson: JSON.stringify(saved) } }); });
		await waitFor(() => expect(put.mock.calls.some(([, options]) => options.params.path.sideId === "replacement" && JSON.parse(options.body.contentJson).text === "My first question")).toBe(true));
		expect(chat.activeId).toBe("other");
	});
	it.each([false, true])("waits for the selected model and refuses a failed settings write: %s", async failed => {
		models = [{ id: "first", displayName: "First", default: true }, { id: "second", displayName: "Second", default: false }];
		snapshot.side.model = "first";
		render(<Harness />);
		const user = userEvent.setup();
		let finish!: (result: { error?: unknown }) => void;
		patch.mockImplementation(() => new Promise(resolve => { finish = resolve; }));
		await user.click(await screen.findByRole("button", { name: "Model and reasoning effort for the next turn" }));
		await user.keyboard("{ArrowDown}{ArrowRight}");
		await user.click(screen.getByRole("menuitemradio", { name: "Second" }));
		await waitFor(() => expect(patch).toHaveBeenCalledOnce());
		let sending!: Promise<void>;
		act(() => { sending = chat.send("side", "My first question"); });
		expect(post).not.toHaveBeenCalled();
		await act(async () => { finish(failed ? { error: new Error("Model unavailable") } : {}); await sending; });
		expect(post).toHaveBeenCalledTimes(failed ? 0 : 1);
		if (failed) {
			expect(screen.getByTestId("session-startup")).toHaveAttribute("data-failed", "true");
			act(() => { chat.setQuestionDraft("side", "Keep my newer draft"); });
			patch.mockResolvedValue({});
			await user.click(screen.getByRole("button", { name: "Model and reasoning effort for the next turn" }));
			await user.keyboard("{ArrowDown}{ArrowRight}");
			await user.click(screen.getByRole("menuitemradio", { name: "Second" }));
			await act(async () => { await chat.send("side", ""); });
			expect(post).toHaveBeenCalledOnce();
			expect(post.mock.calls[0][1].body.text).toBe("My first question");
			expect(screen.getByRole("combobox", { name: "Side chat question" })).toHaveTextContent("Keep my newer draft");
		}
	});
	it("renders an editable cold draft while the transcript read is pending", async () => {
		const normal = get.getMockImplementation()!;
		get.mockImplementation((path, options) => path.endsWith("/{sideId}") ? new Promise(() => {}) : normal(path, options));
		render(<Harness />);
		expect(await screen.findByRole("combobox", { name: "Side chat question" })).toHaveAttribute("contenteditable", "true");
		expect(screen.queryByText("Opening side chat…")).not.toBeInTheDocument();
		expect(screen.queryByTestId("lattice-loader")).not.toBeInTheDocument();
		expect(post).not.toHaveBeenCalled();
	});

	it("shows one question and the shared lattice before the dispatch barrier, through acknowledgement and streaming", async () => {
		render(<Harness />);
		await screen.findByRole("combobox", { name: "Side chat question" });
		let release!: () => void;
		capture.mockImplementationOnce(() => new Promise<void>(resolve => { release = resolve; }));
		post.mockImplementation(async (_path, options) => { acceptOpening(options.body.clientMessageId); return { data: { turn: snapshot.turns[0] } }; });
		let sending!: Promise<void>;
		act(() => { chat.setQuestionDraft("side", "My first question"); });
		act(() => { sending = chat.send("side", "My first question"); });
		const log = await screen.findByRole("log");
		expect(await within(log).findByText("My first question")).toBeInTheDocument();
		expect(within(log).getByTestId("lattice-loader")).toBeInTheDocument();
		expect(within(log).getByText("Starting side chat…")).toHaveClass("chat-working-shimmer");
		expect(post).not.toHaveBeenCalled();
		act(() => { chat.setQuestionDraft("side", "My newer draft"); });
		await act(async () => { release(); await sending; });
		await waitFor(() => expect(within(log).getAllByText("My first question")).toHaveLength(1));
		expect(screen.queryByText(/Queued message/)).not.toBeInTheDocument();
		expect(screen.getByRole("combobox", { name: "Side chat question" })).toHaveTextContent("My newer draft");
		const loader = within(log).getByTestId("lattice-loader");
		snapshot = { ...snapshot, side: { ...snapshot.side, state: "ready" }, turns: snapshot.turns.map(turn => ({ ...turn, state: "running", startedAt: at })), messages: [...snapshot.messages, { id: "answer", sideId: "side", turnId: "opening", role: "assistant", text: "Live answer", sequence: 2, revision: 1, streaming: true, createdAt: at, updatedAt: at }] };
		await waitFor(() => expect(log.querySelector('[data-find-message-id="answer"]')).toHaveTextContent("Live answer"));
		expect(within(log).getAllByTestId("lattice-loader")).toHaveLength(1);
		expect(loader).toBeInTheDocument();
		expect(screen.queryByText("Starting side chat…")).not.toBeInTheDocument();
	});

	it("settles failed startup inline and retries the same frozen delivery without losing newer edits", async () => {
		render(<Harness />);
		await screen.findByRole("combobox", { name: "Side chat question" });
		post.mockRejectedValueOnce(new Error("Connection lost"));
		await act(async () => { await chat.send("side", "My first question"); });
		expect(screen.getByTestId("session-startup")).toHaveAttribute("data-failed", "true");
		expect(screen.queryByTestId("lattice-loader")).not.toBeInTheDocument();
		const original = post.mock.calls[0][1].body;
		act(() => { chat.setQuestionDraft("side", "Keep this next draft"); });
		post.mockImplementation(async (_path, options) => { acceptOpening(options.body.clientMessageId); return { data: { turn: snapshot.turns[0] } }; });
		await act(async () => { await chat.send("side", ""); });
		expect(post.mock.calls[1][1].body).toEqual(original);
		expect(screen.getByRole("combobox", { name: "Side chat question" })).toHaveTextContent("Keep this next draft");
	});

	it.each([400, 409])("reconciles a rejected first send after HTTP %i without a duplicate question", async status => {
		render(<Harness />);
		await screen.findByRole("combobox", { name: "Side chat question" });
		post.mockResolvedValueOnce({ error: new Error("Opening validation failed"), response: { status } });
		await act(async () => { await chat.send("side", "My first question"); });
		const original = post.mock.calls[0][1].body;
		act(() => { chat.setQuestionDraft("side", "Keep my next draft"); });
		post.mockImplementation(async (_path, options) => { acceptOpening(options.body.clientMessageId); return { data: { turn: snapshot.turns[0] } }; });
		await act(async () => { await chat.send("side", "My first question"); });
		expect(post.mock.calls[1][1].body).toEqual(original);
		await waitFor(() => expect(within(screen.getByRole("log")).getAllByText("My first question")).toHaveLength(1));
		expect(screen.getByRole("combobox", { name: "Side chat question" })).toHaveTextContent("Keep my next draft");
	});

	it.each(["Image question", "/btw Image question"])("restores native image bytes from staged files after a remount: %s", async question => {
		draft = { ...emptySideChatDraft(), text: "Image question", attachments: [{ id: "img", path: ".ao/attachments/a.png", name: "a.png", mimeType: "image/png", bytes: 3 }] };
		const fetchImage = vi.fn().mockResolvedValue({ ok: true, blob: async () => new Blob(["png"], { type: "image/png" }) });
		vi.stubGlobal("fetch", fetchImage);
		render(<Harness />);
		await screen.findByRole("button", { name: "Remove a.png" });
		if (question.startsWith("/btw")) post.mockImplementation(async (path: string) => path.endsWith("/side-chats") ? { data: { side: { ...snapshot.side, id: "destination" } } } : {});
		await act(async () => { await chat.send("side", question); });
		expect(fetchImage).toHaveBeenCalledWith(expect.stringContaining("a.png"));
		const message = post.mock.calls.find(([path]) => path.endsWith("/messages"))![1].body;
		expect(message.attachments).toEqual([{ mimeType: "image/png", data: "cG5n" }]);
		expect(message.resources).toEqual([{ uri: ".ao/attachments/a.png", name: "a.png" }]);
		if (!question.startsWith("/btw")) expect(draft.openingDelivery?.request.attachments).toBeUndefined();
	});

	it.each(["Image question", "/btw Image question"])("keeps oversized saved images as file resources without fetching them: %s", async question => {
		draft = { ...emptySideChatDraft(), text: question, attachments: [{ id: "img", path: ".ao/attachments/large.png", name: "large.png", mimeType: "image/png", bytes: INLINE_ATTACHMENT_BYTES + 1 }] };
		const fetchImage = vi.fn();
		vi.stubGlobal("fetch", fetchImage);
		if (question.startsWith("/btw")) post.mockImplementation(async (path: string) => path.endsWith("/side-chats") ? { data: { side: { ...snapshot.side, id: "destination" } } } : {});
		render(<Harness />);
		await screen.findByRole("button", { name: "Remove large.png" });
		await act(async () => { await chat.send("side", question); });
		const message = post.mock.calls.find(([path]) => path.endsWith("/messages"))![1].body;
		expect(message.attachments).toEqual([]);
		expect(message.resources).toEqual([{ uri: ".ao/attachments/large.png", name: "large.png" }]);
		expect(fetchImage).not.toHaveBeenCalled();
	});

	it("recreates a failed opening as an unused draft with its files, preserving newer edits in the original tab", async () => {
		acceptOpening("first", "failed");
		snapshot.side = { ...snapshot.side, state: "failed", recreateRequired: true, errorMessage: "Startup ownership is uncertain" };
		const file = { id: "img", path: ".ao/attachments/a.png", name: "a.png", mimeType: "image/png", bytes: 3 };
		draft = { ...emptySideChatDraft(), text: "Keep my newer draft", openingDelivery: {
			clientMessageId: "first", createdAt: at, request: { clientMessageId: "first", text: "My first question", resources: [{ uri: file.path, name: file.name }] },
			submitted: { text: "My first question", attachments: [file], references: [] },
		} };
		const originalGet = get.getMockImplementation()!;
		get.mockImplementation((path, options) => options?.params?.path.sideId === "replacement"
			? Promise.resolve(path.endsWith("/draft") ? { data: { contentJson: "" } } : { data: { snapshot: { ...snapshot, side: { ...snapshot.side, id: "replacement", state: "draft" }, turns: [], messages: [] } } })
			: originalGet(path, options));
		post.mockResolvedValue({ data: { side: { ...snapshot.side, id: "replacement", state: "draft", label: "Replacement" } } });
		render(<Harness />);
		await screen.findByText("Keep my newer draft");
		await userEvent.click(within(screen.getByTestId("session-startup")).getByRole("button", { name: "New side chat" }));
		await waitFor(() => expect(put.mock.calls.some(([, options]) => options.params.path.sideId === "replacement" && JSON.parse(options.body.contentJson).text === "My first question" && JSON.parse(options.body.contentJson).attachments[0]?.path === file.path)).toBe(true));
		expect(put.mock.calls.some(([, options]) => options.params.path.sideId === "side" && JSON.parse(options.body.contentJson).text === "Keep my newer draft")).toBe(true);
		expect(post.mock.calls.every(([path]) => path.endsWith("/side-chats"))).toBe(true);
	});
});
