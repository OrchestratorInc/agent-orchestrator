import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useEffect } from "react";
import { useUiStore } from "../stores/ui-store";

export const Route = createFileRoute("/_shell/settings")({
	component: LegacyGlobalSettingsRoute,
});

function LegacyGlobalSettingsRoute() {
	const navigate = useNavigate();
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);

	useEffect(() => {
		openGlobalSettings();
		void navigate({ to: "/", replace: true });
	}, [navigate, openGlobalSettings]);

	return null;
}
