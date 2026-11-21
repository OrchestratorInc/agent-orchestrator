import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import type { ReactElement, ReactNode } from "react";
import { TooltipProvider } from "../components/ui/tooltip";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";

/** Chat rows resolve worker/session links through the app's query context. */
export function renderChat(ui: ReactElement) {
	const client = new QueryClient({
		defaultOptions: {
			queries: {
				retry: false,
				gcTime: 0,
				refetchOnMount: false,
				refetchOnWindowFocus: false,
				refetchOnReconnect: false,
			},
		},
	});
	// These fixture-only chats have no daemon. Keep their workspace reads local.
	client.setQueryData(workspaceQueryKey, []);
	return render(ui, {
		wrapper: ({ children }: { children: ReactNode }) => (
			<QueryClientProvider client={client}>
				<TooltipProvider>{children}</TooltipProvider>
			</QueryClientProvider>
		),
	});
}
