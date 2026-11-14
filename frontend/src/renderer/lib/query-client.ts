import { MutationCache, QueryCache, QueryClient } from "@tanstack/react-query";
import { captureRendererException } from "./telemetry";

export const queryClient = new QueryClient({
	queryCache: new QueryCache({ onError: (error) => { void captureRendererException(error, { operation: "query" }); } }),
	mutationCache: new MutationCache({ onError: (error) => { void captureRendererException(error, { operation: "mutation" }); } }),
	defaultOptions: {
		queries: {
			staleTime: 10_000,
			refetchOnWindowFocus: false,
			networkMode: "always",
		},
		mutations: {
			networkMode: "always",
		},
	},
});
