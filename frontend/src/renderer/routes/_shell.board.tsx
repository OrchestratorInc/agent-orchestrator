import { createFileRoute } from "@tanstack/react-router";
import { SessionsBoard } from "../components/SessionsBoard";
import { useRemoteWorkspaces } from "../hooks/useWorkspaceQuery";
import { useConnectedHosts } from "../hooks/useHostConnection";

export const Route = createFileRoute("/_shell/board")({
	component: AllSessionsRoute,
});

function AllSessionsRoute() {
	const remote = useRemoteWorkspaces();
	const connected = useConnectedHosts();
	const loading = connected.some((hostId) => !remote.failedHostIds.includes(hostId) && (
		!remote.loadedProjectHostIds.includes(hostId) || !remote.loadedSessionHostIds.includes(hostId)
	));
	return <SessionsBoard
		remoteWorkspaces={remote.data}
		remoteLoading={loading}
		failedHostIds={remote.failedHostIds}
		onRetryRemoteHosts={() => void remote.refetch()}
	/>;
}
