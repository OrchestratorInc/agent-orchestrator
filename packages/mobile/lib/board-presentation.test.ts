import { describe, expect, it } from "vitest";
import { boardPresentation, boardFailure, boardStaleMessage, workerInteractionProps } from "./board-presentation";

describe("board presentation", () => {
	it.each(["local", "cloud"] as const)("shows a configured %s board", (environment) => {
		expect(boardPresentation(environment, true).state).toBe("board");
	});
	it("waits for the environment and distinguishes the two setup screens", () => {
		expect(boardPresentation(null, true).state).toBe("loading");
		expect(boardPresentation("cloud", false).state).toBe("cloud-unready");
		expect(boardPresentation("local", false).state).toBe("unpaired");
	});
	it("exposes only read-only Cloud workers and project browsing", () => {
		const view = boardPresentation("cloud", true);
		expect(view.interactionMode).toBe("read-only");
		expect(view.localControls).toBe(false);
	});
	it("preserves all Local controls and full worker interactions", () => {
		const view = boardPresentation("local", true);
		expect(view.interactionMode).toBe("full");
		expect(view.localControls).toBe(true);
	});
	it("does not expose Local controls before the environment is resolved", () => {
		expect(boardPresentation(null, false).localControls).toBe(false);
	});
});

describe("worker interaction boundary", () => {
	it("never constructs navigation or action handlers for a read-only row", () => {
		const props = workerInteractionProps("read-only", () => {
			throw new Error("Local actions must not be constructed for Cloud workers");
		});
		expect(props).toEqual({ interactionMode: "read-only" });
	});
	it("retains the real handlers for a full row", () => {
		let renamed = "";
		const props = workerInteractionProps("full", () => ({ onRename: (title: string) => { renamed = title; } }));
		if (props.interactionMode !== "full") throw new Error("Missing Local actions");
		props.onRename("My worker");
		expect(renamed).toBe("My worker");
	});
});

describe("board errors", () => {
	const target = { host: "mac.local", port: "8080", platform: "ios" };
	it.each([undefined, 401, 429, 500])("uses Cloud retry guidance regardless of LAN-like status %s", (status) => {
		const copy = boardFailure("cloud", status, target, true);
		expect(copy.title).toMatch(/Cloud/);
		expect(copy.message).toMatch(/try again/i);
		expect(copy.message).not.toMatch(/desktop|Wi-Fi|password|scan|mac.local/i);
		expect(copy.showLocalNetworkHint).toBe(false);
	});
	it("preserves Local password and rotated tunnel guidance", () => {
		expect(boardFailure("local", 401, target).message).toContain("Re-scan");
		expect(boardFailure("local", undefined, target, true).title).toContain("remote address changed");
		expect(boardFailure("local", undefined, target).showLocalNetworkHint).toBe(true);
	});
	it("offers Cloud retry without claiming a sync age from the Local clock", () => {
		expect(boardStaleMessage("cloud", true, "mac.local", "moments ago")).toBe("Couldn't refresh Cloud. Try again.");
	});
	it("preserves Local stale copy with or without a hostname and the healthy stale copy", () => {
		expect(boardStaleMessage("local", true, "mac.local", "2m ago")).toBe("Can't reach mac.local — showing data from 2m ago");
		expect(boardStaleMessage("local", true, undefined, "2m ago")).toBe("Can't reach your desktop — showing data from 2m ago");
		expect(boardStaleMessage("cloud", false, undefined, "2m ago")).toBe("Showing data from 2m ago");
	});
});
