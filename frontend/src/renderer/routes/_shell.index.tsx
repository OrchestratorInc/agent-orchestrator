import { createFileRoute, Navigate } from "@tanstack/react-router";
import { DaemonStartupLoader } from "../components/DaemonStartupLoader";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { lastProjectId } from "../lib/last-project";
import { useShell } from "../lib/shell-context";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import { useSystemRequirementsGate } from "../hooks/useSystemRequirementsGate";

export const Route = createFileRoute("/_shell/")({
	component: ShellIndex,
});

// There is no home page: the root always opens the new chat page, on the last
// project the user worked in (the scratchpad when there is none).
function ShellIndex() {
	const { daemonStatus, workspaceStartupState } = useShell();
	const { blocked: requirementsBlocked } = useSystemRequirementsGate();
	const workspaceQuery = useWorkspaceQuery();
	const daemonFailed = Boolean(daemonStatus.code);
	const ready =
		daemonFailed ||
		workspaceQuery.isError ||
		((usesPreviewWorkspaceData || daemonStatus.state === "ready") &&
			workspaceStartupState !== "loading" &&
			workspaceQuery.isSuccess &&
			!requirementsBlocked);
	if (!ready) return <DaemonStartupLoader />;
	return <Navigate to="/new" search={{ project: lastProjectId(workspaceQuery.data ?? []), home: true }} replace />;
}
