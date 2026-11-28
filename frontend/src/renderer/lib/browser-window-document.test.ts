import { afterEach, describe, expect, it } from "vitest";
import { prepareBrowserWindowDocument } from "./browser-window-document";

// A same-origin about:blank frame stands in for the window the shell opens.
function childWindow(): Window {
	const frame = document.createElement("iframe");
	document.body.appendChild(frame);
	return frame.contentWindow!;
}

const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

afterEach(() => {
	document.head.querySelectorAll("[data-test-style]").forEach((node) => node.remove());
	document.body.querySelectorAll("iframe").forEach((node) => node.remove());
	document.documentElement.removeAttribute("data-theme");
	document.documentElement.removeAttribute("data-browser-native-live");
});

describe("prepareBrowserWindowDocument", () => {
	it("gives the window the app's styles, <html> attributes and a container", () => {
		const style = document.createElement("style");
		style.dataset.testStyle = "";
		style.textContent = ".x { color: red; }";
		document.head.appendChild(style);
		document.documentElement.setAttribute("data-theme", "dark");
		document.documentElement.setAttribute("data-browser-native-live", "");
		const child = childWindow();

		const prepared = prepareBrowserWindowDocument(child);

		const root = child.document.documentElement;
		expect([...child.document.head.querySelectorAll("style")].map((node) => node.textContent)).toContain(".x { color: red; }");
		expect(root.getAttribute("data-theme")).toBe("dark");
		// Its page shows through the panel, and the app window's own flags stay there.
		expect(root.getAttribute("data-browser-popout-open")).toBe("true");
		expect(root.getAttribute("data-browser-window")).toBe("true");
		expect(root.hasAttribute("data-browser-native-live")).toBe(false);
		expect(prepared.container.ownerDocument).toBe(child.document);
		expect(prepared.container.parentNode).toBe(child.document.body);
		prepared.dispose();
	});

	it("follows theme and style changes until disposed", async () => {
		const child = childWindow();
		const prepared = prepareBrowserWindowDocument(child);

		document.documentElement.setAttribute("data-theme", "light");
		const style = document.createElement("style");
		style.dataset.testStyle = "";
		style.textContent = ".y { color: blue; }";
		document.head.appendChild(style);
		await flush();

		expect(child.document.documentElement.getAttribute("data-theme")).toBe("light");
		expect([...child.document.head.querySelectorAll("style")].map((node) => node.textContent)).toContain(".y { color: blue; }");

		prepared.dispose();
		document.documentElement.setAttribute("data-theme", "dark");
		await flush();
		expect(child.document.documentElement.getAttribute("data-theme")).toBe("light");
	});
});
