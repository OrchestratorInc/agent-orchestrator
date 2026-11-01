import { act, render, renderHook, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { useIndependentSideChats } from "./IndependentSideChats";
import userEvent from "@testing-library/user-event";
import { TooltipProvider } from "../ui/tooltip";
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

it("stops preparation when its source unmounts", async () => {
	const hook = await setup();
	let release!: () => void;
	capture.mockImplementationOnce(() => new Promise<void>((resolve) => { release = resolve; }));
	let sending!: Promise<void>;
	act(() => { sending = hook.result.current.send("source", "/side question"); });
	await waitFor(() => expect(release).toBeDefined());
	hook.unmount();
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

it.each(["/side explain files", "/side"])("retains %s if its destination has another pending command", async (question) => {
	const hook = await setup();
	const implementation = post.getMockImplementation()!;
	post.mockImplementation(async (path, options) => {
		const result = await implementation(path, options);
		if (path.endsWith("/side-chats")) {
			const source = JSON.parse(drafts.get("source")!);
			drafts.set(result.data.side.id, JSON.stringify({ ...emptySideChatDraft(), pendingCommand: {
				creationKey: "other-command", receipt: source.pendingCommand.receipt,
			} }));
		}
		return result;
	});
	await act(async () => { await hook.result.current.send("source", question); });
	expect(requests).toEqual([]);
	expect(JSON.parse(drafts.get("source")!).pendingCommand.accepted).not.toBe(true);
});


it.each([404,409,500])("cancels a handoff rejected by destination HTTP %i without sending or recreating",async status=>{
 const hook=await setup();
 const original=post.getMockImplementation()!;
 post.mockImplementation((path,options)=>path.endsWith("/messages")?Promise.resolve({error:new Error("Destination unavailable"),response:{status}}):original(path,options));
 await act(async()=>{await hook.result.current.send("source","/side explain files")});
 const command=JSON.parse(drafts.get("source")!).pendingCommand;
 expect(command).toBeDefined();
 const before=post.mock.calls.length;
 await act(async()=>{await hook.result.current.cancelHandoff("source")});
 const draft=JSON.parse(drafts.get("source")!);
 expect(draft.pendingCommand).toBeUndefined();
 expect(draft.text).toBe("/side explain files");
 expect(draft.attachments[0].path).toBe("/staged/notes.txt");
 expect(post).toHaveBeenCalledTimes(before);
 expect(creations.size).toBe(1);
});

it("keeps newer text, files and references while cancellation waits for capture, and blocks repeated actions",async()=>{
 const hook=await setup();loseSend=true;
 await act(async()=>{await hook.result.current.send("source","/side explain files")});
 const old=JSON.parse(drafts.get("source")!).pendingCommand;
 let release!:()=>void;
 capture.mockImplementationOnce(()=>new Promise<void>(resolve=>{release=resolve}));
 let cancelling!:Promise<void>;
 act(()=>{cancelling=hook.result.current.cancelHandoff("source")});
 await waitFor(()=>expect(release).toBeDefined());
 expect(JSON.parse(drafts.get("source")!).pendingCommand).toMatchObject({...old,cancelRequested:true});
 const newer={...emptySideChatDraft(),text:"new draft",attachments:[{id:"new",path:"/staged/new.txt",name:"new.txt",mimeType:"text/plain",bytes:2}],references:[{id:"quote",conversationId:"main",messageId:"answer",revision:1,text:"selected",role:"assistant" as const}]};
 await act(async()=>{await hook.result.current.replaceDraft("source",newer)});
 const before=post.mock.calls.length;
 await act(async()=>{await hook.result.current.cancelHandoff("source");await hook.result.current.send("source","/side another")});
 expect(post).toHaveBeenCalledTimes(before);
 await act(async()=>{release();await cancelling});
 expect(JSON.parse(drafts.get("source")!)).toMatchObject(newer);
 expect(JSON.parse(drafts.get("source")!).pendingCommand).toBeUndefined();
});

it.each(["intent save","intent capture","clear save","clear capture"])("retains retryable cancellation after failed %s and remount",async stage=>{
 const hook=await setup();loseSend=true;
 await act(async()=>{await hook.result.current.send("source","/side explain files")});
 const writer=put.getMockImplementation()!;
 if(stage.endsWith("save")) {
  if(stage.startsWith("clear"))put.mockImplementationOnce(writer);
  put.mockResolvedValueOnce({error:new Error("save failed")});
 } else {
  if(stage.startsWith("clear"))capture.mockResolvedValueOnce(undefined);
  capture.mockRejectedValueOnce(new Error("capture failed"));
 }
 await act(async()=>{await hook.result.current.cancelHandoff("source")});
 expect(hook.result.current.error).toContain("failed");
 // Flush the failed-operation state through the same ordered writer before remount.
 await act(async()=>{await hook.result.current.replaceDraft("source",{...emptySideChatDraft(),text:"newer draft"})});
 expect(JSON.parse(drafts.get("source")!).pendingCommand.cancelRequested).toBe(true);
 hook.unmount();
 const restored=renderHook(()=>useIndependentSideChats("session",[],[],async()=>[],true));
 await waitFor(()=>expect(restored.result.current.activeId).toBe("source"));
 const before=post.mock.calls.length;
 await act(async()=>{await restored.result.current.send("source","")});
 expect(post).toHaveBeenCalledTimes(before);
 expect(JSON.parse(drafts.get("source")!).pendingCommand).toBeUndefined();
 expect(JSON.parse(drafts.get("source")!).text).toBe("newer draft");
});

it("uses fresh identities for an explicit question after cancellation",async()=>{
 const hook=await setup();loseSend=true;
 await act(async()=>{await hook.result.current.send("source","/side explain files")});
 const original=JSON.parse(drafts.get("source")!).pendingCommand;
 await act(async()=>{await hook.result.current.cancelHandoff("source")});
 await act(async()=>{await hook.result.current.send("source","/side another question")});
 expect(creations.size).toBe(2);
 expect([...creations.keys()][1]).not.toBe(original.creationKey);
 expect(requests.at(-1)).toMatchObject({text:"another question"});
 expect((requests.at(-1) as {clientMessageId:string}).clientMessageId).not.toBe(original.receipt.clientMessageId);
});

it("provides accessible cancellation next to retry and explains uncertain delivery",async()=>{
 const hook=await setup();loseSend=true;
 await act(async()=>{await hook.result.current.send("source","/side explain files")});
 act(()=>hook.result.current.show("source"));
 await waitFor(()=>expect(hook.result.current.activeId).toBe("source"));
 const view=render(<TooltipProvider>{hook.result.current.panel}</TooltipProvider>);
 await waitFor(()=>{view.rerender(<TooltipProvider>{hook.result.current.panel}</TooltipProvider>);expect(screen.getByRole("button",{name:"Retry delivery"})).toBeEnabled()});
 const cancel=screen.getByRole("button",{name:"Cancel handoff"});
 expect(cancel.tagName).toBe("BUTTON");
 expect(screen.getByText(/The question may already have reached its destination/)).toBeInTheDocument();
 await userEvent.click(cancel);
 await waitFor(()=>expect(JSON.parse(drafts.get("source")!).pendingCommand).toBeUndefined());
 view.unmount();
});


it.each(["close","unmount"])("does not clear cancellation after source %s",async mode=>{
 const hook=await setup();loseSend=true;
 await act(async()=>{await hook.result.current.send("source","/side explain files")});
 let release!:()=>void;
 capture.mockImplementationOnce(()=>new Promise<void>(resolve=>{release=resolve}));
 let cancelling!:Promise<void>;
 act(()=>{cancelling=hook.result.current.cancelHandoff("source")});
 await waitFor(()=>expect(release).toBeDefined());
 if(mode==="unmount")hook.unmount();
 else await act(async()=>{await hook.result.current.close("source").catch(()=>undefined)});
 await act(async()=>{release();await cancelling});
 expect(JSON.parse(drafts.get("source")!).pendingCommand.cancelRequested).toBe(true);
 expect(requests).toHaveLength(1);
});
