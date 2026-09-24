import { describe, expect, it, vi } from "vitest";
import type { ServerConfig } from "../config";
import type { SessionSource } from "../environment/types";
import { conversationAccess } from "./conversation-access";

const source = (kind: "local" | "cloud"): SessionSource => ({
	kind,
	listProjects: vi.fn(),
	listSessions: vi.fn(),
	createSession: vi.fn(),
	deleteSession: vi.fn(),
	getConversationPage: vi.fn(),
	sendMessage: vi.fn(),
	cancelTurn: vi.fn(),
	subscribeEvents: vi.fn(),
	resumeSession: vi.fn(),
});

const config = {
	host: "mac.local",
	httpPort: "9090",
	password: "secret",
	secure: false,
} as ServerConfig;

describe("conversationAccess", () => {
	it("opens Cloud conversations without a Local daemon and polls for updates", () => {
		const cloud = source("cloud");
		expect(conversationAccess({ config: null, source: cloud, sessionId: "orch-1" })).toEqual({
			source: cloud,
			cacheKey: "cloud/orch-1",
			pollInterval: 2_000,
			subscribeToEvents: false,
		});
	});

	it("keeps Local conversations scoped to their paired daemon and event stream", () => {
		const local = source("local");
		const access = conversationAccess({ config, source: local, sessionId: "worker-1" });
		expect(access).toMatchObject({ source: local, pollInterval: null, subscribeToEvents: true });
		expect(access?.cacheKey).toContain("mac.local:9090/secret/worker-1");
	});

	it("does not expose a Local source before its daemon configuration resolves", () => {
		expect(conversationAccess({ config: null, source: source("local"), sessionId: "worker-1" })).toBeUndefined();
		expect(conversationAccess({ config, source: undefined, sessionId: "worker-1" })).toBeUndefined();
	});
});
