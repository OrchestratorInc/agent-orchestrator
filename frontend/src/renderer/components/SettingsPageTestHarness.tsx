import { useTranslation } from "react-i18next";
import { useRef } from "react";
import { SwipeNavigation } from "./SwipeNavigation";
import { SettingsPane, SettingsProvider, useSettingsPage } from "./SettingsDialog";
import { SettingsSidebarNav } from "./Sidebar";
import { SidebarProvider } from "./ui/sidebar";
import { TooltipProvider } from "./ui/tooltip";

function Nav() {
	const { t } = useTranslation();
	const layer = useSettingsPage();
	if (!layer) return null;
	return (
		<>
			<SettingsSidebarNav layer={layer} />
			{/* Mirrors the sidebar footer's Back row. */}
			<button disabled={layer.cueBusy} onClick={layer.close} type="button">{t("settings.back")}</button>
		</>
	);
}

function CenterPane() {
	const layer = useSettingsPage();
	const ref = useRef<HTMLDivElement>(null);
	return <div ref={ref}><SettingsPane />{layer && <SwipeNavigation containerRef={ref} navigation={layer.navigation} />}</div>;
}

/** Test-only stand-in for the shell: settings sidebar section beside the settings pane. */
export function SettingsDialog() {
	return (
		<SettingsProvider>
			<TooltipProvider>
				<SidebarProvider>
					<Nav />
					<CenterPane />
				</SidebarProvider>
			</TooltipProvider>
		</SettingsProvider>
	);
}
