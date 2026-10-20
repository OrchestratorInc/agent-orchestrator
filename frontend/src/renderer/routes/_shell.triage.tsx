import { createFileRoute } from "@tanstack/react-router";
import { TriageWorkspaceView } from "../components/triage/TriageWorkspaceView";

export const Route = createFileRoute("/_shell/triage")({
	validateSearch: (search: Record<string, unknown>) => ({ run: typeof search.run === "string" ? search.run : undefined }),
	component: TriagePage,
});

function TriagePage() {
	return <TriageWorkspaceView runId={Route.useSearch().run} />;
}
