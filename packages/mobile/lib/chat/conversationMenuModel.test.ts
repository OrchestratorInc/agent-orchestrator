import { describe, expect, it } from "vitest";
import { conversationMenuGroups, type ConversationMenuState } from "./conversationMenuModel";

const base: ConversationMenuState = {
	canPin: true,
	canCompact: true,
	canDelete: true,
	pinned: false,
	openingShell: false,
	compacting: false,
	turnInFlight: false,
};

const layout = (state: ConversationMenuState) => conversationMenuGroups(state).map((group) => [group.title, group.items.map((item) => item.label)]);

describe("conversationMenuGroups", () => {
	// Only these seven, each in its group, on both platforms.
	it("offers Terminal, Preview, Artifacts, Pin, PR Review, Compact and Archive in four groups", () => {
		expect(layout({ ...base, artifactCount: 2, hasPullRequest: true })).toEqual([
			["Open", ["Shell", "Preview", "Artifacts"]],
			["Worker", ["Pin", "PR Review"]],
			["Context", ["Compact"]],
			["Session", ["Archive"]],
		]);
	});

	// PR Review draws the app's pull-request glyph on both platforms, as the
	// board's header does; nothing else replaces its symbol.
	it("marks PR Review, and only it, for the app's pull-request glyph", () => {
		const items = conversationMenuGroups({ ...base, artifactCount: 2, hasPullRequest: true }).flatMap((group) => group.items);
		expect(items.filter((item) => item.glyph).map((item) => [item.label, item.glyph])).toEqual([["PR Review", "pullRequest"]]);
	});

	// On an empty session both led to an empty page.
	it("offers Artifacts and PR Review only once there is something to open", () => {
		expect(layout(base)).toEqual([
			["Open", ["Shell", "Preview"]],
			["Worker", ["Pin"]],
			["Context", ["Compact"]],
			["Session", ["Archive"]],
		]);
		expect(layout({ ...base, artifactCount: 0 })[0][1]).not.toContain("Artifacts");
	});

	// Only where the agent can carry the conversation on in Terminal UI, and
	// just before Archive.
	it("offers Switch to Terminal UI before Archive when the agent supports it", () => {
		expect(layout({ ...base, canSwitchToTerminal: true }).at(-1)).toEqual(["Session", ["Switch to Terminal UI", "Archive"]]);
		expect(layout({ ...base, canSwitchToTerminal: true, canDelete: false }).at(-1)).toEqual(["Session", ["Switch to Terminal UI"]]);
		const switching = conversationMenuGroups({ ...base, canSwitchToTerminal: true, switchingToTerminal: true }).at(-1)!.items[0];
		expect(switching).toMatchObject({ action: "switch_tui", label: "Switching to Terminal UI…", disabled: true });
	});

	it("drops what the session cannot do, and never shows an empty group", () => {
		expect(layout({ ...base, canPin: false, canCompact: false, canDelete: false })).toEqual([
			["Open", ["Shell", "Preview"]],
		]);
		expect(layout({ ...base, canPin: false, canCompact: false, canDelete: false, hasPullRequest: true })).toEqual([
			["Open", ["Shell", "Preview"]],
			["Worker", ["PR Review"]],
		]);
	});

	it("draws the context in use as a ring on Compact, and says it under the label", () => {
		const compact = conversationMenuGroups({ ...base, contextPercent: 42 })[2].items[0];
		expect(compact).toMatchObject({ action: "compact", meter: 42, subtitle: "42% of context used" });
		const unknown = conversationMenuGroups(base)[2].items[0];
		expect(unknown.meter).toBeUndefined();
		expect(unknown.subtitle).toBeUndefined();
	});

	it("says what is running and holds it until it finishes", () => {
		const groups = conversationMenuGroups({ ...base, openingShell: true, compacting: true, pinned: true, previewReady: true });
		expect(groups[0].items[0]).toMatchObject({ label: "Opening shell…", disabled: true });
		expect(groups[0].items[1].subtitle).toBe("Ready to open");
		expect(groups[1].items[0].label).toBe("Unpin");
		expect(groups[2].items[0]).toMatchObject({ label: "Compacting…", disabled: true });
		expect(conversationMenuGroups({ ...base, turnInFlight: true })[2].items[0].disabled).toBe(true);
	});

	it("counts the artifacts under their label", () => {
		expect(conversationMenuGroups({ ...base, artifactCount: 3 })[0].items[2]).toMatchObject({ action: "artifacts", subtitle: "3 files" });
		expect(conversationMenuGroups({ ...base, artifactCount: 1 })[0].items[2].subtitle).toBe("1 file");
	});

	it("draws Archive in red with its hint", () => {
		expect(conversationMenuGroups(base)[3].items[0]).toMatchObject({ action: "delete", destructive: true });
		expect(conversationMenuGroups(base)[3].items[0].subtitle).toBeTruthy();
	});
});
