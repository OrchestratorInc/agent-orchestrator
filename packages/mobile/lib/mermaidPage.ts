/**
 * The page a Mermaid diagram is drawn in: the engine and the diagram's source,
 * in one document loaded from memory into a WebView. It has no origin, so it
 * cannot reach the daemon or anything else; its CSP forbids every load besides
 * its own inline script and styles; Mermaid runs in strict mode (no script
 * callbacks, no `javascript:` links), as on desktop. The page posts back
 * whether the diagram rendered, so the viewer can fall back to the source.
 */

export type DiagramColors = { scheme: "light" | "dark"; background: string };

/**
 * What the page posts back. A drawn diagram reports its laid-out height (for
 * an inline frame sized to it) and its SVG (so the next mount of the same
 * source draws from a cache instead of loading the engine again).
 */
export type DiagramMessage = { type: "rendered"; height?: number; svg?: string } | { type: "failed"; message: string };

const CSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:";

/** Text written into a <script> element: a `</script` inside would close it early. */
export function inlineScript(source: string): string {
	return source.replace(/<\/(script)/gi, "<\\/$1");
}

/** A JS string literal for `value`, safe inside an inline <script>. */
export function scriptString(value: string): string {
	return JSON.stringify(value).replace(/</g, "\\u003c").replace(/\u2028/g, "\\u2028").replace(/\u2029/g, "\\u2029");
}

/** The colour a background name or hex writes into CSS, or a neutral one if it is not a plain colour. */
function cssColor(value: string, fallback: string): string {
	return /^#[0-9a-f]{3,8}$/i.test(value) || /^[a-z]+$/i.test(value) ? value : fallback;
}

/**
 * `inline` lays the diagram out for a frame inside a message or document: no
 * zoom, a smaller margin, and its height and SVG posted back. Otherwise it is
 * a full page that pinch-zooms.
 */
export function mermaidPageHtml({ runtime, code, colors, inline = false }: { runtime: string; code: string; colors: DiagramColors; inline?: boolean }): string {
	const theme = colors.scheme === "dark" ? "dark" : "default";
	return pageShell(colors, inline, `<div id="diagram"></div>
<script>${inlineScript(runtime)}</script>
<script>
${REPORT_SCRIPT}
(async function () {
	var code = ${scriptString(code)};
	try {
		mermaid.initialize({ startOnLoad: false, theme: ${scriptString(theme)}, securityLevel: "strict", suppressErrorRendering: true });
		// Parse first, so a syntax error fails here instead of drawing an error box that reads as a diagram.
		await mermaid.parse(code);
		var result = await mermaid.render("ao-diagram", code);
		document.getElementById("diagram").innerHTML = result.svg;
		report(${inline ? "result.svg" : "undefined"});
	} catch (error) {
		post({ type: "failed", message: String((error && error.message) || error) });
	}
})();
</script>`);
}

/**
 * A diagram drawn before, from its cached SVG: the same page without the
 * engine, so it opens at once. The SVG is Mermaid's own strict-mode output.
 */
export function mermaidSvgPageHtml({ svg, colors, inline = false }: { svg: string; colors: DiagramColors; inline?: boolean }): string {
	return pageShell(colors, inline, `<div id="diagram">${withoutScripts(svg)}</div>
<script>
${REPORT_SCRIPT}
report(undefined);
</script>`);
}

/** Markup with every script element, and any stray script tag, taken out. */
function withoutScripts(markup: string): string {
	return markup.replace(/<script\b[\s\S]*?<\/script\s*>/gi, "").replace(/<\/?script\b[^>]*>/gi, "");
}

// Posts the diagram's height once laid out (after a frame, so the SVG has its
// size), and again whenever it changes: the SVG scales with the frame's width,
// so a resized window (iPad Split View, Stage Manager) changes its height.
const REPORT_SCRIPT = `var post = function (message) { window.ReactNativeWebView.postMessage(JSON.stringify(message)); };
var measure = function () { return Math.ceil(document.getElementById("diagram").getBoundingClientRect().height); };
var report = function (svg) {
	requestAnimationFrame(function () {
		var last = measure();
		post(svg === undefined ? { type: "rendered", height: last } : { type: "rendered", height: last, svg: svg });
		var follow = function () {
			var height = measure();
			if (height > 0 && height !== last) { last = height; post({ type: "rendered", height: height }); }
		};
		if (window.ResizeObserver) new ResizeObserver(follow).observe(document.getElementById("diagram"));
		else window.addEventListener("resize", follow);
	});
};`;

function pageShell(colors: DiagramColors, inline: boolean, body: string): string {
	const background = cssColor(colors.background, colors.scheme === "dark" ? "#0a0a0a" : "#ffffff");
	const viewport = inline ? "width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no" : "width=device-width, initial-scale=1, maximum-scale=6, user-scalable=yes";
	return `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="${CSP}">
<meta name="viewport" content="${viewport}">
<style>
html, body { margin: 0; padding: 0; background: ${background}; color-scheme: ${colors.scheme}; }
#diagram { padding: ${inline ? 12 : 16}px; box-sizing: border-box; display: flex; justify-content: center; }
#diagram svg { max-width: 100%; height: auto; }
</style>
</head>
<body>
${body}
</body>
</html>`;
}

/** A message the page posted, or undefined for anything else. */
export function parseDiagramMessage(data: string): DiagramMessage | undefined {
	try {
		const message = JSON.parse(data) as { type?: unknown; message?: unknown; height?: unknown; svg?: unknown };
		if (message.type === "rendered") {
			return {
				type: "rendered",
				...(typeof message.height === "number" && Number.isFinite(message.height) && message.height > 0 ? { height: message.height } : {}),
				...(typeof message.svg === "string" ? { svg: message.svg } : {}),
			};
		}
		if (message.type === "failed") return { type: "failed", message: typeof message.message === "string" ? message.message : "" };
	} catch {
		// Not one of ours.
	}
	return undefined;
}
