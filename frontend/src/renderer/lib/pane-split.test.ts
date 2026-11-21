import { describe, expect, it } from "vitest";
import {
	edgeAt,
	expandGroupTabOrder,
	groupTabKey,
	halfOf,
	insertBeside,
	layoutBoxes,
	layoutCss,
	MAX_STORED_SPLIT_SESSIONS,
	parseStoredGroups,
	leafKeys,
	reconcileGroups,
	removeLeaf,
	splitBeside,
	ungroupById,
	trimStoredGroups,
	setRatioAt,
	type SplitNode,
	type TabGroup,
} from "./pane-split";

const leaf = (key: string): SplitNode => ({ type: "leaf", key });
const twoColumns: SplitNode = { type: "split", dir: "row", ratio: 0.5, a: leaf("chat"), b: leaf("shell") };

describe("group tab order", () => {
	it("keeps the members together at the position where their group was reordered", () => {
		const group: TabGroup = { id: "g", root: twoColumns };
		expect(expandGroupTabOrder(["file", groupTabKey(group), "other"], [group])).toEqual(["file", "chat", "shell", "other"]);
	});
	it("leaves the pinned session out of the auxiliary order", () => {
		const group: TabGroup = { id: "g", root: { ...twoColumns, a: leaf("session") } };
		expect(expandGroupTabOrder([groupTabKey(group), "file"], [group])).toEqual(["shell", "file"]);
	});
});

describe("insertBeside", () => {
	it("splits only the pane the tab was dropped on", () => {
		// A third tab dropped on the bottom of the left column halves that column.
		const root = insertBeside(twoColumns, "chat", "file", "bottom");
		expect(root).toEqual({
			type: "split",
			dir: "row",
			ratio: 0.5,
			a: { type: "split", dir: "column", ratio: 0.5, a: leaf("chat"), b: leaf("file") },
			b: leaf("shell"),
		});
		expect(leafKeys(root)).toEqual(["chat", "file", "shell"]);
	});

	it("puts the new tab first for a left or top drop", () => {
		expect(insertBeside(leaf("chat"), "chat", "shell", "left")).toEqual({
			type: "split", dir: "row", ratio: 0.5, a: leaf("shell"), b: leaf("chat"),
		});
	});
});

describe("removeLeaf", () => {
	it("collapses the split it leaves behind", () => {
		const root = insertBeside(twoColumns, "chat", "file", "bottom");
		expect(removeLeaf(root, "file")).toEqual(twoColumns);
		expect(removeLeaf(twoColumns, "shell")).toEqual(leaf("chat"));
	});
});

describe("reconcileGroups", () => {
	const group: TabGroup = { id: "g", root: insertBeside(twoColumns, "chat", "file", "bottom") };

	it("keeps unchanged groups identical", () => {
		const groups = [group];
		expect(reconcileGroups(groups, new Set(["chat", "file", "shell"]))).toBe(groups);
	});

	it("drops a closed member", () => {
		expect(reconcileGroups([group], new Set(["chat", "shell"]))).toEqual([{ id: "g", root: twoColumns }]);
	});

	it("dissolves a group left with one tab", () => {
		expect(reconcileGroups([{ id: "g", root: twoColumns }], new Set(["chat"]))).toEqual([]);
	});
});

describe("layout", () => {
	it("gives every pane its box", () => {
		const root = setRatioAt(insertBeside(twoColumns, "chat", "file", "bottom"), "r", 0.25);
		const { leaves } = layoutBoxes(root);
		expect(leaves.get("chat")).toEqual({ x: 0, y: 0, w: 0.25, h: 0.5 });
		expect(leaves.get("file")).toEqual({ x: 0, y: 0.5, w: 0.25, h: 0.5 });
		expect(leaves.get("shell")).toEqual({ x: 0.25, y: 0, w: 0.75, h: 1 });
	});

	it("reads ratios from variables so a divider drag needs no render", () => {
		const { leaves, dividers, vars } = layoutCss(twoColumns);
		expect(vars).toEqual({ "--split-r": 0.5 });
		expect(leaves.get("chat")?.width).toBe("calc(((1) * var(--split-r)) * 100%)");
		expect(dividers).toHaveLength(1);
		expect(dividers[0]?.dir).toBe("row");
	});
});

