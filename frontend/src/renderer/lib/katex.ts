import "katex/dist/katex.min.css";

export { default } from "katex";

function formulaAround(node: Node): HTMLElement | null {
	return (node instanceof Element ? node : node.parentElement)?.closest<HTMLElement>("[data-tex]") ?? null;
}

document.addEventListener("copy", (event) => {
	const selection = window.getSelection();
	if (!event.clipboardData || !selection || selection.isCollapsed || selection.rangeCount === 0) return;
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
		const source = document.createElement(display ? "p" : "span");
		source.style.whiteSpace = "pre-wrap";
		source.textContent = display ? `$$\n${tex}\n$$` : `$${tex}$`;
		formula.replaceWith(source);
	}
	copy.style.cssText = "position: fixed; left: -100000px; top: 0;";
	document.body.append(copy);
	const text = copy.innerText;
	copy.remove();
	event.clipboardData.setData("text/plain", text);
	event.clipboardData.setData("text/html", copy.innerHTML);
	event.preventDefault();
});
