import { createFileRoute } from "@tanstack/react-router";
import { LiveGrid } from "../components/LiveGrid";
import { refKey } from "../lib/hosts";

export const Route = createFileRoute("/_shell/host/$hostId/project/$projectId_/grid")({ component: GridRoute });
function GridRoute() {
	const { hostId, projectId } = Route.useParams();
	return <LiveGrid key={refKey({ host: hostId, id: projectId })} hostId={hostId} projectId={projectId} />;
}
