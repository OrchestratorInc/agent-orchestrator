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
	// Only these six, each in its group, on both platforms.
	it("offers Terminal, Preview, Pin, PR Review, Compact and Archive in four groups", () => {
		expect(layout(base)).toEqual([
			["Open", ["Terminal", "Preview"]],
			["Worker", ["Pin", "PR Review"]],
			["Context", ["Compact"]],
			["Session", ["Archive"]],
		]);
	});

	it("drops what the session cannot do, and never shows an empty group", () => {
		expect(layout({ ...base, canPin: false, canCompact: false, canDelete: false })).toEqual([
			["Open", ["Terminal", "Preview"]],
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
		expect(groups[0].items[0]).toMatchObject({ label: "Opening terminal…", disabled: true });
		expect(groups[0].items[1].subtitle).toBe("Ready to open");
		expect(groups[1].items[0].label).toBe("Unpin");
		expect(groups[2].items[0]).toMatchObject({ label: "Compacting…", disabled: true });
		expect(conversationMenuGroups({ ...base, turnInFlight: true })[2].items[0].disabled).toBe(true);
	});

	it("draws Archive in red with its hint", () => {
		expect(conversationMenuGroups(base)[3].items[0]).toMatchObject({ action: "delete", destructive: true });
		expect(conversationMenuGroups(base)[3].items[0].subtitle).toBeTruthy();
	});
});
