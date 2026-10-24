import { useLocalSearchParams, useRouter } from "expo-router";
import { useEffect } from "react";
import { ProjectPickerSheet } from "../../lib/ProjectPickerSheet";
import { releaseSheetResult, takeSheetResult } from "../../lib/sheetResult";
import { useApp } from "../../lib/store";
import { backOr } from "../../lib/backNavigation";

// The project picker, as a native form sheet. Opened from the board's
// ProjectSwitcher, Settings and the spawn screen — see `openProjectSheet`.
export default function ProjectSheetRoute() {
	const router = useRouter();
	const app = useApp();
	const { resultKey, selected, includeAll, title, subtitle, hostId } = useLocalSearchParams<{
		hostId?: string;
		resultKey?: string;
		selected?: string;
		includeAll?: string;
		title?: string;
		subtitle?: string;
	}>();
	const projects = hostId === undefined ? app.projects : app.hostStates.find((host) => host.hostId === hostId)?.projects ?? [];

	// Release the parked callback however the sheet goes away, including a
	// swipe-dismiss that never chose anything.
	useEffect(() => () => releaseSheetResult(resultKey), [resultKey]);

	return (
		<ProjectPickerSheet
			projects={projects}
			activeProjectId={selected ?? ""}
			// Params are strings over the URL, so the flag arrives as "0"/"1".
			includeAll={includeAll !== "0"}
			title={title}
			subtitle={subtitle}
			onClose={() => backOr(router)}
			onSelect={(id) => takeSheetResult<string>(resultKey)?.(id)}
		/>
	);
}

export { SheetErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
