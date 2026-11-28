import { useEffect } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { useBrowserView } from "../hooks/useBrowserView";
import { useBrowserWindowStore, type BrowserWindowEntry } from "../stores/browser-window-store";

/**
 * Renders each session's Browser page into its own window. Mounted once in the
 * shell, outside the routed session view, so a window keeps its page while the
 * app window shows another session or page.
 */
export function BrowserWindowsHost() {
	const windows = useBrowserWindowStore((state) => state.windows);
	useEffect(
		() => window.ao?.browser.onWindowState((state) => useBrowserWindowStore.getState().applyWindowState(state)),
		[],
	);
	// `ao browser record` asks for the session's window, whichever session the
	// app window is showing.
	useEffect(
		() =>
			window.ao?.browser.onOpenWindowRequest(({ viewId, sessionId }) => {
				useBrowserWindowStore.getState().openWindow(sessionId, viewId);
			}),
		[],
	);
	return (
		<>
			{Object.entries(windows).map(([uiSessionId, entry]) =>
				entry.container
					? createPortal(<BrowserWindowPage entry={entry} uiSessionId={uiSessionId} />, entry.container, uiSessionId)
					: null,
			)}
		</>
	);
}

/**
 * The window shows the active tab's page and nothing else: tabs, the address
 * bar and the controls stay in the app window's panel. Closing the window, or
 * Move back here in the panel, brings the page back.
 */
function BrowserWindowPage({ entry, uiSessionId }: { entry: BrowserWindowEntry; uiSessionId: string }) {
	const { t } = useTranslation();
	const { child, panel, viewId } = entry;
	const browserView = useBrowserView({
		sessionId: uiSessionId,
		origin: panel?.origin,
		cloudAgentBrowser: panel?.cloudAgentBrowser,
		active: true,
		poppedOut: false,
		surface: "window",
		terminated: panel?.terminated ?? false,
		sessionEnded: panel?.sessionEnded,
		previewUrl: panel?.previewUrl,
		previewRevision: panel?.previewRevision,
	});
	const pageTitle = browserView.navState.title;
	const windowTitle = t("browser.windowTitle");
	useEffect(() => {
		if (!child.closed) child.document.title = pageTitle ? `${pageTitle} · ${windowTitle}` : windowTitle;
	}, [child, pageTitle, windowTitle]);
	// The page closing its last tab closes the panel: here, the window.
	useEffect(
		() =>
			window.ao?.browser.onClosePanel((targetViewId) => {
				if (targetViewId === viewId) void window.ao?.browser.closeWindow(viewId);
			}),
		[viewId],
	);
	return <div className="browser-window-page" data-testid="browser-window-page" ref={browserView.slotRef} />;
}
