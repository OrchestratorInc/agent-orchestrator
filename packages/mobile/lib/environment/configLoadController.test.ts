import { describe, expect, it, vi } from "vitest";
import type { ServerConfig } from "../config";
import type { Endpoint } from "../endpoints";
import { ConfigLoadController, type ConfigLoadPublication } from "./configLoadController";

const saved: ServerConfig = { host: "saved.local", httpPort: "3011", muxPort: "14801", password: "pw" };
const resolved: ServerConfig = { ...saved, host: "lan.local", endpointKind: "lan" };
const endpoints: Endpoint[] = [{ kind: "lan", host: "lan.local", port: 3011, secure: false }];

function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((done) => { resolve = done; });
	return { promise, resolve };
}

function harness(overrides: Partial<ConstructorParameters<typeof ConfigLoadController>[0]> = {}) {
	const loadSaved = vi.fn().mockResolvedValue(saved);
	const resolveActive = vi.fn().mockResolvedValue(resolved);
	const loadEndpoints = vi.fn().mockResolvedValue(endpoints);
	const deps = { loadSaved, resolveActive, loadEndpoints, ...overrides };
	const published: ConfigLoadPublication[] = [];
	const controller = new ConfigLoadController(deps, (result) => published.push(result));
	return { controller, ...deps, published };
}

describe("config loading controller", () => {
	it("continues resolving the paired desktop while Cloud is selected", async () => {
		const h = harness();
		h.controller.setLocalEnabled(true);
		h.controller.setEnvironment("cloud");
		await h.controller.reload();
		expect(h.resolveActive).toHaveBeenCalledTimes(1);
		expect(h.published).toEqual([{ config: resolved, endpoints, raced: true }]);
	});
	it("does no configuration work while the environment is unresolved", async () => {
		const h = harness();
		h.controller.setEnvironment(null);
		await h.controller.reload();
		expect(h.loadSaved).not.toHaveBeenCalled();
		expect(h.resolveActive).not.toHaveBeenCalled();
		expect(h.loadEndpoints).not.toHaveBeenCalled();
		expect(h.published).toEqual([]);
	});

	it("hydrates Cloud from storage without resolving or probing Local endpoints", async () => {
		const h = harness();
		h.controller.setEnvironment("cloud");
		await h.controller.reload();
		expect(h.loadSaved).toHaveBeenCalledTimes(1);
		expect(h.resolveActive).not.toHaveBeenCalled();
		expect(h.loadEndpoints).not.toHaveBeenCalled();
		expect(h.published).toEqual([{ config: saved, endpoints: [], raced: false }]);
	});

	it("resolves Local endpoints before publishing the Local pairing", async () => {
		const h = harness();
		h.controller.setEnvironment("local");
		await h.controller.reload();
		expect(h.resolveActive).toHaveBeenCalledTimes(1);
		expect(h.loadEndpoints).toHaveBeenCalledTimes(1);
		expect(h.published).toEqual([{ config: resolved, endpoints, raced: true }]);
	});

	it("invalidates a deferred Local load after switching to Cloud", async () => {
		const localResult = deferred<ServerConfig | null>();
		const h = harness({ resolveActive: vi.fn(() => localResult.promise) });
		h.controller.setEnvironment("local");
		const pending = h.controller.reload();
		expect(h.resolveActive).toHaveBeenCalledTimes(1);

		h.controller.setEnvironment("cloud");
		localResult.resolve(resolved);
		await pending;

		expect(h.loadEndpoints).not.toHaveBeenCalled();
		expect(h.published).toEqual([]);
		await h.controller.reload();
		expect(h.published).toEqual([{ config: saved, endpoints: [], raced: false }]);
	});
});
