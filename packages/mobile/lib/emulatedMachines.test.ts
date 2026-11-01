import { describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(async () => null), setItem: vi.fn(async () => {}) } }));

import { emulatedHostSnapshots, isEmulatedHost } from "./emulatedMachines";

describe("emulated machines", () => {
	it("are a fixed crowd of fake machines, each marked as emulated", () => {
		const fakes = emulatedHostSnapshots();
		expect(fakes.length).toBeGreaterThan(1);
		expect(emulatedHostSnapshots()).toBe(fakes);
		for (const fake of fakes) expect(isEmulatedHost(fake.hostId)).toBe(true);
		expect(isEmulatedHost("dev")).toBe(false);
		expect(new Set(fakes.map((fake) => fake.appearance.symbol)).size).toBe(fakes.length);
	});

	// A fake that read as connected could become the board's live machine or a
	// New task target; one that read as loading would hold the board's spinner.
	it("never read as connected, loading or reachable", () => {
		for (const fake of emulatedHostSnapshots()) {
			expect(fake.connection).not.toBe("open");
			expect(fake.loading).toBe(false);
			expect(fake.config).toBeNull();
		}
	});
});
