import { describe, expect, it } from "vitest";
import {
	chipTone,
	formatResourceBytes,
	largestSession,
	pressureState,
	resourceSuggestion,
	stableResourceOrder,
	type ResourceSessionFacts,
} from "./resource-presentation";

const GB = 1024 ** 3;
const MB = 1000 ** 2;

function machine(availableGiB: number, pressureRaw: number, pressureSource = "psi") {
	return { totalBytes: 16 * GB, availableBytes: availableGiB * GB, pressureRaw, pressureSource };
}

function session(over: Partial<ResourceSessionFacts> & { id: string }): ResourceSessionFacts {
	return { title: over.id, rssBytes: 200 * MB, working: false, idleSeconds: 0, paused: false, pausable: true, ...over };
}

describe("pressureState", () => {
	it("reads PSI stall time when the kernel offers it", () => {
		expect(pressureState(machine(1, 0.5))).toBe("fine");
		expect(pressureState(machine(1, 5))).toBe("tight_soon");
		expect(pressureState(machine(1, 20.1))).toBe("tight");
	});

	it("falls back to available percent elsewhere", () => {
		expect(pressureState(machine(8, 50, "available_pct"))).toBe("fine");
		expect(pressureState(machine(3, 81, "available_pct"))).toBe("tight_soon");
		expect(pressureState(machine(1, 94, "available_pct"))).toBe("tight");
	});
});

describe("resourceSuggestion", () => {
	const m = { totalBytes: 16 * GB, availableBytes: 2 * GB };

	it("says nothing while fine, whatever the sessions look like", () => {
		expect(resourceSuggestion("fine", m, 10 * GB, [session({ id: "a", idleSeconds: 99_999 })])).toEqual({ kind: "none" });
	});

	it("is honest when other apps hold the memory", () => {
		expect(resourceSuggestion("tight", m, 1 * GB, [session({ id: "a", idleSeconds: 99_999 })])).toEqual({ kind: "other_apps", aoBytes: 1 * GB });
	});

	it("points at the biggest unpaused session", () => {
		const out = resourceSuggestion("tight", m, 8 * GB, [
			session({ id: "small", working: true, rssBytes: 300 * MB }),
			session({ id: "big", title: "build-indexer", working: true, rssBytes: 3 * GB }),
			session({ id: "paused", paused: true, rssBytes: 9 * GB }),
		]);
		expect(out).toEqual({ kind: "pause_largest", sessionId: "big", title: "build-indexer", rssBytes: 3 * GB });
	});
});

describe("chipTone", () => {
	const idle = session({ id: "idle" });
	const busy = session({ id: "busy", working: true, rssBytes: 3 * GB });

	it("is grey unless the card is part of the fix", () => {
		expect(chipTone("fine", idle, "busy")).toBe("neutral");
		expect(chipTone("tight_soon", busy, "busy")).toBe("neutral");
		expect(chipTone("tight_soon", idle, "busy")).toBe("warning");
		expect(chipTone("tight", busy, "busy")).toBe("critical");
		expect(chipTone("tight", session({ id: "p", paused: true }), "busy")).toBe("neutral");
	});

	it("names the largest unpaused session", () => {
		expect(largestSession([idle, busy, session({ id: "huge", paused: true, rssBytes: 9 * GB })])).toBe("busy");
	});
});

describe("formatResourceBytes", () => {
	it("shows whole megabytes and switches to GB at a thousand", () => {
		expect(formatResourceBytes(0.3 * MB)).toBe("1 MB");
		expect(formatResourceBytes(238.4 * MB)).toBe("238 MB");
		expect(formatResourceBytes(994 * MB)).toBe("994 MB");
		expect(formatResourceBytes(999.6 * MB)).toBe("1.0 GB");
		expect(formatResourceBytes(2.25 * 1000 ** 3)).toBe("2.3 GB");
	});
});

describe("stableResourceOrder", () => {
	it("sorts fresh rows by size but keeps a near-tie in its old order", () => {
		const rows = [
			{ id: "a", rssBytes: 500 },
			{ id: "b", rssBytes: 520 },
		];
		expect(stableResourceOrder([], rows).map((r) => r.id)).toEqual(["b", "a"]);
		expect(stableResourceOrder(["b", "a"], [{ id: "a", rssBytes: 530 }, { id: "b", rssBytes: 520 }]).map((r) => r.id)).toEqual(["b", "a"]);
		expect(stableResourceOrder(["b", "a"], [{ id: "a", rssBytes: 900 }, { id: "b", rssBytes: 520 }]).map((r) => r.id)).toEqual(["a", "b"]);
	});

	it("drops rows that vanished and appends newcomers", () => {
		expect(stableResourceOrder(["gone", "b"], [{ id: "b", rssBytes: 5 }, { id: "new", rssBytes: 1 }]).map((r) => r.id)).toEqual(["b", "new"]);
	});
});
