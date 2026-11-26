import type { WebContents } from "electron";
import type { BrowserViewHost } from "./browser-view-host";

export async function toggleAppDevTools(
	browserHost: Pick<BrowserViewHost, "toggleDevToolsForLastFocused"> | null,
	getShell: () => Pick<WebContents, "toggleDevTools"> | null,
	allowShellDebugging = false,
): Promise<void> {
	try {
		const state = await browserHost?.toggleDevToolsForLastFocused();
		if (state) return;
	} catch {
		// Development may fall back to the shell when the Browser is unavailable.
	}
	if (allowShellDebugging) getShell()?.toggleDevTools();
}

export function reloadAppView(
	browserHost: Pick<BrowserViewHost, "getLastFocusedPanelContents" | "reloadAgentTab"> | null,
	getShell: () => Pick<WebContents, "reload" | "reloadIgnoringCache"> | null,
	allowShellDebugging = false,
	ignoreCache = false,
): void {
	const browser = browserHost?.getLastFocusedPanelContents();
	const target = browser ?? (allowShellDebugging ? getShell() : null);
	if (!target) return;
	if (!ignoreCache && browser && browserHost?.reloadAgentTab(browser)) return;
	if (ignoreCache) target.reloadIgnoringCache();
	else target.reload();
}
