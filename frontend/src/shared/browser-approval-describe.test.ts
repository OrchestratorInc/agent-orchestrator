// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from "vitest";
import { approvalCropRect, approvalDescribeScript, describeApprovalTarget } from "./browser-approval-describe";

// jsdom has no layout: give every element a visible box.
beforeEach(() => {
	Element.prototype.getBoundingClientRect = function () {
		return { x: 100, y: 200, width: 120, height: 32, left: 100, top: 200, right: 220, bottom: 232, toJSON() {} } as DOMRect;
	};
});

function page(html: string): Document {
	document.body.innerHTML = html;
	return document;
}

describe("describeApprovalTarget", () => {
	it("finds a checkout button by role and name and reports its payment form", () => {
		const doc = page(`
			<form action="/orders" method="post">
				<input id="cc" name="cardnumber" autocomplete="cc-number">
				<button>Place order</button>
			</form>`);
		const facts = describeApprovalTarget(doc, { mode: "match", role: "button", name: "Place order" });
		expect(facts).toMatchObject({
			role: "button", name: "Place order", tag: "button", isSubmit: true,
			formMethod: "post", formHasPayment: true, formHasPassword: false, submitName: "Place order",
			rect: { x: 100, y: 200, width: 120, height: 32 },
		});
		expect(facts?.formAction).toContain("/orders");
	});

	it("reports Enter in a form's text field as a submit with the form's button label", () => {
		const doc = page(`
			<form method="post"><label for="m">Message</label><input id="m"><button type="submit">Send</button></form>`);
		(doc.getElementById("m") as HTMLInputElement).focus();
		expect(describeApprovalTarget(doc, { mode: "focused" })).toMatchObject({
			role: "textbox", name: "Message", isSubmit: true, submitName: "Send", formMethod: "post",
		});
	});

	it("does not treat Enter in a textarea or a type=button as a submit", () => {
		const doc = page(`<form method="post"><textarea id="t" placeholder="Notes"></textarea><button type="button" id="b">Preview</button></form>`);
		(doc.getElementById("t") as HTMLTextAreaElement).focus();
		expect(describeApprovalTarget(doc, { mode: "focused" })?.isSubmit).toBe(false);
		expect(describeApprovalTarget(doc, { mode: "element" }, doc.getElementById("b"))?.isSubmit).toBe(false);
	});

	it("reports a search form as a GET submit", () => {
		const doc = page(`<form action="/search"><input type="search" id="q" aria-label="Search"><button>Search</button></form>`);
		(doc.getElementById("q") as HTMLInputElement).focus();
		expect(describeApprovalTarget(doc, { mode: "focused" })).toMatchObject({ role: "searchbox", isSubmit: true, formMethod: "get" });
	});

	it("reports links with their href and no form", () => {
		const doc = page(`<nav><a href="https://shop.example/payments">Payments</a></nav>`);
		expect(describeApprovalTarget(doc, { mode: "match", role: "link", name: "payments" })).toMatchObject({
			role: "link", href: "https://shop.example/payments", isSubmit: false,
		});
	});

	it("returns null when nothing matches or nothing is focused", () => {
		const doc = page(`<button>Other</button>`);
		expect(describeApprovalTarget(doc, { mode: "match", role: "button", name: "Delete" })).toBeNull();
		expect(describeApprovalTarget(doc, { mode: "focused" })).toBeNull();
	});

	it("runs as injected source text with no outer references", () => {
		const doc = page(`<form method="post"><input type="password"><input type="submit" value="Save changes"></form>`);
		// eslint-disable-next-line no-new-func
		const run = new Function("document", "window", `return ${approvalDescribeScript({ mode: "match", role: "button", name: "Save changes" })}`);
		expect(run(doc, { innerWidth: 1280, innerHeight: 800 })).toMatchObject({
			facts: { name: "Save changes", inputType: "submit", isSubmit: true, formHasPassword: true },
			viewport: { width: 1280, height: 800 },
		});
	});
});

describe("approvalCropRect", () => {
	it("pads around the element and stays inside the viewport", () => {
		expect(approvalCropRect({ x: 100, y: 200, width: 120, height: 32 }, { width: 1280, height: 800 })).toEqual({
			x: 0, y: 110, width: 380, height: 212,
		});
		expect(approvalCropRect(undefined, { width: 1280, height: 800 })).toBeUndefined();
	});
});
