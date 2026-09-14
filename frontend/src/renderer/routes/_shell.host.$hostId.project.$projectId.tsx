import { createFileRoute } from "@tanstack/react-router";
import { SessionsBoard } from "../components/SessionsBoard";
import { preloadSessionUsageSummaries } from "../hooks/useSessionUsageSummaries";
import { refKey } from "../lib/hosts";

export const Route = createFileRoute("/_shell/host/$hostId/project/$projectId")({
	loader: ({ context, params }) =>
		preloadSessionUsageSummaries(context.queryClient, params.projectId, params.hostId),
	component: HostProjectBoardRoute,
});

function HostProjectBoardRoute() {
	const { hostId, projectId } = Route.useParams();
	return <SessionsBoard key={refKey({ host: hostId, id: projectId })} hostId={hostId} projectId={projectId} />;
}