describe("drop zones", () => {
	const rect = { left: 100, top: 50, width: 800, height: 600 };

	it("picks a side half across most of a pane and top or bottom near those edges", () => {
		expect(edgeAt(rect, 200, 350)).toBe("left");
		expect(edgeAt(rect, 800, 350)).toBe("right");
		expect(edgeAt(rect, 500, 60)).toBe("top");
		expect(edgeAt(rect, 500, 640)).toBe("bottom");
		expect(edgeAt(rect, 50, 300)).toBeNull();
	});

	it("previews the half the tab would take", () => {
		expect(halfOf(rect, "bottom")).toEqual({ left: 100, top: 350, width: 800, height: 300 });
	});
});

describe("stored split layouts", () => {
	const leaf = (key: string) => ({ type: "leaf", key });
	const pair = (a: string, b: string, ratio = 0.5) => ({ type: "split", dir: "row", ratio, a: leaf(a), b: leaf(b) });

	it("reads back a saved layout", () => {
		const saved = { "sess-1": [{ id: "g1", root: pair("session", "sh-1", 0.4), focus: "sh-1" }] };
		expect(parseStoredGroups(JSON.stringify(saved))).toEqual(saved);
	});

	it("ignores storage that is missing or not a layout", () => {
		expect(parseStoredGroups(null)).toEqual({});
		expect(parseStoredGroups("{not json")).toEqual({});
		expect(parseStoredGroups("[]")).toEqual({});
		expect(parseStoredGroups(JSON.stringify({ "sess-1": [{ id: "g1", root: { type: "split", dir: "diagonal" } }] }))).toEqual({});
	});

	it("drops tabs that cannot outlive a reload and dissolves what is left alone", () => {
		const saved = { "sess-1": [{ id: "g1", root: pair("session", "pending-shell:1"), focus: "pending-shell:1" }] };
		expect(parseStoredGroups(JSON.stringify(saved), (key) => key.startsWith("pending-shell:"))).toEqual({});
	});

	it("keeps each tab in one group and clamps ratios", () => {
		const saved = { "sess-1": [{ id: "g1", root: pair("a", "b", 7) }, { id: "g2", root: pair("b", "c") }] };
		expect(parseStoredGroups(JSON.stringify(saved))).toEqual({ "sess-1": [{ id: "g1", root: { ...pair("a", "b"), ratio: 0.95 } }] });
	});

	it("remembers only the most recently changed sessions", () => {
		const many = Object.fromEntries(
			Array.from({ length: MAX_STORED_SPLIT_SESSIONS + 5 }, (_, index) => [`sess-${index}`, [{ id: `g${index}`, root: pair("a", "b") as never }]]),
		);
		const trimmed = trimStoredGroups(many);
		expect(Object.keys(trimmed)).toHaveLength(MAX_STORED_SPLIT_SESSIONS);
		expect(trimmed["sess-0"]).toBeUndefined();
		expect(trimmed[`sess-${MAX_STORED_SPLIT_SESSIONS + 4}`]).toBeDefined();
	});
});

describe("splitBeside", () => {
	it("forms a group of the anchor and the tab, focused on the tab", () => {
		const [group] = splitBeside([], "session", "sh-1", "right");
		expect(leafKeys(group!.root)).toEqual(["session", "sh-1"]);
		expect(group!.focus).toBe("sh-1");
	});

	it("joins the anchor's group beside it and moves the tab out of its old group", () => {
		let groups = splitBeside([], "session", "sh-1", "right");
		groups = splitBeside(groups, "file:a", "sh-2", "left");
		groups = splitBeside(groups, "sh-1", "sh-2", "bottom");
		expect(groups).toHaveLength(1);
		expect(leafKeys(groups[0]!.root)).toEqual(["session", "sh-1", "sh-2"]);
		expect(groups[0]!.focus).toBe("sh-2");
	});

	it("leaves the layout alone when a tab is split beside itself", () => {
		const groups = splitBeside([], "session", "sh-1", "right");
		expect(splitBeside(groups, "sh-1", "sh-1", "left")).toBe(groups);
	});

	it("ungroups by id", () => {
		const groups = splitBeside([], "session", "sh-1", "right");
		expect(ungroupById(groups, groups[0]!.id)).toEqual([]);
		expect(ungroupById(groups, "missing")).toBe(groups);
	});
});
