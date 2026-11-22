export const SESSION_BROWSER_POPOUT_REQUEST = "ao:session-browser-popout-request";

export function requestSessionBrowserPopOut(): void {
	window.dispatchEvent(new Event(SESSION_BROWSER_POPOUT_REQUEST));
}
