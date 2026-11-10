/**
 * The page a Mermaid diagram is drawn in: the engine and the diagram's source,
 * in one document loaded from memory into a WebView. It has no origin, so it
 * cannot reach the daemon or anything else; its CSP forbids every load besides
 * its own inline script and styles; Mermaid runs in strict mode (no script
 * callbacks, no `javascript:` links), as on desktop. The page posts back
 * whether the diagram rendered, so the viewer can fall back to the source.
 */

export type DiagramColors = { scheme: "light" | "dark"; background: string };

export type DiagramMessage = { type: "rendered" } | { type: "failed"; message: string };

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

export function mermaidPageHtml({ runtime, code, colors }: { runtime: string; code: string; colors: DiagramColors }): string {
	const background = cssColor(colors.background, colors.scheme === "dark" ? "#0a0a0a" : "#ffffff");
	const theme = colors.scheme === "dark" ? "dark" : "default";
	return `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="${CSP}">
<meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=6, user-scalable=yes">
<style>
html, body { margin: 0; padding: 0; background: ${background}; color-scheme: ${colors.scheme}; }
#diagram { padding: 16px; box-sizing: border-box; display: flex; justify-content: center; }
#diagram svg { max-width: 100%; height: auto; }
</style>
</head>
<body>
<div id="diagram"></div>
<script>${inlineScript(runtime)}</script>
<script>
(async function () {
	var post = function (message) { window.ReactNativeWebView.postMessage(JSON.stringify(message)); };
	var code = ${scriptString(code)};
	try {
		mermaid.initialize({ startOnLoad: false, theme: ${scriptString(theme)}, securityLevel: "strict", suppressErrorRendering: true });
		// Parse first, so a syntax error fails here instead of drawing an error box that reads as a diagram.
		await mermaid.parse(code);
		var result = await mermaid.render("ao-diagram", code);
		document.getElementById("diagram").innerHTML = result.svg;
		post({ type: "rendered" });
	} catch (error) {
		post({ type: "failed", message: String((error && error.message) || error) });
	}
})();
</script>
</body>
</html>`;
}

/** A message the page posted, or undefined for anything else. */
export function parseDiagramMessage(data: string): DiagramMessage | undefined {
	try {
		const message = JSON.parse(data) as { type?: unknown; message?: unknown };
		if (message.type === "rendered") return { type: "rendered" };
		if (message.type === "failed") return { type: "failed", message: typeof message.message === "string" ? message.message : "" };
	} catch {
		// Not one of ours.
	}
	return undefined;
}
