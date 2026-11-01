import { act, render, renderHook, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useIndependentSideChats } from "./IndependentSideChats";
import type { ChatDraftExcerptReference } from "../../lib/chat-drafts";
import { TooltipProvider } from "../ui/tooltip";

const { get, post, put, remove, capture, pendingCloses } = vi.hoisted(() => ({
	get: vi.fn(), post: vi.fn(), put: vi.fn(), remove: vi.fn(), capture: vi.fn(), pendingCloses: vi.fn(),
}));
vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: get, POST: post, PUT: put, DELETE: remove, PATCH: vi.fn() },
	apiErrorMessage: (error: unknown) => error instanceof Error ? error.message : "Request failed",
	getApiBaseUrl: () => "", subscribeApiBaseUrl: () => () => {},
}));
vi.mock("../../lib/bridge", () => ({ aoBridge: { sideChats: {
	capture, getPendingCloses: pendingCloses, confirmClose: async () => {}, onPendingClosesChanged: () => () => {},
} } }));

const excerpt: ChatDraftExcerptReference = {
	id: "selection", conversationId: "main", messageId: "message", revision: 1, text: "selected text", role: "assistant",
};
const side = (id: string) => ({ id, sessionId: "session", mainConversationId: "main", state: "ready", label: id, hasWork: false });
let listed: ReturnType<typeof side>[];

beforeEach(() => {
	vi.resetAllMocks(); listed = [side("A"), side("B")];
	HTMLElement.prototype.scrollTo = vi.fn();
	get.mockImplementation(async (path, options) => {
		if (path.endsWith("/side-chats")) return { data: { sides: listed } };
		if (path.endsWith("/draft")) return { data: { contentJson: "" } };
		return { data: { snapshot: { side: side(options.params.path.sideId), messages: [], turns: [], activities: [] } } };
	});
	post.mockResolvedValue({ data: { side: side("created") } });
	put.mockResolvedValue({}); remove.mockResolvedValue({});
	capture.mockResolvedValue(undefined); pendingCloses.mockResolvedValue([]);
});

async function mount() {
	const hook = renderHook(({ session, enabled }) => useIndependentSideChats(session, [], [], async () => [], false, enabled),
		{ initialProps: { session: "session", enabled: true } });
	await waitFor(() => expect(hook.result.current.activeId).toBe("B"));
	return hook;
}

