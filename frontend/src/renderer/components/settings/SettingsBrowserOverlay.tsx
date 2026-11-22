import { FocusScope } from "@radix-ui/react-focus-scope";
import { createContext, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { BrowserPanelView, useBrowserAnnotationQueue } from "../BrowserPanel";
import { SessionBrowserPopOut } from "../SessionBrowserPopOut";
import { Button } from "../ui/button";
import { useBrowserView } from "../../hooks/useBrowserView";

// Settings can run login terminals without a selected session. This Browser
// belongs to the Settings page, so opening a device link never stops the PTY.
const SETTINGS_BROWSER_ID = "settings-login-browser";

export const SettingsBrowserLinkContext = createContext<((url: string) => void) | undefined>(undefined);

export function SettingsBrowserOverlay({ request, onClose }: {
	request: { id: number; url: string };
	onClose: () => void;
}) {
	const { t } = useTranslation();
	const [topbarHost, setTopbarHost] = useState<HTMLDivElement | null>(null);
	const overlayRoot = useRef<HTMLDivElement>(null);
	const hasNativeBrowser = Boolean(window.ao?.browser);
	const browserView = useBrowserView({ sessionId: `${SETTINGS_BROWSER_ID}:${request.id}`, active: true, poppedOut: true, terminated: false, transient: true });
	const annotationQueue = useBrowserAnnotationQueue({});
	useEffect(() => {
		const root = overlayRoot.current;
		if (!root) return;
		const siblings = Array.from(document.body.children).filter((child) => child !== root);
		const previouslyInert = siblings.map((child) => child.hasAttribute("inert"));
		siblings.forEach((child) => child.setAttribute("inert", ""));
		return () => siblings.forEach((child, index) => {
			if (!previouslyInert[index]) child.removeAttribute("inert");
		});
	}, []);
	useEffect(() => {
		if (!hasNativeBrowser) return;
		void browserView.openLink(request.url).catch((error) => {
			console.warn("Unable to open login link in Browser", error);
		});
	}, [browserView.openLink, hasNativeBrowser, request.id, request.url]);
	return (
		<FocusScope asChild loop trapped onMountAutoFocus={(event) => event.preventDefault()} onUnmountAutoFocus={(event) => event.preventDefault()}>
			<div ref={overlayRoot}>
				<SessionBrowserPopOut onClose={onClose} onTopbarHost={setTopbarHost} phase={topbarHost ? "open" : "mounting"}>
					{topbarHost && !hasNativeBrowser ? <div className="m-auto flex flex-col items-center gap-3 p-6 text-center text-sm text-muted-foreground">
						<p role="alert">{t("browser.nativeUnavailable", { defaultValue: "Orchestrator Browser is unavailable in this window." })}</p>
						<Button onClick={onClose} size="sm" type="button" variant="quiet">{t("common.close")}</Button>
					</div> : null}
					{topbarHost && hasNativeBrowser ? <BrowserPanelView
						active
						annotationQueue={annotationQueue}
						browserView={browserView}
						onTogglePopOut={(next) => { if (!next) onClose(); }}
						poppedOut
						topbarHost={topbarHost}
					/> : null}
				</SessionBrowserPopOut>
			</div>
		</FocusScope>
	);
}
