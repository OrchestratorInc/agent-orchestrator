import { createFileRoute } from "@tanstack/react-router";
import { SessionsBoard } from "../components/SessionsBoard";

export const Route = createFileRoute("/_shell/projects/$projectId")({
	component: ProjectBoardRoute,
});

function ProjectBoardRoute() {
	const { projectId } = Route.useParams();
	return <SessionsBoard projectId={projectId} />;
}