describe("selection creation activation", () => {
	it("adds the original reference without stealing a destination chosen during the request", async () => {
		const hook = await mount();
		let complete!: (value: { data: { side: ReturnType<typeof side> } }) => void;
		post.mockImplementationOnce(() => new Promise(resolve => { complete = resolve; }));
		let request!: Promise<{ id: string }>;
		act(() => { request = hook.result.current.create(excerpt, false, "creation", { shouldActivate: () => true }); });
		act(() => hook.result.current.show("A"));
		await act(async () => { complete({ data: { side: side("created") } }); await request; });
		expect(hook.result.current.activeId).toBe("A");
		expect(hook.result.current.sides.some(value => value.id === "created")).toBe(true);
		await waitFor(() => expect(put.mock.calls.some(([, options]) => options.params.path.sideId === "created" &&
			JSON.parse(options.body.contentJson).references?.[0]?.text === excerpt.text)).toBe(true));
	});

	it("honors the caller's activation guard even if the active tab did not change", async () => {
		const hook = await mount();
		await act(async () => { await hook.result.current.create(excerpt, false, "creation", { shouldActivate: () => false }); });
		expect(hook.result.current.activeId).toBe("B");
	});

	it("rejects same-session source replacement during capture without updating drafts or error state", async () => {
		const hook = await mount(); let release!: () => void; let valid = true;
		capture.mockImplementationOnce(() => new Promise<void>(resolve => { release = resolve; }));
		let request!: Promise<{ id: string }>;
		act(() => { request = hook.result.current.create(excerpt, false, "creation", { isCurrent: () => valid, shouldActivate: () => true }); });
		await waitFor(() => expect(capture).toHaveBeenCalledOnce());
		valid = false;
		await act(async () => { release(); await expect(request).rejects.toThrow("no longer active"); });
		expect(hook.result.current.sides.some(value => value.id === "created")).toBe(false);
		expect(put).not.toHaveBeenCalled();
		expect(hook.result.current.error).toBeUndefined();
		expect(hook.result.current.pending).toBe(false);
	});

	it("loads an inactive recovered draft before saving its reference", async () => {
		const hook = await mount();
		const oldGet = get.getMockImplementation()!;
		get.mockImplementation(async (path, options) => path.endsWith("/draft") && options.params.path.sideId === "created"
			? { data: { contentJson: JSON.stringify({ version: 1, text: "keep my question", attachments: [], references: [] }) } }
			: oldGet(path, options));
		await act(async () => { await hook.result.current.create(excerpt, false, "creation", { shouldActivate: () => false }); });
		expect(put.mock.calls.some(([, options]) => options.params.path.sideId === "created" &&
			JSON.parse(options.body.contentJson).text === "keep my question" &&
			JSON.parse(options.body.contentJson).references[0].text === excerpt.text)).toBe(true);
	});

	it("keeps native focus outside the side composer when guarded creation finishes", async () => {
		let chat!: ReturnType<typeof useIndependentSideChats>;
		function Harness() {
			chat = useIndependentSideChats("session", [], [], async () => [], false);
			return <TooltipProvider><input aria-label="Elsewhere" />{chat.panel}</TooltipProvider>;
		}
		render(<Harness />);
		await waitFor(() => expect(chat.activeId).toBe("B"));
		let release!: () => void;
		capture.mockImplementationOnce(() => new Promise<void>(resolve => { release = resolve; }));
		let request!: Promise<{ id: string }>;
		act(() => { request = chat.create(excerpt, false, "creation", { shouldActivate: () => true }); });
		await waitFor(() => expect(capture).toHaveBeenCalledOnce());
		act(() => chat.show("A"));
		await waitFor(() => expect(screen.getByRole("combobox", { name: "Side chat question" })).toHaveFocus());
		const elsewhere = screen.getByRole("textbox", { name: "Elsewhere" });
		act(() => elsewhere.focus());
		await act(async () => { release(); await request; });
		expect(elsewhere).toHaveFocus();
		expect(chat.activeId).toBe("A");
	});

	it("preserves an edit batched with an inactive creation's draft read", async () => {
		const hook = await mount();
		const oldGet = get.getMockImplementation()!;
		let release!: (value: { data: { contentJson: string } }) => void;
		get.mockImplementation((path, options) => path.endsWith("/draft") && options.params.path.sideId === "created"
			? new Promise(resolve => { release = resolve; }) : oldGet(path, options));
		let request!: Promise<{ id: string }>;
		act(() => { request = hook.result.current.create(excerpt, false, "creation", { shouldActivate: () => false }); });
		await waitFor(() => expect(release).toBeDefined());
		await act(async () => {
			hook.result.current.setQuestionDraft("B", "newer B question");
			release({ data: { contentJson: "" } }); await request;
		});
		await waitFor(() => expect(put.mock.calls.some(([, options]) => options.params.path.sideId === "B" &&
			JSON.parse(options.body.contentJson).text === "newer B question")).toBe(true));
	});

	it("does not activate after the user switches away and back while desktop capture waits", async () => {
		const hook = await mount(); let release!: () => void;
		capture.mockImplementationOnce(() => new Promise<void>(resolve => { release = resolve; }));
		let request!: Promise<{ id: string }>;
		act(() => { request = hook.result.current.create(excerpt, false, "creation", { shouldActivate: () => true }); });
		await waitFor(() => expect(capture).toHaveBeenCalledOnce());
		act(() => hook.result.current.show("A")); act(() => hook.result.current.show("B"));
		await act(async () => { release(); await request; });
		expect(hook.result.current.activeId).toBe("B");
	});

	it("preserves ordinary New side chat activation and generates a fresh key each time", async () => {
		const hook = await mount();
		const creations = new Map<string, string>();
		post.mockImplementation(async (_path, options) => {
			const key = options.body.idempotencyKey;
			if (!creations.has(key)) creations.set(key, `new-${creations.size + 1}`);
			return { data: { side: side(creations.get(key)!) } };
		});
		await act(async () => { await hook.result.current.create(excerpt, true); });
		await act(async () => { await hook.result.current.create(excerpt, true); });
		expect(creations.size).toBe(2);
		expect(post.mock.calls.map(([, options]) => options.body.forceNew)).toEqual([true, true]);
		expect(hook.result.current.activeId).toBe("new-2");
	});

	it("allows deliberate New creation after a lost Add creation reply", async () => {
		const hook = await mount();
		post.mockRejectedValueOnce(new Error("Reply lost"));
		await act(async () => { await expect(hook.result.current.create(excerpt, false, "unanswered", { shouldActivate: () => true })).rejects.toThrow("Reply lost"); });
		expect(hook.result.current.pending).toBe(false);
		await act(async () => { await hook.result.current.create(excerpt, true); });
		expect(post.mock.calls[1][1].body).toMatchObject({ forceNew: true });
		expect(post.mock.calls[1][1].body.idempotencyKey).not.toBe("unanswered");
		expect(hook.result.current.activeId).toBe("created");
	});

	it.each(["replace", "disable", "replace-back"])("rejects a completion after %s during desktop capture", async (change) => {
		const hook = await mount(); let release!: () => void;
		capture.mockImplementationOnce(() => new Promise<void>(resolve => { release = resolve; }));
		let request!: Promise<{ id: string }>;
		act(() => { request = hook.result.current.create(excerpt, false, "creation", { shouldActivate: () => true }); });
		await waitFor(() => expect(capture).toHaveBeenCalledOnce());
		hook.rerender({ session: "other", enabled: change !== "disable" });
		if (change === "replace-back") hook.rerender({ session: "session", enabled: true });
		await act(async () => { release(); await expect(request).rejects.toThrow("no longer active"); });
		expect(hook.result.current.sides.some(value => value.id === "created")).toBe(false);
		expect(hook.result.current.error).toBeUndefined();
		expect(hook.result.current.pending).toBe(false);
	});

	it("does not resurrect a side closed while desktop capture waits", async () => {
		const hook = await mount(); let release!: () => void;
		capture.mockImplementationOnce(() => new Promise<void>(resolve => { release = resolve; }));
		let request!: Promise<{ id: string }>;
		act(() => { request = hook.result.current.create(excerpt, false, "creation", { shouldActivate: () => true }); });
		await waitFor(() => expect(capture).toHaveBeenCalledOnce());
		await act(async () => { await hook.result.current.close("created"); });
		await act(async () => { release(); await expect(request).rejects.toThrow("no longer active"); });
		expect(hook.result.current.sides.some(value => value.id === "created")).toBe(false);
		expect(hook.result.current.pending).toBe(false);
	});

	it("ignores late failures from a replaced session", async () => {
		const hook = await mount(); let fail!: (error: Error) => void;
		post.mockImplementationOnce(() => new Promise((_resolve, reject) => { fail = reject; }));
		let request!: Promise<{ id: string }>;
		act(() => { request = hook.result.current.create(excerpt, false, "creation", { shouldActivate: () => true }); });
		hook.rerender({ session: "other", enabled: true });
		await act(async () => { fail(new Error("old failure")); await expect(request).rejects.toThrow("old failure"); });
		expect(hook.result.current.error).toBeUndefined();
	});
});
