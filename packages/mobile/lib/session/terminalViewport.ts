type InjectableTerminalRenderer = {
	injectJavaScript: (script: string) => void;
};

export function adjustTerminalViewport(renderer: InjectableTerminalRenderer | null, direction: -1 | 1): boolean {
	if (!renderer) return false;
	renderer.injectJavaScript(`window.__aoAdjustTerminalZoom(${direction}); true;`);
	return true;
}

/** Zooms the phone's viewport back out until the whole grid's width fits. */
export function fitTerminalViewport(renderer: InjectableTerminalRenderer | null): boolean {
	if (!renderer) return false;
	renderer.injectJavaScript("window.__aoFitTerminalWidth(); true;");
	return true;
}
