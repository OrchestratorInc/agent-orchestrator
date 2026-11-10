import { createRootRouteWithContext, Outlet, useRouterState } from "@tanstack/react-router";
import { useEffect } from "react";
import { TooltipProvider } from "../components/ui/tooltip";
import type { QueryClient } from "@tanstack/react-query";
import { captureRendererEvent, routeSurface } from "../lib/telemetry";
import { useKeybindingsStore } from "../stores/keybindings-store";

export const Route = createRootRouteWithContext<{
	queryClient: QueryClient;
}>()({
	component: RootComponent,
});

function RootComponent() {
	const location = useRouterState({ select: (state) => state.location });
	const loadKeybindings = useKeybindingsStore((state) => state.load);

	useEffect(() => {
		void captureRendererEvent("ao.routes.viewed", {
			surface: routeSurface(location.pathname),
		});
	}, [location.pathname]);

	useEffect(() => {
		void loadKeybindings();
	}, [loadKeybindings]);

	return (
		<TooltipProvider>
			<Outlet />
		</TooltipProvider>
	);
}
