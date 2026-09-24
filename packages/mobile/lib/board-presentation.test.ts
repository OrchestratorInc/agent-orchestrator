import { describe, expect, it } from "vitest";
import {
	boardPresentation,
	boardFailure,
	boardStaleMessage,
	canUseOrchestratorAction,
	projectDetailState,
	workerInteractionProps,
} from "./board-presentation";

describe("board presentation", () => {
	it.each(["local", "cloud"] as const)("shows a configured %s board", (environment) => {
		expect(boardPresentation(environment, true).state).toBe("board");
	});
	it("waits for the environment and distinguishes the two setup screens", () => {
		expect(boardPresentation(null, true).state).toBe("loading");
		expect(boardPresentation("cloud", false).state).toBe("cloud-unready");
		expect(boardPresentation("local", false).state).toBe("unpaired");
	});
	it("lets Cloud workers open without exposing Local controls", () => {
		const view = boardPresentation("cloud", true);
		expect(view.interactionMode).toBe("open-only");
		expect(view.localControls).toBe(false);
		expect(view.spawnControls).toBe(true);
	});
	it("preserves all Local controls and full worker interactions", () => {
		const view = boardPresentation("local", true);
		expect(view.interactionMode).toBe("full");
		expect(view.localControls).toBe(true);
		expect(view.spawnControls).toBe(true);
	});
	it("does not expose Local controls before the environment is resolved", () => {
		expect(boardPresentation(null, false).localControls).toBe(false);
		expect(boardPresentation(null, true).spawnControls).toBe(false);
		expect(boardPresentation("local", false).spawnControls).toBe(false);
		expect(boardPresentation("cloud", false).spawnControls).toBe(false);
	});
	it("shows sidebar sessions for configured Local and Cloud boards", () => {
		expect(boardPresentation("local", true).showSidebarSessions).toBe(true);
		expect(boardPresentation("cloud", true).showSidebarSessions).toBe(true);
		expect(boardPresentation("local", false).showSidebarSessions).toBe(false);
		expect(boardPresentation("cloud", false).showSidebarSessions).toBe(false);
		expect(boardPresentation(null, true).showSidebarSessions).toBe(false);
	});

	it("lets the Cloud project button open or create an orchestrator from one action", () => {
		expect(canUseOrchestratorAction("cloud", "open")).toBe(true);
		expect(canUseOrchestratorAction("cloud", "start")).toBe(true);
		expect(canUseOrchestratorAction("cloud", "resume")).toBe(true);
	});

	it("keeps every orchestrator action available locally", () => {
		for (const action of ["open", "start", "resume"] as const) {
			expect(canUseOrchestratorAction("local", action)).toBe(true);
		}
		expect(canUseOrchestratorAction(null, "open")).toBe(false);
	});
});

describe("worker interaction boundary", () => {
	it("does not construct Local mutation handlers for an open-only row", () => {
		const props = workerInteractionProps("open-only", () => {
			throw new Error("Local actions must not be constructed for Cloud workers");
		});
		expect(props).toEqual({ interactionMode: "open-only" });
	});
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

describe("project detail availability", () => {
	it("returns Cloud setup after source removal while a project is open", () => {
		expect(projectDetailState(boardPresentation("cloud", true).state, true, false, false)).toBe("project");
		expect(projectDetailState(boardPresentation("cloud", false).state, false, false, false)).toBe("cloud-unready");
	});
	it("hides retained project data until environment and Cloud readiness resolve", () => {
		expect(projectDetailState("loading", true, false, false)).toBe("loading");
		expect(projectDetailState("cloud-unready", true, false, false)).toBe("cloud-unready");
	});
	it("preserves loaded, missing, loading and Cloud error presentations on a ready board", () => {
		expect(projectDetailState("board", true, false, true)).toBe("project");
		expect(projectDetailState("board", false, false, false)).toBe("not-found");
		expect(projectDetailState("board", false, true, false)).toBe("loading");
		expect(projectDetailState("board", false, false, true)).toBe("cloud-error");
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
		expect(boardFailure("local", undefined, target, true).title).toContain("desktop's address changed");
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
