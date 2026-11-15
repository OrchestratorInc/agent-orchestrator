import { describe, expect, it } from "vitest";
import { canStackOn, stackBlocker, stackLayersToLand, type PRStack, type PRStackLayer } from "./pr-stack";

const layer = (number: number, state: PRStackLayer["state"], extra: Partial<PRStackLayer> = {}): PRStackLayer => ({
	ci: "passing", headBranch: `b${number}`, headSha: `h${number}`, mergeability: "mergeable", number, reviewDecision: "none", state, title: `L${number}`, url: "u", ...extra,
});
const stack = (layers: PRStackLayer[], open = true): PRStack => ({ baseBranch: "main", layers, number: 1, open, url: "s" });

describe("stackLayersToLand", () => {
	it("takes the unmerged layers up to the PR, bottom first", () => {
		const layers = [layer(1, "merged"), layer(2, "open"), layer(3, "open"), layer(4, "open")];
		expect(stackLayersToLand(stack(layers), 3)?.map(({ number }) => number)).toEqual([2, 3]);
	});

	it("leaves a PR outside an open stack to merge alone", () => {
		expect(stackLayersToLand(undefined, 3)).toBeUndefined();
		expect(stackLayersToLand(stack([layer(1, "open")], false), 1)).toBeUndefined();
		expect(stackLayersToLand(stack([layer(1, "open")]), 3)).toBeUndefined();
	});
});

describe("stackBlocker", () => {
	it("names the first lower layer in the way, not this PR", () => {
		const layers = [layer(1, "open"), layer(2, "open", { ci: "failing" }), layer(3, "open", { ci: "failing" })];
		expect(stackBlocker(layers, 3)).toEqual({ number: 2, reason: "checks" });
		expect(stackBlocker(layers.slice(0, 1), 1)).toBeUndefined();
	});

	it("waits on a layer whose facts haven't arrived", () => {
		expect(stackBlocker([layer(1, "open", { ci: undefined }), layer(2, "open")], 2)).toEqual({ number: 1, reason: "checking" });
	});

	it("doesn't wait on a layer nothing checks", () => {
		expect(stackBlocker([layer(1, "open", { ci: "unknown", noChecks: true }), layer(2, "open")], 2)).toBeUndefined();
		expect(stackBlocker([layer(1, "open", { ci: "unknown" }), layer(2, "open")], 2)).toEqual({ number: 1, reason: "checking" });
	});
});

describe("canStackOn", () => {
	it("takes a new layer on the top of an open stack, or with no stack", () => {
		const layers = [layer(1, "open"), layer(2, "open")];
		expect(canStackOn(undefined, 9)).toBe(true);
		expect(canStackOn(stack(layers), 2)).toBe(true);
		expect(canStackOn(stack(layers), 1)).toBe(false);
		expect(canStackOn(stack(layers, false), 2)).toBe(false);
	});
});
