import type { ReactNode } from "react";
import { cn } from "../lib/utils";
import { useWindowFullScreen } from "../hooks/useWindowFullScreen";
import { isLinuxPlatform, isMacPlatform } from "../lib/platform";
import { sidebarOccupiesLayout, useUiStore } from "../stores/ui-store";
import { PageTopbarSurface } from "./PageTopbar";

export function CenterPanelShell({
	className,
	children,
	titlebarAlign = true,
	notifications = false,
	topbar = true,
}: {
	className?: string;
	children: ReactNode;
	titlebarAlign?: boolean;
	notifications?: boolean;
	topbar?: boolean;
}) {
	const isSidebarOpen = useUiStore(sidebarOccupiesLayout);
	const isFullScreen = useWindowFullScreen();
	const isMac = isMacPlatform();
	const isLinux = isLinuxPlatform();
	const align = titlebarAlign && isMac;
	const titlebarClearance = align && !isSidebarOpen;
	const linuxTitlebarClearance = titlebarAlign && isLinux && !isSidebarOpen;
	const reservesTopbarBand = topbar || titlebarClearance;
	const floatingTopbar = align && !isFullScreen;
	const bare = floatingTopbar && !reservesTopbarBand;

	return (
		<div
			className={cn(
				"center-panel-shell",
				align && "center-panel-shell--mac",
				floatingTopbar && reservesTopbarBand && "center-panel-shell--floating-topbar",
				bare && "center-panel-shell--bare",
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
