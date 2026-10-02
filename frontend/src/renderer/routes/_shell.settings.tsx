import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useEffect } from "react";
import { resolveGlobalSettingsSection, useUiStore, type GlobalSettingsSection } from "../stores/ui-store";

export const Route = createFileRoute("/_shell/settings")({
	validateSearch: (search: Record<string, unknown>): { section?: GlobalSettingsSection } => ({
		section: resolveGlobalSettingsSection(search.section),
	}),
	component: LegacyGlobalSettingsRoute,
});

// Deep-link / bookmark shim: settings is a modal now. In-app openers call
// openGlobalSettings() directly; this route only handles cold loads of /settings.
function LegacyGlobalSettingsRoute() {
	const navigate = useNavigate();
	const { section } = Route.useSearch();
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);

	useEffect(() => {
		openGlobalSettings(section);
		void navigate({ to: "/", replace: true });
	}, [navigate, openGlobalSettings, section]);

	return null;
}
