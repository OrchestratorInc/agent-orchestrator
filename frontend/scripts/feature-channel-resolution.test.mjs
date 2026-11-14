// @vitest-environment node

import { describe, it, expect } from "vitest";
import semver from "semver";

const TAGS = [
	"v0.3.0-nightly.202607060000",
	"v0.2.0-pr2270.202607061200",
	"v0.2.0",
];

function selectTag(tags, channel) {
	for (const tag of tags) {
		if (!semver.valid(tag)) continue;
		const hrefChannel = semver.prerelease(tag)?.[0] ?? null;
		const isNextPreRelease = hrefChannel && hrefChannel === channel;
		if (isNextPreRelease) return tag;
	}
	return null;
}

describe("electron-updater GitHubProvider channel-selection invariant", () => {
	describe("channel 'pr2270'", () => {
		it("selects the pr2270 feature-build tag", () => {
			expect(selectTag(TAGS, "pr2270")).toBe("v0.2.0-pr2270.202607061200");
		});

		it("does NOT select the newer nightly tag (no hijack)", () => {
			const selected = selectTag(TAGS, "pr2270");
			expect(selected).not.toBe("v0.3.0-nightly.202607060000");
		});

		it("does NOT select the stable tag", () => {
			const selected = selectTag(TAGS, "pr2270");
			expect(selected).not.toBe("v0.2.0");
		});

		it("semver.prerelease(tag)[0] for the pr2270 tag is exactly 'pr2270'", () => {
			expect(semver.prerelease("v0.2.0-pr2270.202607061200")?.[0]).toBe("pr2270");
		});
	});

	describe("channel 'nightly'", () => {
		it("selects the nightly tag", () => {
			expect(selectTag(TAGS, "nightly")).toBe("v0.3.0-nightly.202607060000");
		});

		it("does NOT select the pr2270 build (no cross-channel hijack)", () => {
			const selected = selectTag(TAGS, "nightly");
			expect(selected).not.toBe("v0.2.0-pr2270.202607061200");
		});

		it("does NOT select the stable tag", () => {
			const selected = selectTag(TAGS, "nightly");
			expect(selected).not.toBe("v0.2.0");
		});

		it("semver.prerelease(tag)[0] for the nightly tag is exactly 'nightly'", () => {
			expect(semver.prerelease("v0.3.0-nightly.202607060000")?.[0]).toBe("nightly");
		});
	});

	describe("channel 'latest' (stable users)", () => {
		it("returns null — stable users are on no prerelease channel, nothing matches", () => {
			expect(selectTag(TAGS, "latest")).toBeNull();
		});
	});

	describe("isolation: no tag is selected by the wrong channel", () => {
		it("pr2270 channel does not match nightly prerelease[0]", () => {
			expect(semver.prerelease("v0.3.0-nightly.202607060000")?.[0]).not.toBe("pr2270");
		});

		it("nightly channel does not match pr2270 prerelease[0]", () => {
			expect(semver.prerelease("v0.2.0-pr2270.202607061200")?.[0]).not.toBe("nightly");
		});

		it("stable tag has no prerelease component at all", () => {
			expect(semver.prerelease("v0.2.0")).toBeNull();
		});
	});
});
