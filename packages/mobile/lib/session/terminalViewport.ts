type InjectableTerminalRenderer = {
	injectJavaScript: (script: string) => void;
};

export function adjustTerminalViewport(renderer: InjectableTerminalRenderer | null, direction: -1 | 1): boolean {
	if (!renderer) return false;
	renderer.injectJavaScript(`window.__aoAdjustTerminalZoom(${direction}); true;`);
	return true;
}

/** Back to the oldest output: xterm's scrollback, or the app's own in a full-screen TUI. */
export function scrollTerminalToTop(renderer: InjectableTerminalRenderer | null): boolean {
	if (!renderer) return false;
	renderer.injectJavaScript("window.__aoScrollTop(); true;");
	return true;
}

/**
 * Whether a debug message from the WebView reports the scroll-to-top button's
 * visibility, and if so what it says.
 */
export function scrollTopReport(message: unknown): boolean | undefined {
	if (typeof message !== "string" || !message.startsWith("AO_SCROLLTOP ")) return undefined;
	return message.slice("AO_SCROLLTOP ".length) === "1";
}

/** Zooms the phone's viewport back out until the whole grid's width fits. */
export function fitTerminalViewport(renderer: InjectableTerminalRenderer | null): boolean {
	if (!renderer) return false;
	renderer.injectJavaScript("window.__aoFitTerminalWidth(); true;");
	return true;
}
