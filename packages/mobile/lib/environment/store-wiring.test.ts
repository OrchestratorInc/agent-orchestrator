import { describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({
	default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() },
}));
vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn(),
}));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));

import type { CloudClient } from "@aoagents/cloud-client";
import { DEFAULT_CONFIG, type ServerConfig } from "../config";
import { resolveSessionSource } from "./resolve";

const fakeClient = {} as CloudClient;

describe("resolveSessionSource", () => {
	describe("local environment", () => {
		it("returns a local source for a configured daemon", () => {
			const cfg: ServerConfig = { ...DEFAULT_CONFIG, host: "10.0.0.2", password: "pw" };
			expect(resolveSessionSource({ environment: "local", cfg })?.kind).toBe("local");
		});

		it("returns undefined when no daemon is configured", () => {
			expect(resolveSessionSource({ environment: "local", cfg: null })).toBeUndefined();
		});

		// The store rebuilds on every render; a new source object per render would
		// retrigger every effect keyed on it.
		it("returns the same instance for an unchanged config", () => {
			const cfg: ServerConfig = { ...DEFAULT_CONFIG, host: "10.0.0.2", password: "pw" };
			expect(resolveSessionSource({ environment: "local", cfg })).toBe(
				resolveSessionSource({ environment: "local", cfg }),
			);
		});

		it("returns the same instance for an equal-but-distinct config object", () => {
			const a: ServerConfig = { ...DEFAULT_CONFIG, host: "10.0.0.2", password: "pw" };
			const b: ServerConfig = { ...DEFAULT_CONFIG, host: "10.0.0.2", password: "pw" };
			expect(resolveSessionSource({ environment: "local", cfg: a })).toBe(
				resolveSessionSource({ environment: "local", cfg: b }),
			);
		});

		it("returns a new instance when the config changes", () => {
			const a: ServerConfig = { ...DEFAULT_CONFIG, host: "10.0.0.2", password: "pw" };
			const b: ServerConfig = { ...DEFAULT_CONFIG, host: "10.0.0.3", password: "pw" };
			expect(resolveSessionSource({ environment: "local", cfg: a })).not.toBe(
				resolveSessionSource({ environment: "local", cfg: b }),
			);
		});

		// sameServerConfig also compares the password, so a rotated credential
		// must rebuild the source rather than reuse a stale connection.
		it("returns a new instance when the password rotates", () => {
			const a: ServerConfig = { ...DEFAULT_CONFIG, host: "10.0.0.2", password: "old" };
			const b: ServerConfig = { ...DEFAULT_CONFIG, host: "10.0.0.2", password: "new" };
			expect(resolveSessionSource({ environment: "local", cfg: a })).not.toBe(
				resolveSessionSource({ environment: "local", cfg: b }),
			);
		});
	});

	describe("cloud environment", () => {
		it("returns undefined when signed out", () => {
			expect(
				resolveSessionSource({
					environment: "cloud",
					cfg: null,
					cloud: { client: fakeClient, signedIn: false, orgId: null },
				}),
			).toBeUndefined();
		});

		it("returns undefined when signed in but no org has resolved yet", () => {
			expect(
				resolveSessionSource({
					environment: "cloud",
					cfg: null,
					cloud: { client: fakeClient, signedIn: true, orgId: null },
				}),
			).toBeUndefined();
		});

		it("returns undefined when no cloud input is given", () => {
			expect(resolveSessionSource({ environment: "cloud", cfg: null })).toBeUndefined();
		});

		it("returns a cloud source when signed in and an org has resolved", () => {
			const result = resolveSessionSource({
				environment: "cloud",
				cfg: null,
				cloud: { client: fakeClient, signedIn: true, orgId: "org1" },
			});
			expect(result?.kind).toBe("cloud");
		});

		it("returns the same instance for an unchanged org id", () => {
			const cloud = { client: fakeClient, signedIn: true, orgId: "org1" };
			expect(resolveSessionSource({ environment: "cloud", cfg: null, cloud })).toBe(
				resolveSessionSource({ environment: "cloud", cfg: null, cloud }),
			);
		});

		it("returns a new instance when the org id changes", () => {
			const first = resolveSessionSource({
				environment: "cloud",
				cfg: null,
				cloud: { client: fakeClient, signedIn: true, orgId: "org1" },
			});
			const second = resolveSessionSource({
				environment: "cloud",
				cfg: null,
				cloud: { client: fakeClient, signedIn: true, orgId: "org2" },
			});
			expect(first).not.toBe(second);
		});
	});

	it("returns a new instance when switching environments even if the org id repeats", () => {
		const cloud = { client: fakeClient, signedIn: true, orgId: "org1" };
		const cloudSource = resolveSessionSource({ environment: "cloud", cfg: null, cloud });
		const cfg: ServerConfig = { ...DEFAULT_CONFIG, host: "10.0.0.2", password: "pw" };
		const localSource = resolveSessionSource({ environment: "local", cfg });
		expect(cloudSource).not.toBe(localSource);
		// The single memo slot is keyed on the *current* resolution, not a
		// per-environment cache — switching away and back rebuilds rather than
		// resurrecting the old instance. That is fine: it only costs one rebuild
		// per environment toggle, not per render.
		const cloudAgain = resolveSessionSource({ environment: "cloud", cfg: null, cloud });
		expect(cloudAgain?.kind).toBe("cloud");
		expect(resolveSessionSource({ environment: "cloud", cfg: null, cloud })).toBe(cloudAgain);
	});

	// The symmetric direction: 15b's switcher goes local -> cloud -> local, so
	// this must rebuild (not resurrect a stale local instance) just like the
	// cloud -> local -> cloud case above.
	it("returns a new instance when switching from local to cloud and back to local", () => {
		const cfg: ServerConfig = { ...DEFAULT_CONFIG, host: "10.0.0.2", password: "pw" };
		const cloud = { client: fakeClient, signedIn: true, orgId: "org1" };
		const firstLocal = resolveSessionSource({ environment: "local", cfg });
		expect(firstLocal?.kind).toBe("local");
		const cloudSource = resolveSessionSource({ environment: "cloud", cfg: null, cloud });
		expect(cloudSource?.kind).toBe("cloud");
		const secondLocal = resolveSessionSource({ environment: "local", cfg });
		expect(secondLocal?.kind).toBe("local");
		expect(secondLocal).not.toBe(firstLocal);
		// Stable from here on, same unchanged config.
		expect(resolveSessionSource({ environment: "local", cfg })).toBe(secondLocal);
	});
});
