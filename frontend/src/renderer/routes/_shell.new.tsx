import { createFileRoute } from "@tanstack/react-router";
import { MigrationPopup } from "../components/MigrationPopup";
import { NewChatPage } from "../components/NewChatPage";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";

// `home` marks the arrival from app open, which also lists recent work.
type NewChatSearch = { project?: string; host?: string; home?: true };

export const Route = createFileRoute("/_shell/new")({
	validateSearch: (search: Record<string, unknown>): NewChatSearch => ({
		project: typeof search.project === "string" && search.project ? search.project : undefined,
		host: typeof search.host === "string" && search.host ? search.host : undefined,
		home: search.home === true || search.home === "true" ? true : undefined,
	}),
	component: NewChatRoute,
});

function NewChatRoute() {
	const { project, host, home } = Route.useSearch();
	// Keyed by host: picks made for one machine never carry over to another.
	return (
		<>
			<MigrationPopup />
			<NewChatPage key={host ?? "local"} projectId={project ?? STANDALONE_WORKSPACE_ID} hostId={host} showRecents={home} />
		</>
	);
}
