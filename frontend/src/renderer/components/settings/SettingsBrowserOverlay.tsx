import { FocusScope } from "@radix-ui/react-focus-scope";
import { createContext, useEffect, useRef, useState } from "react";
import { BrowserPanelView, useBrowserAnnotationQueue } from "../BrowserPanel";
import { SessionBrowserPopOut } from "../SessionBrowserPopOut";
import { useBrowserView } from "../../hooks/useBrowserView";

// Settings can run login terminals without a selected session. This Browser
// belongs to the Settings page, so opening a device link never stops the PTY.
const SETTINGS_BROWSER_ID = "settings-login-browser";

export const SettingsBrowserLinkContext = createContext<((url: string) => void) | undefined>(undefined);

export function SettingsBrowserOverlay({ request, onClose }: {
	request: { id: number; url: string };
	onClose: () => void;
}) {
	const [topbarHost, setTopbarHost] = useState<HTMLDivElement | null>(null);
	const overlayRoot = useRef<HTMLDivElement>(null);
	const browserView = useBrowserView({ sessionId: SETTINGS_BROWSER_ID, active: true, poppedOut: true, terminated: false });
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
		void browserView.openLink(request.url).catch((error) => {
			console.warn("Unable to open login link in Browser", error);
		});
	}, [browserView.openLink, request.id, request.url]);
	return (
		<FocusScope asChild loop trapped onMountAutoFocus={(event) => event.preventDefault()} onUnmountAutoFocus={(event) => event.preventDefault()}>
			<div ref={overlayRoot}>
				<SessionBrowserPopOut onClose={onClose} onTopbarHost={setTopbarHost} phase={topbarHost ? "open" : "mounting"}>
					{topbarHost ? <BrowserPanelView
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
