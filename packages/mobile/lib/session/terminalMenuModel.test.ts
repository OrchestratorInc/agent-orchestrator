import { describe, expect, it } from "vitest";
import { terminalMenuSections, type TerminalMenuState } from "./terminalMenuModel";

const base: TerminalMenuState = {
	agentSession: true,
	chatReady: true,
	checkingChat: false,
	switchingToChat: false,
	artifactCount: 0,
	canZoom: true,
	close: "archive",
};

const layout = (state: TerminalMenuState) => terminalMenuSections(state).map((section) => section.items.map((item) => item.label));
const first = (state: Partial<TerminalMenuState>) => terminalMenuSections({ ...base, ...state })[0].items[0];

describe("terminalMenuSections", () => {
	it("leads with Chat and what the session opens, then the view, then Archive", () => {
		expect(terminalMenuSections(base).map((section) => section.kind)).toEqual(["session", "view", "close"]);
		expect(layout({ ...base, artifactCount: 2 })).toEqual([
			["Switch to Chat", "Preview", "Files", "Artifacts (2)"],
			["Smaller", "Larger", "Fit width"],
			["Archive session"],
		]);
	});

	it("offers a shell or a reviewer only its view and closing it", () => {
		expect(layout({ ...base, agentSession: false, close: "shell" })).toEqual([["Smaller", "Larger", "Fit width"], ["Close shell"]]);
		expect(layout({ ...base, agentSession: false, close: "reviewer" }).at(-1)).toEqual(["Stop reviewer"]);
	});

	it("drops the view and Archive from a dead session, and never leaves an empty section", () => {
		expect(layout({ ...base, canZoom: false, close: undefined })).toEqual([["Switch to Chat", "Preview", "Files"]]);
		expect(terminalMenuSections({ ...base, agentSession: false, canZoom: false, close: undefined })).toEqual([]);
	});

	it("still offers Chat before the daemon says yes, and holds it while checking or switching", () => {
		expect(first({ chatReady: false })).toMatchObject({ action: "switch_chat", subtitle: "Not ready yet", disabled: false });
		expect(first({ checkingChat: true })).toMatchObject({ label: "Checking Chat…", disabled: true });
		expect(first({ switchingToChat: true })).toMatchObject({ label: "Switching to Chat…", disabled: true });
	});

	it("says when a preview is ready", () => {
		expect(terminalMenuSections({ ...base, previewReady: true })[0].items[1].subtitle).toBe("Ready to open");
	});

	it("draws closing the terminal in red", () => {
		for (const close of ["archive", "shell", "reviewer"] as const) {
			expect(terminalMenuSections({ ...base, close }).at(-1)!.items[0].destructive).toBe(true);
		}
	});
});
