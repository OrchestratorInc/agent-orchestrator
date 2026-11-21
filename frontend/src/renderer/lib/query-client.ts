import { MutationCache, QueryCache, QueryClient, focusManager } from "@tanstack/react-query";
import { captureRendererException } from "./telemetry";

export const queryClient = new QueryClient({
	queryCache: new QueryCache({ onError: (error) => { void captureRendererException(error, { operation: "query" }); } }),
	mutationCache: new MutationCache({ onError: (error) => { void captureRendererException(error, { operation: "mutation" }); } }),
	defaultOptions: {
		queries: {
			staleTime: 10_000,
			refetchOnWindowFocus: false,
			// AO talks to a localhost daemon, so its queries must run regardless of
			// the browser's online flag. React Query's default (networkMode
			// "online") pauses every query when navigator.onLine is false, which on
			// a flaky internet connection strands even local reads (e.g. settings,
			// which gates the cloud sign-in UI) in a perpetual loading state.
			networkMode: "always",
		},
		mutations: {
			networkMode: "always",
		},
	},
});

// React Query treats a hidden document as unfocused, and while unfocused it
// pauses every retry and refetchInterval. A browser tab can afford that; AO
// cannot: its window is often hidden while agents work, and a read that
// failed once (or a poll, such as a cloud approval's) then waited for the
// window to be shown again. So the app always counts as focused, and showing
// the window still tells queries that refetch on focus to do so.
focusManager.setEventListener((setFocused) => {
	setFocused(true);
	if (typeof window === "undefined") return undefined;
	const onVisibilityChange = () => {
		if (document.visibilityState === "visible") focusManager.onFocus();
	};
	window.addEventListener("visibilitychange", onVisibilityChange, false);
	return () => window.removeEventListener("visibilitychange", onVisibilityChange);
});
