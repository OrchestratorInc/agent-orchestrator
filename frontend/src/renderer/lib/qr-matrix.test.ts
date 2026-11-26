import { describe, expect, it } from "vitest";
import { encodeQr, modulesForVersion } from "./qr-matrix";

describe("encodeQr", () => {
	it("draws the three finder squares a scanner locks onto", () => {
		const { modules, isDark } = encodeQr("aomobile://pair#abc");
		const finderAt = (row0: number, col0: number) => {
			for (let r = 0; r < 7; r++) {
				for (let c = 0; c < 7; c++) {
					const ring = r === 0 || r === 6 || c === 0 || c === 6;
					const centre = r >= 2 && r <= 4 && c >= 2 && c <= 4;
					expect(isDark(row0 + r, col0 + c), `finder module ${r},${c}`).toBe(ring || centre);
				}
			}
		};
		finderAt(0, 0);
		finderAt(0, modules - 7);
		finderAt(modules - 7, 0);
	});

	it("is deterministic for the same payload", () => {
		const a = encodeQr("aomobile://pair#same");
		const b = encodeQr("aomobile://pair#same");
		expect(a.modules).toBe(b.modules);
		for (let r = 0; r < a.modules; r++) for (let c = 0; c < a.modules; c++) expect(a.isDark(r, c)).toBe(b.isDark(r, c));
	});

	it("picks the smallest version that fits when none is pinned", () => {
		const short = encodeQr("aomobile://pair#a");
		const long = encodeQr(`aomobile://pair#${"a".repeat(300)}`);
		expect(short.version).toBeLessThan(long.version);
		expect(short.modules).toBe(modulesForVersion(short.version));
	});

	// Pinning keeps codes of different payload lengths at one density, which is
	// what stops the pairing code resizing between generations.
	it("draws at the pinned version for any payload that fits", () => {
		expect(encodeQr("aomobile://pair#a", { version: 17 }).modules).toBe(modulesForVersion(17));
		expect(encodeQr(`aomobile://pair#${"a".repeat(400)}`, { version: 17 }).modules).toBe(modulesForVersion(17));
	});

	it("takes a bigger version than the pinned one rather than failing", () => {
		const matrix = encodeQr(`aomobile://pair#${"a".repeat(1200)}`, { version: 5 });
		expect(matrix.version).toBeGreaterThan(5);
		expect(matrix.modules).toBe(modulesForVersion(matrix.version));
	});
});
