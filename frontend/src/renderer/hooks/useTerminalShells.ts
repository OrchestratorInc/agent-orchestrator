// Shells installed on this machine, detected by the local daemon for the
// Settings shell picker. The renderer never probes the filesystem itself.

import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";

export type TerminalShell = components["schemas"]["TerminalShellResponse"];

export function useTerminalShells() {
	return useQuery({
		queryKey: ["terminal-shells"],
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/terminal-shells");
			if (error) throw error;
			return data;
		},
		staleTime: 30_000,
	});
}

/** Asks the daemon whether a custom shell path can launch. */
export async function checkTerminalShellPath(path: string): Promise<components["schemas"]["TerminalShellCheckResponse"]> {
	const { data, error } = await apiClient.GET("/api/v1/terminal-shells/check", { params: { query: { path } } });
	if (error || !data) throw error ?? new Error("Daemon returned no shell check");
	return data;
}
