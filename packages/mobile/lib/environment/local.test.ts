import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({
	default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() },
}));
vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn(),
}));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));

const getProjects = vi.fn();
const getSessions = vi.fn();
const killSession = vi.fn();
const delegateTask = vi.fn();
vi.mock("../api", async (importOriginal) => ({
	...(await importOriginal<typeof import("../api")>()),
	getProjects: (...args: unknown[]) => getProjects(...args),
	getSessions: (...args: unknown[]) => getSessions(...args),
	killSession: (...args: unknown[]) => killSession(...args),
	delegateTask: (...args: unknown[]) => delegateTask(...args),
}));

import { DEFAULT_CONFIG, type ServerConfig } from "../config";
import { createLocalSessionSource } from "./local";

const cfg: ServerConfig = { ...DEFAULT_CONFIG, host: "10.0.0.2", password: "pw" };

describe("createLocalSessionSource", () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("identifies as the local environment", () => {
		expect(createLocalSessionSource(cfg).kind).toBe("local");
	});

	it("forwards listProjects to the daemon with the active config", async () => {
		getProjects.mockResolvedValue([{ id: "p1", name: "web" }]);
		const result = await createLocalSessionSource(cfg).listProjects();
		expect(getProjects).toHaveBeenCalledWith(cfg);
		expect(result).toEqual([{ id: "p1", name: "web" }]);
	});

	// getSessions returns the daemon's full board payload; the port only
	// promises the session list, so the adapter unwraps it here rather than
	// widening the interface for one environment's extra fields.
	it("unwraps the daemon's sessions response", async () => {
		getSessions.mockResolvedValue({
			sessions: [{ id: "s1" }], orchestrators: [], orchestratorId: null, stats: {}, projects: [],
		});
		expect(await createLocalSessionSource(cfg).listSessions()).toEqual([{ id: "s1" }]);
		expect(getSessions).toHaveBeenCalledWith(cfg);
	});

	it("spawns through delegateTask and returns the new session id", async () => {
		delegateTask.mockResolvedValue({ id: "s9" });
		const result = await createLocalSessionSource(cfg).createSession({
			projectId: "p1", prompt: "fix the build", mode: "chat",
		});
		expect(result).toEqual({ id: "s9" });
		expect(delegateTask).toHaveBeenCalledWith(cfg, expect.objectContaining({
			projectId: "p1", brief: "fix the build", mode: "chat",
		}));
	});

	it("refuses to spawn without a resolved project", async () => {
		await expect(
			createLocalSessionSource(cfg).createSession({ prompt: "x" }),
		).rejects.toThrow("Pick a project first");
		expect(delegateTask).not.toHaveBeenCalled();
	});

	it("deletes a session by killing it", async () => {
		await createLocalSessionSource(cfg).deleteSession("s1");
		expect(killSession).toHaveBeenCalledWith(cfg, "s1");
	});
});
