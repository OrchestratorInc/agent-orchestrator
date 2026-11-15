import { createHashHistory, createRouter, useRouterState } from "@tanstack/react-router";
import type { QueryClient } from "@tanstack/react-query";
import { DaemonStartupLoader } from "./components/DaemonStartupLoader";
import { routeTree } from "./routeTree.gen";
import { reportRouteError } from "./components/RouteErrorCapture";

export function AppPendingFallback() {
	const isInitialLoad = useRouterState({ select: (state) => state.resolvedLocation === undefined });
	return isInitialLoad ? <DaemonStartupLoader /> : null;
}

export function createAppRouter(queryClient: QueryClient) {
	return createRouter({
		history: createHashHistory(),
		routeTree,
		context: { queryClient },
		defaultOnCatch: reportRouteError,
		defaultPreload: "intent",
		defaultPendingComponent: AppPendingFallback,
		defaultPendingMs: 0,
		defaultPendingMinMs: 0,
		defaultPreloadStaleTime: 0,
		scrollRestoration: true,
	});
}
