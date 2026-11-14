// @vitest-environment node
import { describe, it, expect } from "vitest";
import { computeNightlyVersion } from "./nightly-version.mjs";

const now = new Date("2026-06-27T03:00:00.000Z");

describe("computeNightlyVersion", () => {
	it("bumps the patch and formats a UTC-timestamped nightly prerelease with sha build metadata", () => {
		expect(computeNightlyVersion("0.10.3", now, "ab12cd3")).toBe("0.10.4-nightly.202606270300+ab12cd3");
	});

	it("strips a v / desktop-v tag prefix", () => {
		expect(computeNightlyVersion("v0.10.3", now, "ab12cd3")).toBe("0.10.4-nightly.202606270300+ab12cd3");
		expect(computeNightlyVersion("desktop-v0.10.3", now, "ab12cd3")).toBe("0.10.4-nightly.202606270300+ab12cd3");
	});

	it("orders monotonically by timestamp for the same base", () => {
		const earlier = computeNightlyVersion("0.10.3", new Date("2026-06-27T03:00:00Z"), "aaaaaaa");
		const later = computeNightlyVersion("0.10.3", new Date("2026-06-27T04:00:00Z"), "bbbbbbb");
		expect(later > earlier).toBe(true);
	});

	it("rejects a non-semver base tag", () => {
		expect(() => computeNightlyVersion("not-a-version", now, "ab12cd3")).toThrow();
	});
});
