// @vitest-environment jsdom
// The daemon's remote describe script (backend/internal/service/browser/
// remote.go) runs inside the remote page, where Go tests cannot run it: this
// test loads the script from the Go source and runs it in jsdom.
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const source = readFileSync(resolve(__dirname, "../../../backend/internal/service/browser/remote.go"), "utf8");
const template = /const remoteDescribeScript = `([\s\S]*?)`/.exec(source)?.[1] ?? "";

/** Runs the script on the current document, as the Go side builds it. */
function run(request: Record<string, string>): Record<string, unknown> {
	// The Go side fills %s with the JSON request (fmt.Sprintf).
	const script = template.replace("%s", JSON.stringify(request)).replaceAll("%%", "%");
	return (0, eval)(script) as Record<string, unknown>;
}

function describeIn(html: string, request: Record<string, string>): Record<string, unknown> {
	document.body.innerHTML = html;
	return run(request);
}

describe("remote describe script", () => {
	it("is found in the Go source", () => {
		expect(template).toContain("req.mode");
	});

	it("counts card fields that join the form from outside it", () => {
		const facts = describeIn(
			`<input form="checkout" name="cardnumber" autocomplete="cc-number">
			<form id="checkout" method="post" action="/pay"><button id="go">Continue</button></form>`,
			{ mode: "selector", selector: "#go" },
		);
		expect(facts).toMatchObject({ role: "button", name: "Continue", isSubmit: true, formMethod: "post", formHasPayment: true });
	});

	it("counts new-password fields that join the form from outside it", () => {
		const facts = describeIn(
			`<input form="profile" type="password" autocomplete="new-password">
			<form id="profile" method="post"><button id="save">Sign in</button></form>`,
			{ mode: "selector", selector: "#save" },
		);
		expect(facts).toMatchObject({ formHasPassword: true, formHasNewPassword: true });
	});

	it("uses a submit button that joins the form from outside it", () => {
		const facts = describeIn(
			`<form id="order" method="post"><input id="qty" value="1"></form>
			<button form="order">Place order</button>`,
			{ mode: "selector", selector: "#qty" },
		);
		expect(facts).toMatchObject({ submitName: "Place order" });
	});

	it("leaves out fields that belong to another form", () => {
		const facts = describeIn(
			`<form id="pay"><input name="cardnumber" autocomplete="cc-number"></form>
			<form id="search" method="get"><button id="find">Search</button></form>`,
			{ mode: "selector", selector: "#find" },
		);
		expect(facts).toMatchObject({ formHasPayment: false });
	});

	it("names editable fields by label, never by what is typed in them", () => {
		for (const [id, name] of [["m", "Message"], ["c", "Card"]] as const) {
			document.body.innerHTML = `<label for="m">Message</label><textarea id="m">my secret draft</textarea>
				<label>Card <input id="c" value="4111111111111111"></label>`;
			(document.getElementById(id) as HTMLElement).focus();
			const facts = run({ mode: "focused" });
			expect(facts).toMatchObject({ role: "textbox", name });
			expect(JSON.stringify(facts)).not.toMatch(/secret|4111/);
		}
	});
});
