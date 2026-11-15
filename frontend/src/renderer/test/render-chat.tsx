import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import type { ReactElement, ReactNode } from "react";
import { TooltipProvider } from "../components/ui/tooltip";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";

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
	client.setQueryData(workspaceQueryKey, []);
	return render(ui, {
		wrapper: ({ children }: { children: ReactNode }) => (
			<QueryClientProvider client={client}>
				<TooltipProvider>{children}</TooltipProvider>
			</QueryClientProvider>
		),
	});
}
