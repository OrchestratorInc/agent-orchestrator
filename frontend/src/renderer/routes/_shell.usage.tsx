import { createFileRoute, Navigate } from "@tanstack/react-router";
import { UsageView } from "../components/usage/UsageView";
import { useUiStore } from "../stores/ui-store";

// ponytail: the Usage page is Developer Mode only for now; drop this gate,
// and the sidebar and command palette ones, to show it to everyone.
function UsageRoute() {
	const developerMode = useUiStore((state) => state.developerMode);
	return developerMode ? <UsageView /> : <Navigate to="/" replace />;
}

export const Route = createFileRoute("/_shell/usage")({ component: UsageRoute });
