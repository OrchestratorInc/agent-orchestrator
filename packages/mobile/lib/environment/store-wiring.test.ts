import { describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({
	default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() },
}));
vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn(),
}));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));

import { DEFAULT_CONFIG, type ServerConfig } from "../config";
import { sessionSourceForConfig } from "./resolve";

describe("sessionSourceForConfig", () => {
	it("returns a local source for a configured daemon", () => {
		const cfg: ServerConfig = { ...DEFAULT_CONFIG, host: "10.0.0.2", password: "pw" };
		expect(sessionSourceForConfig(cfg)?.kind).toBe("local");
	});

	it("returns undefined when no daemon is configured", () => {
		expect(sessionSourceForConfig(null)).toBeUndefined();
	});

	// The store rebuilds on every render; a new source object per render would
	// retrigger every effect keyed on it.
	it("returns the same instance for an unchanged config", () => {
		const cfg: ServerConfig = { ...DEFAULT_CONFIG, host: "10.0.0.2", password: "pw" };
		expect(sessionSourceForConfig(cfg)).toBe(sessionSourceForConfig(cfg));
	});

	it("returns the same instance for an equal-but-distinct config object", () => {
		const a: ServerConfig = { ...DEFAULT_CONFIG, host: "10.0.0.2", password: "pw" };
		const b: ServerConfig = { ...DEFAULT_CONFIG, host: "10.0.0.2", password: "pw" };
		expect(sessionSourceForConfig(a)).toBe(sessionSourceForConfig(b));
	});

	it("returns a new instance when the config changes", () => {
		const a: ServerConfig = { ...DEFAULT_CONFIG, host: "10.0.0.2", password: "pw" };
		const b: ServerConfig = { ...DEFAULT_CONFIG, host: "10.0.0.3", password: "pw" };
		expect(sessionSourceForConfig(a)).not.toBe(sessionSourceForConfig(b));
	});
});
