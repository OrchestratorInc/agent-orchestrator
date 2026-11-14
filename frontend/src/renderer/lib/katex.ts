/**
 * KaTeX with what it needs on the page, as one lazily imported chunk: its
 * stylesheet (and fonts) and the copy handler below. Import this module only
 * through `import()`, so none of it loads until a formula renders.
 */

import "katex/dist/katex.min.css";

export { default } from "katex";

function formulaAround(node: Node): HTMLElement | null {
	return (node instanceof Element ? node : node.parentElement)?.closest<HTMLElement>("[data-tex]") ?? null;
}

/**
 * Copying a selection that touches a formula copies the formula's TeX, not
 * KaTeX's glyphs twice over (its visual HTML and its MathML).
 *
 * The plain text is the browser's own `innerText` of the selection with each
 * formula swapped for its source, so paragraphs, list items and table cells
 * keep their breaks. Reading `textContent` instead loses them, and a streamed
 * reply shows it worst: its blocks render as sibling roots with no whitespace
 * between them, so their text runs together.
 */
document.addEventListener("copy", (event) => {
	const selection = window.getSelection();
	if (!event.clipboardData || !selection || selection.isCollapsed || selection.rangeCount === 0) return;
	// A selection that starts or ends inside a formula takes all of it.
	const range = selection.getRangeAt(0).cloneRange();
	const first = formulaAround(range.startContainer);
	if (first) range.setStartBefore(first);
	const last = formulaAround(range.endContainer);
	if (last) range.setEndAfter(last);
	const copy = document.createElement("div");
	copy.append(range.cloneContents());
	const formulas = copy.querySelectorAll<HTMLElement>("[data-tex]");
	if (formulas.length === 0) return;
	for (const formula of formulas) {
		const tex = formula.dataset.tex ?? "";
		const display = formula.dataset.math === "display";
		// A display formula is its own paragraph, with a blank line either side.
		const source = document.createElement(display ? "p" : "span");
		// pre-wrap keeps the TeX's own line breaks, which end its `%` comments.
		source.style.whiteSpace = "pre-wrap";
		source.textContent = display ? `$$\n${tex}\n$$` : `$${tex}$`;
		formula.replaceWith(source);
	}
	// innerText needs layout, so the copy renders off-screen while it is read.
	copy.style.cssText = "position: fixed; left: -100000px; top: 0;";
	document.body.append(copy);
	const text = copy.innerText;
	copy.remove();
	event.clipboardData.setData("text/plain", text);
	event.clipboardData.setData("text/html", copy.innerHTML);
	event.preventDefault();
});
