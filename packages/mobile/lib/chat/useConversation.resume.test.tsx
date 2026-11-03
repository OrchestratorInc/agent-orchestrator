import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ServerConfig } from "../config";

// The verified connection drops on every app switch and endpoint race. The
// conversation on screen must not: keyed on the connection, it emptied to the
// "Loading conversation…" state and refilled on each resume.
const fixture = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock("../api", () => ({ apiRequest: fixture.request, ApiError: class extends Error {}, ATTACHMENT_REQUEST_TIMEOUT_MS: 1 }));
vi.mock("../config", () => ({ machineIdentity: (cfg: { hostId?: string }) => `host.${cfg.hostId}`, authHeaders: () => ({}), httpBase: () => "http://qa-host" }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: async () => null, setItem: async () => {}, removeItem: async () => {} } }));
vi.mock("./conversationEvents", () => ({ subscribeConversationEvents: () => () => {} }));
vi.mock("./conversationPoll", () => ({ conversationPollIntervalFor: () => null }));
import { useMobileConversation, type MobileConversation } from "./useConversation";

const lan = { hostId: "a", host: "192.168.1.20" } as ServerConfig;
const tunnel = { hostId: "a", host: "a.trycloudflare.com" } as ServerConfig;
const other = { hostId: "b", host: "192.168.1.20" } as ServerConfig;
let renderer: ReactTestRenderer | undefined;
const seen: Array<{ snapshot: boolean; loading: boolean }> = [];
let conversation!: MobileConversation;
function Harness({ cfg, machine, id = "resume" }: { cfg: ServerConfig | null; machine: string; id?: string }) {
	conversation = useMobileConversation(cfg, machine, id);
	seen.push({ snapshot: Boolean(conversation.snapshot), loading: conversation.loading });
	return null;
}
const conversationRequests = () => fixture.request.mock.calls.filter(([, path]) => /\/conversation(\?|$)/.test(String(path)));

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	seen.length = 0;
	fixture.request.mockReset().mockImplementation(async (cfg: ServerConfig) => ({
		json: async () => ({ controller: "ready", capabilities: [], messages: [], conversationId: `c-${cfg.hostId}` }),
	}));
});
afterEach(async () => { if (renderer) await act(async () => renderer!.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

it("keeps the transcript while the connection is gone, and refreshes when it returns", async () => {
	await act(async () => { renderer = create(<Harness cfg={lan} machine="host.a" />); });
	expect(conversation.snapshot).toBeDefined();
	const shown = seen.length;

	await act(async () => renderer!.update(<Harness cfg={null} machine="host.a" />)); // backgrounded / racing
	await act(async () => renderer!.update(<Harness cfg={tunnel} machine="host.a" />)); // back, on another endpoint

	expect(seen.slice(shown).every((frame) => frame.snapshot && !frame.loading)).toBe(true);
	expect(conversationRequests().map(([cfg]) => (cfg as ServerConfig).host)).toEqual(["192.168.1.20", "a.trycloudflare.com"]);
});

it("never shows one machine's conversation for another, or asks it through the wrong connection", async () => {
	await act(async () => { renderer = create(<Harness cfg={lan} machine="host.a" id="switch" />); });
	fixture.request.mockClear();
	// Mid-switch: the store names machine b while the connection is still a's.
	await act(async () => renderer!.update(<Harness cfg={lan} machine="host.b" id="switch" />));
	expect(conversation.snapshot).toBeUndefined();
	expect(conversationRequests()).toEqual([]);
	await act(async () => renderer!.update(<Harness cfg={other} machine="host.b" id="switch" />));
	expect(conversationRequests().map(([cfg]) => (cfg as ServerConfig).hostId)).toEqual(["b"]);
});
