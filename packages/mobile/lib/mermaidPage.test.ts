import { describe, expect, it } from "vitest";
import { inlineScript, mermaidPageHtml, mermaidSvgPageHtml, parseDiagramMessage, scriptString } from "./mermaidPage";

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

describe("inline diagrams", () => {
	const colors = { scheme: "dark" as const, background: "#111111" };

	it("lays a diagram out for a message: no zoom, and its height and SVG posted back", () => {
		const html = mermaidPageHtml({ runtime: "", code: "graph TD; A-->B", colors, inline: true });
		expect(html).toContain("user-scalable=no");
		expect(html).toContain("report(result.svg)");
		expect(mermaidPageHtml({ runtime: "", code: "graph TD; A-->B", colors })).toContain("report(undefined)");
	});

	it("posts the height again when the diagram's size changes", () => {
		for (const html of [mermaidPageHtml({ runtime: "", code: "graph TD; A-->B", colors, inline: true }), mermaidSvgPageHtml({ svg: "<svg/>", colors, inline: true })]) {
			expect(html).toContain("new ResizeObserver(follow).observe(document.getElementById(\"diagram\"))");
			expect(html).toContain('window.addEventListener("resize", follow)');
		}
	});

	it("draws a cached diagram without the engine, and without any script it carries", () => {
		const html = mermaidSvgPageHtml({ svg: '<svg><script>alert(1)</script><g/></svg>', colors, inline: true });
		expect(html).toContain("<svg><g/></svg>");
		expect(html).not.toContain("alert(1)</script>");
		expect(html).not.toContain("mermaid.initialize");
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
		expect(parseDiagramMessage('{"type":"rendered","height":212,"svg":"<svg/>"}')).toEqual({ type: "rendered", height: 212, svg: "<svg/>" });
		expect(parseDiagramMessage('{"type":"rendered","height":-4,"svg":7}')).toEqual({ type: "rendered" });
		expect(parseDiagramMessage('{"type":"failed","message":"Parse error"}')).toEqual({ type: "failed", message: "Parse error" });
	});

	it("ignores anything else", () => {
		expect(parseDiagramMessage("not json")).toBeUndefined();
		expect(parseDiagramMessage('{"type":"navigate"}')).toBeUndefined();
	});
});
