import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

const storage = vi.hoisted(() => ({ items: new Map<string, string>(), gate: undefined as Promise<void> | undefined }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => { await storage.gate; return storage.items.get(key) ?? null; },
	setItem: async (key: string, value: string) => { storage.items.set(key, value); },
} }));

import { rememberLastProject, useLastProject } from "./lastProject";

type Result = ReturnType<typeof useLastProject>;
const renderers: ReactTestRenderer[] = [];

function renderLastProject(hostId: string | null): { current: Result } {
	const result = {} as { current: Result };
	function Probe() {
		result.current = useLastProject(hostId);
		return null;
	}
	act(() => { renderers.push(create(<Probe />)); });
	return result;
}

const flush = () => act(async () => { await Promise.resolve(); await Promise.resolve(); });

afterEach(() => {
	act(() => renderers.splice(0).forEach((renderer) => renderer.unmount()));
	storage.gate = undefined;
});

// The module keeps a cache for the life of the app, so each case uses its own machine.
describe("useLastProject", () => {
	it("is not loaded until the machine's stored project is read", async () => {
		storage.items.set("ao.lastProject.host-stored", "api");
		const result = renderLastProject("host-stored");
		expect(result.current).toEqual({ loaded: false, projectId: null });
		await flush();
		expect(result.current).toEqual({ loaded: true, projectId: "api" });
	});

	it("loads with no project for a machine nothing was remembered on", async () => {
		const result = renderLastProject("host-empty");
		await flush();
		expect(result.current).toEqual({ loaded: true, projectId: null });
	});

	it("keeps each machine's project separate and persists it", async () => {
		rememberLastProject("host-a", "web");
		rememberLastProject("host-b", "api");
		const a = renderLastProject("host-a");
		const b = renderLastProject("host-b");
		await flush();
		expect(a.current.projectId).toBe("web");
		expect(b.current.projectId).toBe("api");
		expect(storage.items.get("ao.lastProject.host-a")).toBe("web");
	});

	it("updates a mounted reader when a project is remembered", async () => {
		const result = renderLastProject("host-live");
		await flush();
		rememberLastProject("host-live", "docs");
		await flush();
		expect(result.current).toEqual({ loaded: true, projectId: "docs" });
	});

	it("keeps a project remembered while the stored one was still being read", async () => {
		storage.items.set("ao.lastProject.host-race", "old");
		let release!: () => void;
		storage.gate = new Promise((resolve) => { release = resolve; });
		const result = renderLastProject("host-race");
		rememberLastProject("host-race", "new");
		release();
		await flush();
		expect(result.current).toEqual({ loaded: true, projectId: "new" });
	});

	it("ignores a missing machine or project", async () => {
		rememberLastProject(null, "web");
		rememberLastProject("host-none", null);
		expect(storage.items.has("ao.lastProject.host-none")).toBe(false);
		expect(renderLastProject(null).current).toEqual({ loaded: false, projectId: null });
	});
});
