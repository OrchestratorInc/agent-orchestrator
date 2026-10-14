import { createFileRoute } from "@tanstack/react-router";
import { NewChatPage } from "../components/NewChatPage";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";

type NewChatSearch = { project?: string; host?: string };

export const Route = createFileRoute("/_shell/new")({
	validateSearch: (search: Record<string, unknown>): NewChatSearch => ({
		project: typeof search.project === "string" && search.project ? search.project : undefined,
		host: typeof search.host === "string" && search.host ? search.host : undefined,
	}),
	component: NewChatRoute,
});

function NewChatRoute() {
	const { project, host } = Route.useSearch();
	return <NewChatPage projectId={project ?? STANDALONE_WORKSPACE_ID} hostId={host} />;
}
