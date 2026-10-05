import { describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({
	default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() },
}));
vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn(),
}));

import { DEFAULT_CONFIG, type ServerConfig } from "../config";
import type { SourceRef } from "./scopedBoard";
import { localConfigForSource } from "./sourceResolution";

const cfg = (hostId: string): ServerConfig => ({
	...DEFAULT_CONFIG,
	hostId,
	host: `${hostId}.local`,
	password: `${hostId}-secret`,
});

describe("local source resolution", () => {
	it("uses a non-selected host's own verified connection", () => {
		const selected = cfg("mac-a");
		const other = cfg("mac-b");
		const configs = new Map([["mac-a", selected], ["mac-b", other]]);
		const requested: SourceRef = { kind: "local", id: "mac-b" };
		expect(localConfigForSource(requested, (id) => configs.get(id) ?? null)).toBe(other);
	});

	it("rejects an endpoint that answered as a different host", () => {
		const requested: SourceRef = { kind: "local", id: "mac-b" };
		expect(localConfigForSource(requested, () => cfg("mac-a"))).toBeNull();
	});

	it("never resolves a Cloud ref to a Local bearer", () => {
		const requested: SourceRef = { kind: "cloud", id: "org-a" };
		expect(localConfigForSource(requested, () => cfg("mac-a"))).toBeNull();
	});
});
