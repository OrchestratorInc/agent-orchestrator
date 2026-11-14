import { afterEach, describe, expect, it, vi } from "vitest";
import { findSettingsTarget, highlightSettingsTarget } from "./settings-focus";

function page(html: string): HTMLElement {
	const root = document.createElement("div");
	root.innerHTML = html;
	document.body.append(root);
	return root;
}

afterEach(() => {
	document.body.innerHTML = "";
});

describe("settings focus", () => {
	it("finds the row a label sits in, not the page title", () => {
		const root = page(`
			<h1>Theme</h1>
			<section><h2>Appearance</h2>
				<div class="settings-row-bar" id="theme-row"><div><span>Theme</span></div><button>System</button></div>
			</section>`);
		expect(findSettingsTarget(root, "Theme")?.id).toBe("theme-row");
	});

	it("matches across whitespace and case", () => {
		const root = page(`<div class="settings-row-bar" id="row"><div>  Copy   on select </div></div>`);
		expect(findSettingsTarget(root, "copy on select")?.id).toBe("row");
	});

	it("takes a section for its heading and a button for its text or aria-label", () => {
		const root = page(`
			<section id="appearance"><h2>Appearance</h2><div class="settings-row-bar"><span>Theme</span></div></section>
			<button id="add">Add account</button>
			<button id="refresh" aria-label="Refresh limits"><svg></svg></button>`);
		expect(findSettingsTarget(root, "Appearance")?.id).toBe("appearance");
		expect(findSettingsTarget(root, "Add account")?.id).toBe("add");
		expect(findSettingsTarget(root, "Refresh limits")?.id).toBe("refresh");
	});

	it("skips hidden copies and returns null when nothing matches", () => {
		const root = page(`<div aria-hidden="true"><div class="settings-row-bar"><span>Theme</span></div></div>`);
		expect(findSettingsTarget(root, "Theme")).toBeNull();
		expect(findSettingsTarget(root, "")).toBeNull();
	});

	it("scrolls the target into view and marks it until cleared", () => {
		const root = page(`<div class="settings-row-bar" id="row"><span>Theme</span></div>`);
		const row = root.querySelector<HTMLElement>("#row")!;
		row.scrollIntoView = vi.fn();
		const clear = highlightSettingsTarget(row);
		expect(row.scrollIntoView).toHaveBeenCalledWith({ block: "center" });
		expect(row.classList.contains("settings-focus-target")).toBe(true);
		clear();
		expect(row.classList.contains("settings-focus-target")).toBe(false);
	});
});
