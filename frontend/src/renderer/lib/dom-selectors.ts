export const OPEN_DIALOG_OR_MENU_SELECTOR =
	'[role="dialog"][data-state="open"], [role="alertdialog"][data-state="open"], [role="menu"][data-state="open"]';

// Native BrowserView pixels sit above the renderer, so only dialogs and
// explicitly marked overlays (browser-panel tooltips/menus, titlebar menu,
// global toast) should raise the shell. The viewport wrapper must stay
// transparent while the shell is raised (see styles.css native-composition
// cascade) or the page blanks out. Radix tooltips never use `data-state="open"`;
// they report `delayed-open` or `instant-open`, so both states are matched.
export const OPEN_BROWSER_OVERLAY_SELECTOR =
	'[role="dialog"][data-state="open"], [role="alertdialog"][data-state="open"], [data-browser-native-overlay="true"][data-state="open"], [data-browser-native-overlay="true"][data-state="delayed-open"], [data-browser-native-overlay="true"][data-state="instant-open"]';

export const BROWSER_OVERLAY_CANDIDATE_SELECTOR =
	'[role="dialog"], [role="alertdialog"], [data-browser-native-overlay="true"]';

export function isDialogOrMenuOpen(): boolean {
	if (typeof document === "undefined") return false;
	return document.querySelector(OPEN_DIALOG_OR_MENU_SELECTOR) !== null;
}

// Like isDialogOrMenuOpen, but ignores a dialog/menu that contains `host` itself.
// A terminal rendered inside its own open dialog (e.g. the harness login
// terminal inside Settings) must still be able to auto-focus; only an
// unrelated dialog/menu elsewhere in the document should block it.
export function isDialogOrMenuOpenOutside(host: Element): boolean {
	if (typeof document === "undefined") return false;
	const elements = document.querySelectorAll(OPEN_DIALOG_OR_MENU_SELECTOR);
	for (const element of elements) {
		if (!element.contains(host)) return true;
	}
	return false;
}
