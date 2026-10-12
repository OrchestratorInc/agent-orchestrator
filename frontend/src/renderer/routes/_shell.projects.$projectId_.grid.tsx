import { createFileRoute } from "@tanstack/react-router";
import { LiveGrid } from "../components/LiveGrid";

export const Route = createFileRoute("/_shell/projects/$projectId_/grid")({ component: GridRoute });
function GridRoute() {
	const { projectId } = Route.useParams();
	return <LiveGrid key={projectId} projectId={projectId} />;
}
