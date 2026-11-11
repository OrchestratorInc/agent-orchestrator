import { createFileRoute, Navigate } from "@tanstack/react-router";
import { UsageView } from "../components/usage/UsageView";
import { useUiStore } from "../stores/ui-store";

function UsageRoute() {
	const developerMode = useUiStore((state) => state.developerMode);
	return developerMode ? <UsageView /> : <Navigate to="/" replace />;
}

export const Route = createFileRoute("/_shell/usage")({ component: UsageRoute });
