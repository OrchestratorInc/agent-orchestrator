export const OPEN_DIALOG_OR_MENU_SELECTOR =
	'[role="dialog"][data-state="open"], [role="alertdialog"][data-state="open"], [role="menu"][data-state="open"]';

export const OPEN_BROWSER_OVERLAY_SELECTOR =
	'[role="dialog"][data-state="open"], [role="alertdialog"][data-state="open"], [data-browser-native-overlay="true"][data-state="open"], [data-browser-native-overlay="true"][data-state="delayed-open"], [data-browser-native-overlay="true"][data-state="instant-open"]';

export const BROWSER_OVERLAY_CANDIDATE_SELECTOR =
	'[role="dialog"], [role="alertdialog"], [data-browser-native-overlay="true"]';

export function isDialogOrMenuOpen(): boolean {
	if (typeof document === "undefined") return false;
	return document.querySelector(OPEN_DIALOG_OR_MENU_SELECTOR) !== null;
}

export function isDialogOrMenuOpenOutside(host: Element): boolean {
	if (typeof document === "undefined") return false;
	const elements = document.querySelectorAll(OPEN_DIALOG_OR_MENU_SELECTOR);
	for (const element of elements) {
		if (!element.contains(host)) return true;
	}
	return false;
}
