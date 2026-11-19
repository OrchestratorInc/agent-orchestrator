import { describe, expect, it } from "vitest";
import { snapshotDelta } from "./delta";

const base = {
	revision: 3,
	text: '- heading "Cart"\n- button "Checkout" [ref=e1]\n- link "Help" [ref=e2]',
	refs: { e1: { role: "button", name: "Checkout" }, e2: { role: "link", name: "Help" } },
};

describe("snapshotDelta", () => {
	it("returns the full tree without a base or when asked", () => {
		expect(snapshotDelta(undefined, base, 1, false)).toEqual({ kind: "full", revision: 1, text: base.text, refs: base.refs });
		expect(snapshotDelta(base, base, 4, true).kind).toBe("full");
	});

	it("reports unchanged snapshots", () => {
		expect(snapshotDelta(base, { text: base.text, refs: { ...base.refs } }, 4, false)).toEqual({
			kind: "unchanged",
			revision: 4,
			baseRevision: 3,
		});
	});

	it("reports ref changes and one contiguous line replacement", () => {
		const current = {
			text: '- heading "Cart"\n- button "Checkout (2)" [ref=e1]\n- textbox "Coupon" [ref=e3]\n- link "Help" [ref=e2]',
			refs: { e1: { role: "button", name: "Checkout (2)" }, e2: { role: "link", name: "Help" }, e3: { role: "textbox", name: "Coupon" } },
		};
		expect(snapshotDelta(base, current, 4, false)).toEqual({
			kind: "delta",
			revision: 4,
			baseRevision: 3,
			changes: [
				{ op: "changed", ref: "e1", node: { role: "button", name: "Checkout (2)" } },
				{ op: "added", ref: "e3", node: { role: "textbox", name: "Coupon" } },
			],
			treeChange: {
				startLine: 1,
				deleteCount: 1,
				lines: ['- button "Checkout (2)" [ref=e1]', '- textbox "Coupon" [ref=e3]'],
			},
		});
	});

	it("reports removals", () => {
		const current = { text: '- heading "Cart"\n- button "Checkout" [ref=e1]', refs: { e1: base.refs.e1 } };
		const delta = snapshotDelta(base, current, 4, false);
		expect(delta).toMatchObject({ kind: "delta", changes: [{ op: "removed", ref: "e2" }], treeChange: { startLine: 2, deleteCount: 1, lines: [] } });
	});
});
