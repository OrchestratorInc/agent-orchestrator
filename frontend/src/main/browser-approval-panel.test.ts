import { describe, expect, it, vi } from "vitest";
import { describePanelApprovalTarget, type PanelApprovalPage } from "./browser-approval-panel";

const refs = {
	e1: { role: "link", name: "Home" },
	e4: { role: "button", name: "Place order" },
};

function fakePage(result: unknown): PanelApprovalPage & { evaluate: ReturnType<typeof vi.fn>; capture: ReturnType<typeof vi.fn> } {
	return {
		url: "https://shop.example/checkout",
		title: "Checkout",
		evaluate: vi.fn(async () => result),
		capture: vi.fn(async () => "iVBORw0KGgo="),
	};
}

const checkoutFacts = {
	facts: {
		role: "button", name: "Place order", tag: "button", isSubmit: true, formMethod: "post",
		formHasPayment: true, formHasPassword: false, rect: { x: 400, y: 300, width: 120, height: 32 },
	},
	viewport: { width: 1280, height: 800 },
};

describe("describePanelApprovalTarget", () => {
	it("resolves a ref through the snapshot and reads the matching control's DOM facts", async () => {
		const page = fakePage(checkoutFacts);
		const out = await describePanelApprovalTarget({ action: "click", ref: "@e4", snapshot: async () => ({ refs }), page });
		expect(out.target).toMatchObject({
			role: "button", name: "Place order", isSubmit: true, formHasPayment: true,
			pageUrl: "https://shop.example/checkout", pageTitle: "Checkout",
		});
		expect(page.evaluate.mock.calls[0][0]).toContain('"mode":"match","role":"button","name":"Place order"');
		expect(page.capture).toHaveBeenCalledWith({ x: 240, y: 210, width: 440, height: 212 });
		expect(out.image).toBe("iVBORw0KGgo=");
	});

	it("describes a drag's drop target, which decides what the drag does", async () => {
		const page = fakePage(checkoutFacts);
		// The daemon asks about the drop target (the drag's targetRef) as ref.
		const out = await describePanelApprovalTarget({ action: "drag", ref: "@e4", snapshot: async () => ({ refs }), page });
		expect(out.target).toMatchObject({ role: "button", name: "Place order", pageUrl: "https://shop.example/checkout" });
		expect(page.evaluate.mock.calls[0][0]).toContain('"mode":"match","role":"button","name":"Place order"');
	});

	it("resolves act instructions with the same matcher act uses", async () => {
		const page = fakePage(checkoutFacts);
		const out = await describePanelApprovalTarget({
			action: "act", instruction: "click the place order button", snapshot: async () => ({ refs }), page,
		});
		expect(out.target).toMatchObject({ role: "button", name: "Place order", instruction: "click the place order button" });
	});

	it("describes the focused element for Enter without a snapshot", async () => {
		const page = fakePage({ facts: { role: "textbox", name: "Message", isSubmit: true, submitName: "Send" }, viewport: { width: 0, height: 0 } });
		const snapshot = vi.fn();
		const out = await describePanelApprovalTarget({ action: "press", snapshot, page });
		expect(snapshot).not.toHaveBeenCalled();
		expect(page.evaluate.mock.calls[0][0]).toContain('"mode":"focused"');
		expect(out.target).toMatchObject({ role: "textbox", submitName: "Send" });
		expect(out.image).toBeUndefined();
	});

	it("returns page facts only when the ref is unknown", async () => {
		const page = fakePage(checkoutFacts);
		const out = await describePanelApprovalTarget({ action: "click", ref: "e99", snapshot: async () => ({ refs }), page });
		expect(out.target).toEqual({ pageUrl: "https://shop.example/checkout", pageTitle: "Checkout", instruction: undefined });
		expect(page.evaluate).not.toHaveBeenCalled();
	});

	it("keeps the accessibility role and name when page scripts fail", async () => {
		const page = fakePage(undefined);
		page.evaluate.mockRejectedValue(new Error("blocked"));
		const out = await describePanelApprovalTarget({ action: "click", ref: "e4", snapshot: async () => ({ refs }), page });
		expect(out.target).toMatchObject({ role: "button", name: "Place order" });
		expect(out.partial).toBe(true);
	});

	it("reports the matched ref for act so an approval can be bound to it", async () => {
		const out = await describePanelApprovalTarget({
			action: "act", instruction: "place order", snapshot: async () => ({ refs }), page: fakePage(checkoutFacts),
		});
		expect(out.resolvedRef).toBe("e4");
	});

	it("does not read DOM facts when several controls share the role and name", async () => {
		const page = fakePage(checkoutFacts);
		const out = await describePanelApprovalTarget({
			action: "click",
			ref: "e5",
			snapshot: async () => ({ refs: { e4: { role: "button", name: "Continue" }, e5: { role: "button", name: "Continue" } } }),
			page,
		});
		expect(page.evaluate).not.toHaveBeenCalled();
		expect(out.target).toEqual({ role: "button", name: "Continue", pageUrl: "https://shop.example/checkout", pageTitle: "Checkout" });
		expect(out.partial).toBe(true);
	});
});
