import { useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useAgentReadinessQuery } from "./useAgentReadinessQuery";
import { invalidateAgentModelCatalogs, type ModelCatalogAuthIssue } from "./useAgentModelsQuery";

const FOCUS_RECHECK_INTERVAL_MS = 5_000;

/**
 * Clears a model-catalog login error on its own once the login is fixed,
 * wherever that happened:
 *
 * - readiness reports the agent signed in again (AO's login terminal, the
 *   Harness settings page, or any other readiness check);
 * - the window regains focus while the error is showing, which is how a
 *   login (or Claude Code renewing its token) done in a terminal shows up.
 *
 * Both re-read the daemon's catalog, which rediscovers models once the
 * agent's credential changed.
 */
export function useModelCatalogAuthRecovery(agentId: string, hostId: string | undefined, issue: ModelCatalogAuthIssue | undefined) {
	const queryClient = useQueryClient();
	const readiness = useAgentReadinessQuery(agentId !== "", hostId);
	const authState = readiness.data?.agents.find((agent) => agent.id === agentId)?.authentication.state;
	const previous = useRef<{ agentId: string; authState: string | undefined }>({ agentId, authState });

	useEffect(() => {
		const before = previous.current;
		previous.current = { agentId, authState };
		if (before.agentId !== agentId || before.authState === authState) return;
		const signedIn = authState === "authorized" || authState === "configured";
		if (signedIn && before.authState !== "authorized") void invalidateAgentModelCatalogs(queryClient, agentId, hostId);
	}, [agentId, authState, hostId, queryClient]);

	useEffect(() => {
		if (!issue || agentId === "") return;
		let lastCheck = 0;
		const recheck = () => {
			const now = Date.now();
			if (now - lastCheck < FOCUS_RECHECK_INTERVAL_MS) return;
			lastCheck = now;
			void invalidateAgentModelCatalogs(queryClient, agentId, hostId);
		};
		window.addEventListener("focus", recheck);
		return () => window.removeEventListener("focus", recheck);
	}, [agentId, hostId, issue, queryClient]);
}
