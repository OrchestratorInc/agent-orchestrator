import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ServerConfig } from "../config";
import type { ChatExcerpt, ChatImage } from "./types";

const { request, storage, stored } = vi.hoisted(() => {
 const stored = new Map<string, string>();
 return { request: vi.fn(), stored, storage: {
  getItem: vi.fn(async (key: string) => stored.get(key) ?? null),
  setItem: vi.fn(async (key: string, value: string) => { stored.set(key, value); }),
  removeItem: vi.fn(async (key: string) => { stored.delete(key); }),
 } };
});
vi.mock("../api", () => ({ apiRequest: request, ApiError: class extends Error {}, ATTACHMENT_REQUEST_TIMEOUT_MS: 1 }));
vi.mock("../config", () => ({ machineIdentity: () => "qa-host", authHeaders: () => ({}), httpBase: () => "http://qa-host" }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: storage }));
vi.mock("./conversationEvents", () => ({ subscribeConversationEvents: () => () => {} }));
vi.mock("./conversationPoll", () => ({ conversationPollIntervalFor: () => null }));
import { useMobileConversation, type MobileConversation } from "./useConversation";

const config = {} as ServerConfig;
const images: ChatImage[] = [{ mimeType: "image/png", data: "aGVsbG8=" }];
const excerpts: ChatExcerpt[] = [{ conversationId: "conversation", messageId: "source", revision: 2, text: "Quoted context" }];
let renderer: ReactTestRenderer | undefined;
let conversation: MobileConversation;
let supportsExcerpts: boolean | undefined;
function Harness() { conversation = useMobileConversation(config, "qa-session"); return null; }

beforeEach(() => {
 vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
 stored.clear(); vi.clearAllMocks(); supportsExcerpts = undefined;
 request.mockImplementation(async (_cfg, path, init) => ({ json: async () => path.endsWith("/attachments")
  ? { paths: [".ao/attachments/qa-image.png"] }
  : init?.method === "POST" ? { turnId: "qa-turn", duplicate: false }
  : { controller: "ready", supportsExcerpts, capabilities: ["images"], messages: [] } }));
});
afterEach(async () => { if (renderer) await act(async () => renderer!.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

it("rejects repeated quoted image submissions to an older host before staging durable files", async () => {
 await act(async () => { renderer = create(<Harness />); });
 request.mockClear();
 for (let attempt = 0; attempt < 2; attempt++) {
  await act(async () => { await expect(conversation.send("Explain this", images, undefined, excerpts)).rejects.toThrow("Update Orchestrator on this machine"); });
 }
 expect(request.mock.calls.filter(call => call[2]?.method === "POST")).toEqual([]);
 expect(storage.setItem).not.toHaveBeenCalled();
 expect(images).toEqual([{ mimeType: "image/png", data: "aGVsbG8=" }]);
 expect(excerpts[0].text).toBe("Quoted context");
});

it("probes a downgraded host before staging even when the loaded conversation advertised support", async () => {
 supportsExcerpts = true;
 await act(async () => { renderer = create(<Harness />); });
 expect(conversation.snapshot?.supportsExcerpts).toBe(true);
 supportsExcerpts = undefined; request.mockClear();
 await act(async () => { await expect(conversation.send("Explain this", images, undefined, excerpts)).rejects.toThrow("Update Orchestrator on this machine"); });
 expect(request.mock.calls.filter(call => call[2]?.method === "POST")).toEqual([]);
});

it("checks support before staging supported quoted images and preserves ordinary attachment sends", async () => {
 supportsExcerpts = true;
 await act(async () => { renderer = create(<Harness />); });
 request.mockClear();
 await act(async () => { await conversation.send("Explain this", images, undefined, excerpts); });
 const calls = request.mock.calls;
 const stage = calls.findIndex(call => call[1].endsWith("/attachments"));
 expect(stage).toBeGreaterThan(0);
 expect(calls[0][1]).toContain("/conversation?");
 const posted = calls.find(call => call[1].endsWith("/messages"));
 expect(JSON.parse(posted![2].body)).toMatchObject({ excerpts, attachments: images });
 await act(async () => { await conversation.acknowledgeSend(JSON.parse(posted![2].body).clientMessageId); });
 supportsExcerpts = undefined; request.mockClear();
 await act(async () => { await conversation.send("Ordinary image", images); });
 expect(request.mock.calls[0][1]).toBe("/api/v1/sessions/qa-session/attachments");
});
