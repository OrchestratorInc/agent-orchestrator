// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from "vitest";
import { APPROVAL_FOCUS_SCRIPT, approvalCropRect, approvalDescribeScript, describeApprovalTarget } from "./browser-approval-describe";

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

	it("names a textarea by its associated label, never by what is typed in it", () => {
		const doc = page(`
			<label for="m">Message</label><textarea id="m">my secret draft</textarea>
			<label>Reply <textarea id="r">another secret</textarea></label>`);
		const message = doc.getElementById("m") as HTMLTextAreaElement;
		message.value = "typed secret";
		message.focus();
		expect(describeApprovalTarget(doc, { mode: "focused" })).toMatchObject({ role: "textbox", name: "Message" });
		(doc.getElementById("r") as HTMLTextAreaElement).focus();
		const reply = describeApprovalTarget(doc, { mode: "focused" });
		expect(reply).toMatchObject({ role: "textbox", name: "Reply" });
		expect(JSON.stringify(reply)).not.toContain("secret");
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

	it("uses the submitter's formaction and formmethod over the form's", () => {
		const doc = page(`
			<form action="/search" method="get"><input type="password" name="pw"><button id="go" formaction="/account" formmethod="post">Continue</button></form>`);
		const facts = describeApprovalTarget(doc, { mode: "element" }, doc.getElementById("go"));
		expect(facts).toMatchObject({ formMethod: "post", formHasPassword: true, isSubmit: true });
		expect(facts?.formAction).toContain("/account");
	});

	it("applies the default button's overrides to Enter in a field", () => {
		const doc = page(`<form method="get"><input id="f"><button formmethod="post">Save changes</button></form>`);
		(doc.getElementById("f") as HTMLInputElement).focus();
		expect(describeApprovalTarget(doc, { mode: "focused" })).toMatchObject({ formMethod: "post", submitName: "Save changes", isSubmit: true });
	});

	it("counts fields that join the form from outside it", () => {
		const doc = page(`<form id="pay" method="post"><button id="b">Continue</button></form><input form="pay" autocomplete="cc-number"><input form="pay" type="password">`);
		expect(describeApprovalTarget(doc, { mode: "element" }, doc.getElementById("b"))).toMatchObject({
			formHasPayment: true, formHasPassword: true,
		});
	});

	it("is not fooled by fields named elements or action", () => {
		const doc = page(`<form action="/orders" method="post"><input name="elements"><input name="action"><input autocomplete="cc-number"><button id="b">Continue</button></form>`);
		const facts = describeApprovalTarget(doc, { mode: "element" }, doc.getElementById("b"));
		expect(facts).toMatchObject({ formHasPayment: true, formMethod: "post" });
		expect(facts?.formAction).toContain("/orders");
	});

	it("finds an image button as the default submitter for Enter", () => {
		const doc = page(`<form method="get"><input id="d" aria-label="Details"><input type="password"><input type="image" alt="Continue" formmethod="post"></form>`);
		(doc.getElementById("d") as HTMLInputElement).focus();
		expect(describeApprovalTarget(doc, { mode: "focused" })).toMatchObject({ formMethod: "post", submitName: "Continue", formHasPassword: true });
	});

	it("tells a sign-in form from one that sets a new password", () => {
		const login = page(`<form action="/login" method="post"><input type="email"><input type="password" autocomplete="current-password"><button id="b">Continue</button></form>`);
		expect(describeApprovalTarget(login, { mode: "element" }, login.getElementById("b"))).toMatchObject({ formHasPassword: true, formHasNewPassword: false });
		const change = page(`<form method="post"><input type="password"><input type="password" autocomplete="new-password"><button id="b">Update</button></form>`);
		expect(describeApprovalTarget(change, { mode: "element" }, change.getElementById("b"))).toMatchObject({ formHasNewPassword: true });
		const confirm = page(`<form method="post"><input type="password"><input type="password"><button id="b">Save</button></form>`);
		expect(describeApprovalTarget(confirm, { mode: "element" }, confirm.getElementById("b"))).toMatchObject({ formHasNewPassword: true });
	});

	it("prefers the exact name over a normalized look-alike", () => {
		const doc = page(`<button type="button">Continue!</button><form method="post"><input autocomplete="cc-number"><button id="pay">Continue</button></form>`);
		expect(describeApprovalTarget(doc, { mode: "match", role: "button", name: "Continue" })).toMatchObject({ isSubmit: true, formHasPayment: true });
	});

	it("refuses an ambiguous normalized match rather than picking the first", () => {
		const doc = page(`<button type="button">Continue!</button><form method="post"><button>Continue?</button></form>`);
		expect(describeApprovalTarget(doc, { mode: "match", role: "button", name: "Continue" })).toBeNull();
	});

	it("marks the focused element for Enter and reports when focus moves", () => {
		const doc = page(`<input id="a" aria-label="Message"><input id="b" aria-label="Other">`);
		(doc.getElementById("a") as HTMLInputElement).focus();
		const win = { innerWidth: 1, innerHeight: 1 } as Record<string, unknown>;
		// eslint-disable-next-line no-new-func
		const run = (code: string) => new Function("document", "window", `return ${code}`)(doc, win);
		const described = run(approvalDescribeScript({ mode: "focused" })) as { focus?: string };
		expect(described.focus).toEqual(expect.any(String));
		expect(run(APPROVAL_FOCUS_SCRIPT)).toBe(described.focus);
		(doc.getElementById("b") as HTMLInputElement).focus();
		expect(run(APPROVAL_FOCUS_SCRIPT)).toBe("");
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
