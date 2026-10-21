import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import type { ServerConfig } from "./config";
import { getSessions } from "./api";

vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() } }));
vi.mock("expo-secure-store", () => ({ getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn() }));

const config: ServerConfig = { host: "ao.test", httpPort: "3011", muxPort: "3011", password: "secret" };

const worker = {
	id: "worker-1",
	projectId: "project-1",
	kind: "worker",
	displayName: "Polish workers",
	status: "working",
	createdAt: "2026-10-20T10:00:00Z",
};
const liveOrchestrator = {
	id: "orch-live",
	projectId: "project-1",
	kind: "orchestrator",
	displayName: "Orchestrator",
	status: "working",
	createdAt: "2026-10-20T09:00:00Z",
};
// The daemon keeps one session per past kill/respawn; /orchestrators lists them all.
const terminatedOrchestrator = {
	id: "orch-old",
	projectId: "project-1",
	kind: "orchestrator",
	displayName: "Orchestrator",
	status: "terminated",
	isTerminated: true,
	createdAt: "2026-10-19T09:00:00Z",
};

function mockDaemon() {
	vi.stubGlobal("fetch", vi.fn(async (url: RequestInfo | URL) => {
		const path = String(url);
		if (path.endsWith("/api/v1/sessions")) {
			return new Response(JSON.stringify({ sessions: [worker, terminatedOrchestrator] }));
		}
		if (path.endsWith("/api/v1/orchestrators")) {
			return new Response(JSON.stringify({ sessions: [terminatedOrchestrator, liveOrchestrator] }));
		}
		if (path.endsWith("/api/v1/projects")) {
			return new Response(JSON.stringify({ projects: [{ id: "project-1", name: "Agent Orchestrator" }] }));
		}
		return new Response("{}", { status: 404 });
	}));
}

beforeEach(() => mockDaemon());
afterEach(() => vi.unstubAllGlobals());

describe("getSessions board rows", () => {
	it("lists the project's live orchestrator alongside its workers, carrying the wire kind", async () => {
		const { sessions } = await getSessions(config, "all");
		const orchestrator = sessions.find((s) => s.id === "orch-live");
		expect(orchestrator).toMatchObject({ projectId: "project-1", kind: "orchestrator", displayName: "Orchestrator" });
		expect(sessions.find((s) => s.id === "worker-1")).toMatchObject({ kind: "worker", projectId: "project-1" });
	});

	it("keeps dead orchestrator history off the board", async () => {
		const { sessions } = await getSessions(config, "all");
		expect(sessions.some((s) => s.id === "orch-old")).toBe(false);
		expect(sessions.filter((s) => s.kind === "orchestrator")).toHaveLength(1);
	});

	it("still maps the orchestrator link for the project card", async () => {
		const { orchestrators } = await getSessions(config, "all");
		expect(orchestrators).toHaveLength(1);
		expect(orchestrators[0]).toMatchObject({ id: "orch-live", projectId: "project-1", projectName: "Agent Orchestrator", isTerminal: false });
	});
});
