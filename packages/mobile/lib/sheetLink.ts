// A web page must not open on top of a native formSheet: when the in-app
// browser (SFSafariViewController) is presented from the sheet and then
// dismissed, the sheet comes back full-height with no grabber and cannot be
// swiped closed. A sheet parks the link here and closes itself; the screen
// underneath opens it once it is focused again, after the sheet is gone.

let pending: string | undefined;

export function parkSheetLink(url: string): void {
	pending = url;
}

/** The parked link, cleared so it opens at most once. */
export function takeSheetLink(): string | undefined {
	const url = pending;
	pending = undefined;
	return url;
}
