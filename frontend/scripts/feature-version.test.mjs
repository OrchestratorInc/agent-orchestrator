// @vitest-environment node
import { describe, it, expect } from "vitest";
import semver from "semver";
import { computeFeatureVersion, parseFeatureBuild } from "./feature-version.mjs";

const now = new Date("2025-07-06T12:00:00.000Z");
const nowIso = now.toISOString();
const pr = 2270;
const sha = "abc1234";
const base = "0.2.0";

describe("computeFeatureVersion", () => {
	it("formats a UTC-timestamped feature prerelease with sha build metadata", () => {
		expect(computeFeatureVersion(base, pr, sha, nowIso)).toBe("0.2.0-pr2270.202507061200+abc1234");
	});

	it("does NOT bump the patch version (base used as-is)", () => {
		const version = computeFeatureVersion("0.2.3", pr, sha, nowIso);
		expect(version.startsWith("0.2.3-")).toBe(true);
	});

	it("strips a v / desktop-v tag prefix from baseVersion", () => {
		expect(computeFeatureVersion("v0.2.0", pr, sha, nowIso)).toBe("0.2.0-pr2270.202507061200+abc1234");
		expect(computeFeatureVersion("desktop-v0.2.0", pr, sha, nowIso)).toBe("0.2.0-pr2270.202507061200+abc1234");
	});

	it("CRITICAL: semver.prerelease(version)[0] === 'pr<N>' (update channel filter depends on this)", () => {
		const version = computeFeatureVersion(base, pr, sha, nowIso);
		const prerelease = semver.prerelease(version);
		expect(prerelease).not.toBeNull();
		expect(prerelease[0]).toBe(`pr${pr}`);
	});

	it("timestamp is exactly 12 numeric digits (YYYYMMDDHHMM)", () => {
		const version = computeFeatureVersion(base, pr, sha, nowIso);
		const prerelease = semver.prerelease(version);
		const tsStr = String(prerelease[1]);
		expect(tsStr).toMatch(/^\d{12}$/);
	});

	it("build metadata is the shortSha", () => {
		const version = computeFeatureVersion(base, pr, sha, nowIso);
		expect(version.endsWith(`+${sha}`)).toBe(true);
	});

	it("orders monotonically by timestamp for the same base and pr", () => {
		const earlier = computeFeatureVersion(base, pr, sha, "2025-07-06T12:00:00Z");
		const later = computeFeatureVersion(base, pr, sha, "2025-07-06T13:00:00Z");
		const stripMeta = (v) => v.split("+")[0];
		expect(semver.gt(stripMeta(later), stripMeta(earlier))).toBe(true);
	});
});

describe("parseFeatureBuild", () => {
	it("round-trips pr and base from a full feature version", () => {
		const version = computeFeatureVersion(base, pr, sha, nowIso);
		const result = parseFeatureBuild(version);
		expect(result).not.toBeNull();
		expect(result.pr).toBe(pr);
		expect(result.base).toBe(base);
	});

	it("tolerates a leading v", () => {
		const result = parseFeatureBuild("v0.2.0-pr2270.202507061200+abc1234");
		expect(result).not.toBeNull();
		expect(result.pr).toBe(2270);
		expect(result.base).toBe("0.2.0");
	});

	it("works without build metadata suffix", () => {
		const result = parseFeatureBuild("0.2.0-pr2270.202507061200");
		expect(result).not.toBeNull();
		expect(result.pr).toBe(2270);
	});

	it("returns null for a plain stable version", () => {
		expect(parseFeatureBuild("0.2.0")).toBeNull();
	});

	it("returns null for a nightly version", () => {
		expect(parseFeatureBuild("0.2.1-nightly.202507061200")).toBeNull();
		expect(parseFeatureBuild("0.2.1-nightly.202507061200+abc1234")).toBeNull();
	});
});
