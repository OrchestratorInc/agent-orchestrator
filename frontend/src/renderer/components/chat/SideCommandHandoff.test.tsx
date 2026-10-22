import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { useIndependentSideChats } from "./IndependentSideChats";
import { emptySideChatDraft } from "./sideChatDraft";

const { get, post, put, capture } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), capture: vi.fn() }));
vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: get, POST: post, PUT: put, DELETE: vi.fn(), PATCH: vi.fn() },
	apiErrorMessage: (e: unknown) => e instanceof Error ? e.message : "Request failed",
	getApiBaseUrl: () => "", subscribeApiBaseUrl: () => () => {},
}));
vi.mock("../../lib/bridge", () => ({ aoBridge: { sideChats: { capture, confirmClose: async () => {}, getPendingCloses: async () => [], onPendingClosesChanged: () => () => {} } } }));

const drafts = new Map<string, string>();
const creations = new Map<string, string>();
const requests: unknown[] = [];
let loseCreation = false;
let loseSend = false;

beforeEach(() => {
	vi.clearAllMocks(); drafts.clear(); creations.clear(); requests.length = 0;
	loseCreation = false; loseSend = false;
	capture.mockResolvedValue(undefined);
	HTMLElement.prototype.scrollTo = vi.fn();
	get.mockImplementation(async (path, options) => {
		const id = options?.params?.path?.sideId;
		if (path.endsWith("/draft")) return { data: { contentJson: drafts.get(id) ?? "" } };
		if (path.endsWith("/side-chats")) return { data: { sides: [{ id: "source", sessionId: "session", mainConversationId: "main", state: "ready", label: "Source" }] } };
		return { data: { snapshot: { side: { id, state: "ready" }, turns: [], messages: [], activities: [] } } };
	});
	put.mockImplementation(async (_path, options) => {
		drafts.set(options.params.path.sideId, options.body.contentJson);
		return {};
	});
	post.mockImplementation(async (path, options) => {
		if (path.endsWith("/side-chats")) {
			const key = options.body.idempotencyKey;
			if (!creations.has(key)) creations.set(key, `destination-${creations.size + 1}`);
			if (loseCreation) { loseCreation = false; throw new Error("Creation reply lost"); }
			return { data: { side: { id: creations.get(key), state: "ready", sessionId: "session", mainConversationId: "main" } } };
		}
		requests.push(structuredClone(options.body));
		if (loseSend) { loseSend = false; throw new Error("Acceptance reply lost"); }
		return { data: {} };
	});
});

async function setup() {
	const hook = renderHook(() => useIndependentSideChats("session", [], [], async (items) => items.map((_, i) => `/staged/image-${i}.png`), true));
	await waitFor(() => expect(hook.result.current.activeId).toBe("source"));
	await act(async () => { await hook.result.current.replaceDraft("source", { ...emptySideChatDraft(), text: "/side explain files", attachments: [{ id: "doc", path: "/staged/notes.txt", name: "notes.txt", mimeType: "text/plain", bytes: 10 }] }); });
	return hook;
}

it("reserves the source before a delayed create and preserves newer draft edits", async () => {
	const hook = await setup();
	const implementation = post.getMockImplementation()!;
	let release!: () => void;
	post.mockImplementation(async (path, options) => {
		if (path.endsWith("/side-chats")) await new Promise<void>((resolve) => { release = resolve; });
		return implementation(path, options);
	});
	let first!: Promise<void>;
	act(() => { first = hook.result.current.send("source", "/side explain files"); });
	await waitFor(() => expect(release).toBeDefined());
	await act(async () => { await hook.result.current.send("source", "/side explain files"); });
	act(() => hook.result.current.setQuestionDraft("source", "new draft while waiting"));
	await act(async () => { release(); await first; });
	expect(creations.size).toBe(1);
	expect(requests).toHaveLength(1);
	expect(JSON.parse(drafts.get("source")!).text).toBe("new draft while waiting");
	expect(JSON.parse(drafts.get("source")!).attachments).toEqual([]);
});

it("delivers a nested command as the frozen question without creating another destination", async () => {
	const hook = await setup();
	await act(async () => { await hook.result.current.send("source", "/side /side explain files"); });
	expect(creations.size).toBe(1);
	expect(requests).toHaveLength(1);
	expect(requests[0]).toMatchObject({ text: "/side explain files" });
});

it("does not create or dispatch after the source session changes during capture", async () => {
	const hook = renderHook(({ session }) => useIndependentSideChats(session, [], [], async () => [], true), { initialProps: { session: "session" } });
	await waitFor(() => expect(hook.result.current.activeId).toBe("source"));
	let release!: () => void;
	capture.mockImplementationOnce(() => new Promise<void>((resolve) => { release = resolve; }));
	let sending!: Promise<void>;
	act(() => { sending = hook.result.current.send("source", "/side question"); });
	await waitFor(() => expect(release).toBeDefined());
	hook.rerender({ session: "another-session" });
	await act(async () => { release(); await sending; });
	expect(creations.size).toBe(0);
	expect(requests).toEqual([]);
});

it.each(["creation", "send"])("reuses a frozen destination and files after a lost %s reply and remount", async (lost) => {
	const hook = await setup();
	loseCreation = lost === "creation"; loseSend = lost === "send";
	const image = { mimeType: "image/png", data: "aW1hZ2U=" };
	await act(async () => { await hook.result.current.send("source", "/side explain files", [image]); });
	const pending = JSON.parse(drafts.get("source")!).pendingCommand;
	expect(pending.receipt.request.resources.map((r: { uri: string }) => r.uri)).toEqual(["/staged/notes.txt", "/staged/image-0.png"]);
	expect(pending.receipt.request.attachments).toEqual([image]);
	hook.unmount();
	const restored = renderHook(() => useIndependentSideChats("session", [], [], async () => { throw new Error("Must not restage on retry"); }, true));
	await waitFor(() => expect(restored.result.current.activeId).toBe("source"));
	await act(async () => { await restored.result.current.send("source", "/side explain files"); });
	expect(creations.size).toBe(1);
	expect(requests.length).toBe(lost === "send" ? 2 : 1);
	for (const request of requests) expect(request).toEqual(pending.receipt.request);
	expect(JSON.parse(drafts.get("source")!).pendingCommand).toBeUndefined();
});

it("refuses unsupported side references before creating a destination", async () => {
	const hook = await setup();
	act(() => hook.result.current.addReference("source", { id: "ref", conversationId: "source", messageId: "answer", revision: 1, text: "quote", role: "assistant" }));
	await act(async () => { await hook.result.current.send("source", "/side explain files"); });
	expect(creations.size).toBe(0);
	expect(requests).toHaveLength(0);
	expect(hook.result.current.error).toMatch(/References from another side/);
});
