import { useEffect, useState, type ReactNode } from "react";
import { cn } from "../lib/utils";
import { useWindowFullScreen } from "../hooks/useWindowFullScreen";
import { newDesktopUiEnabled } from "../lib/new-desktop-ui";
import { isLinuxPlatform, isMacPlatform } from "../lib/platform";
import { sidebarOccupiesLayout, useUiStore } from "../stores/ui-store";
import { PageTopbarSurface } from "./PageTopbar";

/**
 * Shared inset center panel: sidebar-colored outer frame with a bordered inner
 * surface. Used by the shell's app routes (kanban / session), the welcome board,
 * and settings. Chrome lives in `styles.css` (`center-panel-shell` +
 * `center-panel-surface`).
 *
 * `titlebarAlign` (default true) pulls Board/Terminal titles up level with the
 * fixed TitlebarNav cluster on macOS and Linux. Windows keeps those controls in
 * its custom titlebar, so it does not need this clearance.
 */
export function CenterPanelShell({
	className,
	children,
	titlebarAlign = true,
	notifications = false,
	topbar = true,
}: {
	/** Extra classes on the outer frame. */
	className?: string;
	children: ReactNode;
	/** When false, keep the default panel insets (Settings). */
	titlebarAlign?: boolean;
	/** Workspace pages opt in; utility and settings pages keep quiet chrome. */
	notifications?: boolean;
	/** False for a page with no top bar: the panel grows into the band above it. */
	topbar?: boolean;
}) {
	const isSidebarOpen = useUiStore(sidebarOccupiesLayout);
	const newDesktopUi = useUiStore(newDesktopUiEnabled);
	const isFullScreen = useWindowFullScreen();
	const isMac = isMacPlatform();
	const isLinux = isLinuxPlatform();
	const align = titlebarAlign && isMac;
	const titlebarClearance = align && !isSidebarOpen;
	const linuxTitlebarClearance = titlebarAlign && isLinux && !isSidebarOpen;
	// With the sidebar closed the traffic lights and nav cluster float over the
	// panel's top-left corner, so the band stays even when the page has no bar.
	const reservesTopbarBand = topbar || titlebarClearance;
	// On macOS the top bar floats on the window chrome, except in native fullscreen.
	const floatingTopbar = align && !isFullScreen;
	const bare = floatingTopbar && !reservesTopbarBand;
	// Interface v2 draws a bare panel with the floating bar's frame too, its top
	// at the window inset, so opening or closing the sidebar only slides the top
	// edge instead of swapping frames.
	const floatingFrame = floatingTopbar && (reservesTopbarBand || newDesktopUi);
	// Tabs are pills on every macOS panel, whatever its band, so no frame can
	// draw them in the other platforms' flat strip.
	const pillTabs = align;
	// The panel stays mounted across pages. When the page's bar comes or goes,
	// the frame's top edge snaps for one frame instead of sliding: only a sidebar
	// toggle slides it. Derived in render so the class lands in the same commit.
	const [settledTopbar, setSettledTopbar] = useState(topbar);
	const edgeSnap = newDesktopUi && settledTopbar !== topbar;
	useEffect(() => {
		if (!edgeSnap) return;
		const frame = requestAnimationFrame(() => setSettledTopbar(topbar));
		return () => cancelAnimationFrame(frame);
	}, [edgeSnap, topbar]);

	return (
		<div
			className={cn(
				"center-panel-shell",
				align && "center-panel-shell--mac",
				// The top bar floats on the window chrome; the panel starts below it.
				floatingFrame && "center-panel-shell--floating-topbar",
				bare && "center-panel-shell--bare",
				edgeSnap && "center-panel-shell--edge-snap",
				pillTabs && "center-panel-shell--tab-pills",
				titlebarClearance && "center-panel-shell--titlebar-clearance",
				titlebarClearance && isFullScreen && "center-panel-shell--titlebar-clearance-fullscreen",
				linuxTitlebarClearance && "center-panel-shell--titlebar-clearance-linux",
				align && isFullScreen && "center-panel-shell--fullscreen",
				className,
			)}
		>
			<PageTopbarSurface notifications={notifications}>{children}</PageTopbarSurface>
		</div>
	);
}
