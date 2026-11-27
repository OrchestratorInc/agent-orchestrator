// The Browser's own window starts as an empty about:blank document the app
// shell opened. The shell renders the page's slot into it, so it needs the shell's
// styles and the <html> attributes those styles key on (theme, platform,
// native composition), kept in step as they change.

const STYLE_SELECTOR = 'style, link[rel="stylesheet"]';
// Attributes of the app's <html> that describe the app window alone.
const APP_WINDOW_ONLY_ATTRIBUTES = new Set(["data-browser-hole", "data-browser-native-live"]);
// Set on the window's <html>: the popout flag clears the shell's opaque page
// layers, so the page shows through wherever the panel paints nothing.
const WINDOW_ATTRIBUTES: Record<string, string> = {
	"data-browser-popout-open": "true",
	"data-browser-window": "true",
};

export type BrowserWindowDocument = {
	/** Where the panel renders. */
	container: HTMLElement;
	dispose: () => void;
};

// What the app's styles are, to skip head changes (its title) that leave them be.
function styleSignature(source: Document): string {
	return [...source.head.querySelectorAll(STYLE_SELECTOR)]
		.map((node) => (node.tagName === "LINK" ? (node as HTMLLinkElement).href : node.textContent ?? ""))
		.join("\u0000");
}

function copyStyles(source: Document, target: Document, copied: Node[]): void {
	for (const node of copied.splice(0)) node.parentNode?.removeChild(node);
	for (const node of source.head.querySelectorAll(STYLE_SELECTOR)) {
		let clone: HTMLElement;
		if (node.tagName === "LINK") {
			const link = target.createElement("link");
			link.rel = "stylesheet";
			// Absolute, so it resolves the same from about:blank.
			link.href = (node as HTMLLinkElement).href;
			clone = link;
		} else {
			clone = target.createElement("style");
			clone.textContent = node.textContent;
		}
		target.head.appendChild(clone);
		copied.push(clone);
	}
}

function copyRootAttributes(source: HTMLElement, target: HTMLElement): void {
	for (const { name } of [...target.attributes]) {
		if (!(name in WINDOW_ATTRIBUTES) && !source.hasAttribute(name)) target.removeAttribute(name);
	}
	for (const { name, value } of source.attributes) {
		if (APP_WINDOW_ONLY_ATTRIBUTES.has(name) || name in WINDOW_ATTRIBUTES) continue;
		if (target.getAttribute(name) !== value) target.setAttribute(name, value);
	}
	for (const [name, value] of Object.entries(WINDOW_ATTRIBUTES)) target.setAttribute(name, value);
}

export function prepareBrowserWindowDocument(child: Window, source: Document = document): BrowserWindowDocument {
	const target = child.document;
	const copied: Node[] = [];
	let signature = styleSignature(source);
	copyStyles(source, target, copied);
	copyRootAttributes(source.documentElement, target.documentElement);
	target.body.className = source.body.className;
	const container = target.createElement("div");
	container.className = "browser-window-root";
	target.body.appendChild(container);

	// Theme and appearance changes land on the app's <html>; development style
	// updates replace the app's <style> elements.
	const rootObserver = new MutationObserver(() => copyRootAttributes(source.documentElement, target.documentElement));
	rootObserver.observe(source.documentElement, { attributes: true });
	let pendingStyles = false;
	const headObserver = new MutationObserver(() => {
		if (pendingStyles) return;
		pendingStyles = true;
		queueMicrotask(() => {
			pendingStyles = false;
			const next = styleSignature(source);
			if (child.closed || next === signature) return;
			signature = next;
			copyStyles(source, target, copied);
		});
	});
	headObserver.observe(source.head, { childList: true, subtree: true, characterData: true });

	return {
		container,
		dispose: () => {
			rootObserver.disconnect();
			headObserver.disconnect();
		},
	};
}
