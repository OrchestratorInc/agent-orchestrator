import { describe, expect, it } from "vitest";
import { matchInstruction } from "../main/browser-act-matcher";
import { accessibleName, buildSnapshot, RefRegistry, roleOf } from "./snapshot";

// The live jsdom window document, so computed styles (display/visibility)
// behave as they do in the content script.
function page(html: string): Document {
	window.document.body.innerHTML = html;
	return window.document;
}

describe("roleOf", () => {
	it("maps HTML elements to their implicit ARIA roles", () => {
		const document = page(`
			<a href="/x" id="a">x</a><a id="b">no href</a>
			<input id="c" type="checkbox"><input id="d" type="email"><input id="e" type="submit">
			<select id="f"><option>1</option></select><select id="g" multiple></select>
			<h2 id="h">T</h2><nav id="i"></nav><div id="j" role="tab"></div><div id="k" role="presentation"></div>
			<img id="l" alt="Logo"><img id="m" alt="">
		`);
		const role = (id: string) => roleOf(document.getElementById(id)!);
		expect(["a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m"].map(role)).toEqual([
			"link",
			"",
			"checkbox",
			"textbox",
			"button",
			"combobox",
			"listbox",
			"heading",
			"navigation",
			"tab",
			"",
			"img",
			"",
		]);
	});
});

describe("accessibleName", () => {
	it("follows aria-labelledby, aria-label, labels, placeholders and content", () => {
		const document = page(`
			<span id="lbl">Billing email</span>
			<input id="a" aria-labelledby="lbl">
			<button id="b" aria-label="Close dialog">×</button>
			<label for="c">First name</label><input id="c">
			<label>Country <select id="d"><option>India</option><option>Japan</option></select></label>
			<input id="e" placeholder="Search docs">
			<a id="f" href="/"><img alt="Home"> page</a>
			<input id="g" type="submit">
		`);
		const name = (id: string) => accessibleName(document.getElementById(id)!);
		expect(name("a")).toBe("Billing email");
		expect(name("b")).toBe("Close dialog");
		expect(name("c")).toBe("First name");
		expect(name("d")).toBe("Country");
		expect(name("e")).toBe("Search docs");
		expect(name("f")).toBe("Home page");
		expect(name("g")).toBe("Submit");
	});
});

describe("buildSnapshot", () => {
	const html = `
		<header><nav aria-label="Primary"><a href="/">Home</a><a href="/docs">Docs</a></nav></header>
		<main>
			<h1>Sign in</h1>
			<p>Use your <a href="/help">work account</a> to continue.</p>
			<form aria-label="Login">
				<label for="email">Email</label><input id="email" value="me@example.com">
				<label for="pw">Password</label><input id="pw" type="password" value="hunter2">
				<label><input type="checkbox" checked> Remember me</label>
				<button type="submit">Sign in</button>
				<button disabled>Help</button>
			</form>
			<div hidden><button>Hidden</button></div>
			<div style="display:none"><a href="/x">Invisible</a></div>
			<div aria-hidden="true"><button>Decorative</button></div>
		</main>`;

	it("renders a compact tree with refs for interactive elements", () => {
		const result = buildSnapshot(page(html), new RefRegistry());
		expect(result.snapshot).toContain('- navigation "Primary"');
		expect(result.snapshot).toContain('  - link "Home" [ref=');
		expect(result.snapshot).toContain('- heading "Sign in" [level=1] [ref=');
		expect(result.snapshot).toContain("- paragraph: Use your work account to continue.");
		expect(result.snapshot).toContain('- link "work account" [ref=');
		expect(result.snapshot).toContain('- textbox "Email": "me@example.com" [ref=');
		expect(result.snapshot).toContain('- checkbox "Remember me" [checked] [ref=');
		expect(result.snapshot).toContain('- button "Help" [disabled] [ref=');
		expect(result.snapshot).not.toContain("Hidden");
		expect(result.snapshot).not.toContain("Invisible");
		expect(result.snapshot).not.toContain("Decorative");
		expect(result.truncated).toBe(false);
	});

	it("never exposes password values", () => {
		const result = buildSnapshot(page(html), new RefRegistry());
		expect(result.snapshot).toContain('- textbox "Password" [ref=');
		expect(result.snapshot).not.toContain("hunter2");
	});

	it("lists only interactive elements in interactive mode, unindented", () => {
		const result = buildSnapshot(page(html), new RefRegistry(), { interactive: true });
		const lines = result.snapshot.split("\n");
		expect(lines.every((line) => line.startsWith("- "))).toBe(true);
		expect(lines.some((line) => line.startsWith("- heading"))).toBe(false);
		expect(lines.some((line) => line.startsWith("- paragraph"))).toBe(false);
		expect(Object.values(result.refs).map((ref) => ref.role)).toEqual([
			"link",
			"link",
			"link",
			"textbox",
			"textbox",
			"checkbox",
			"button",
			"button",
		]);
	});

	it("keeps refs stable across snapshots and works with the act matcher", () => {
		const document = page(html);
		const registry = new RefRegistry();
		const first = buildSnapshot(document, registry, { interactive: true });
		const second = buildSnapshot(document, registry, { interactive: true });
		expect(second.refs).toEqual(first.refs);
		const match = matchInstruction("the sign in button", first.refs);
		expect(match.outcome).toBe("matched");
		if (match.outcome !== "matched") return;
		expect(registry.resolve(match.candidate.ref).textContent).toBe("Sign in");
	});

	it("reports stale refs after an element leaves the page", () => {
		const document = page(`<button id="go">Go</button>`);
		const registry = new RefRegistry();
		const { refs } = buildSnapshot(document, registry, { interactive: true });
		const [ref] = Object.keys(refs);
		document.getElementById("go")!.remove();
		expect(() => registry.resolve(ref!)).toThrow(expect.objectContaining({ code: "STALE_REFERENCE" }));
	});

	it("traverses open shadow roots", () => {
		const document = page(`<div id="host"></div>`);
		const root = document.getElementById("host")!.attachShadow({ mode: "open" });
		root.innerHTML = `<button>Shadow action</button>`;
		const result = buildSnapshot(document, new RefRegistry(), { interactive: true });
		expect(result.snapshot).toContain('- button "Shadow action"');
	});

	it("truncates very long pages", () => {
		const items = Array.from({ length: 50 }, (_, i) => `<button>B${i}</button>`).join("");
		const result = buildSnapshot(page(items), new RefRegistry(), { interactive: true, maxLines: 10 });
		expect(result.truncated).toBe(true);
		expect(result.snapshot.split("\n")).toHaveLength(11);
		expect(result.snapshot).toContain("snapshot truncated after 10 lines");
	});
});
