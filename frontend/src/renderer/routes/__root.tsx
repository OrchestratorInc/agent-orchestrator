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
	const loadKeybindings = useKeybindingsStore((state) => state.load);

	useEffect(() => {
		void loadKeybindings();
	}, [loadKeybindings]);

	return (
		<TooltipProvider>
			<RouteViewed />
			<Outlet />
		</TooltipProvider>
	);
}

// Its own component, so a navigation does not re-render the root and every tooltip under it.
function RouteViewed() {
	const pathname = useRouterState({ select: (state) => state.location.pathname });
	useEffect(() => {
		void captureRendererEvent("ao.routes.viewed", { surface: routeSurface(pathname) });
	}, [pathname]);
	return null;
}
