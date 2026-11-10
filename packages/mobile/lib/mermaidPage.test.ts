import { describe, expect, it } from "vitest";
import { inlineScript, mermaidPageHtml, parseDiagramMessage, scriptString } from "./mermaidPage";

describe("mermaidPageHtml", () => {
	const page = (code: string, scheme: "light" | "dark" = "dark", background = "#0a0a0a") => mermaidPageHtml({ runtime: "window.mermaid = {};", code, colors: { scheme, background } });

	it("loads nothing but its own inline script and styles", () => {
		const html = page("graph TD; A-->B");
		expect(html).toContain(`content="default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:"`);
		expect(html).not.toMatch(/<script[^>]+src=/i);
		expect(html).toContain('securityLevel: "strict"');
	});

	it("keeps the diagram's source from closing the script it is written into", () => {
		const html = page("graph TD; A[\"</script><script>alert(1)</script>\"]-->B");
		expect(html).not.toContain("</script><script>alert(1)");
		expect(html.match(/<\/script>/g)).toHaveLength(2);
	});

	it("draws with Mermaid's theme for the app's scheme, on the app's background", () => {
		expect(page("graph TD; A-->B", "dark")).toContain('theme: "dark"');
		const light = page("graph TD; A-->B", "light", "#ffffff");
		expect(light).toContain('theme: "default"');
		expect(light).toContain("background: #ffffff");
	});

	it("does not write a background that is not a plain colour into the stylesheet", () => {
		const html = page("graph TD; A-->B", "dark", "red; } body { display: none");
		expect(html).toContain("background: #0a0a0a");
		expect(html).not.toContain("display: none");
	});
});

describe("inlineScript", () => {
	it("escapes a closing script tag in the engine's source", () => {
		expect(inlineScript('var a = "</script>"; var b = "</SCRIPT>";')).toBe('var a = "<\\/script>"; var b = "<\\/SCRIPT>";');
	});
});

describe("scriptString", () => {
	it("is a string literal that evaluates back to the source", () => {
		const source = "graph LR\n  A[\"<b>a</b>\"] --> B\u2028C";
		const literal = scriptString(source);
		expect(literal).not.toContain("<");
		expect(JSON.parse(literal)).toBe(source);
	});
});

describe("parseDiagramMessage", () => {
	it("reads the page's own messages", () => {
		expect(parseDiagramMessage('{"type":"rendered"}')).toEqual({ type: "rendered" });
		expect(parseDiagramMessage('{"type":"failed","message":"Parse error"}')).toEqual({ type: "failed", message: "Parse error" });
	});

	it("ignores anything else", () => {
		expect(parseDiagramMessage("not json")).toBeUndefined();
		expect(parseDiagramMessage('{"type":"navigate"}')).toBeUndefined();
	});
});
