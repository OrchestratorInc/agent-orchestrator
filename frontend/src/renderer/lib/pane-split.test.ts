import { describe, expect, it } from "vitest";
import {
	edgeAt,
	halfOf,
	insertBeside,
	layoutBoxes,
	layoutCss,
	leafKeys,
	reconcileGroups,
	removeLeaf,
	setRatioAt,
	type SplitNode,
	type TabGroup,
} from "./pane-split";

const leaf = (key: string): SplitNode => ({ type: "leaf", key });
const twoColumns: SplitNode = { type: "split", dir: "row", ratio: 0.5, a: leaf("chat"), b: leaf("shell") };

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
