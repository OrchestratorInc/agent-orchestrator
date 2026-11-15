import type { QueryClient, QueryState } from "@tanstack/react-query";
import { remoteWorkspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { isUnpinnedHere, type RemoteUnpin, useRemoteUnpinStore } from "../stores/remote-unpin-store";
import { clientForHost, connectedHost } from "./host-clients";
import { sessionUiKey } from "./hosts";

const RETRY_MS = 60_000;
const lastAttempt = new Map<string, number>();
const inFlight = new Set<string>();

type RemoteSessionPin = { id: string; isPinned?: boolean; pinnedAt?: string | null };

function tellHost(queryClient: QueryClient, unpin: RemoteUnpin): void {
	const key = sessionUiKey(unpin.sessionId, unpin.hostId);
	if (inFlight.has(key) || !connectedHost(unpin.hostId)) return;
	const last = lastAttempt.get(key);
	if (last !== undefined && Date.now() - last < RETRY_MS) return;
	inFlight.add(key);
	lastAttempt.set(key, Date.now());
	void clientForHost(unpin.hostId)
		.DELETE("/api/v1/sessions/{sessionId}/pin", { params: { path: { sessionId: unpin.sessionId } } })
		.then(({ error, response }) => {
			if (error && response.status !== 404) throw error;
			return queryClient.invalidateQueries({ queryKey: remoteWorkspaceQueryKey(unpin.hostId) });
		})
		.catch((error: unknown) => console.warn("Remote unpin not confirmed yet; it is kept here and retried:", error))
		.finally(() => inFlight.delete(key));
}

export function unpinRemoteSession(queryClient: QueryClient, hostId: string, sessionId: string, pinnedAt?: string): void {
	useRemoteUnpinStore.getState().add(hostId, sessionId, pinnedAt);
	const unpin = useRemoteUnpinStore.getState().unpins[sessionUiKey(sessionId, hostId)];
	lastAttempt.delete(sessionUiKey(sessionId, hostId));
	if (unpin) tellHost(queryClient, unpin);
}

export function forgetRemoteUnpin(hostId: string, sessionId: string): void {
	useRemoteUnpinStore.getState().remove([sessionUiKey(sessionId, hostId)]);
}

function reconcile(queryClient: QueryClient, hostId: string, state: QueryState<RemoteSessionPin[] | undefined>): void {
	if (!state.data) return;
	const { unpins, remove } = useRemoteUnpinStore.getState();
	const settled: string[] = [];
	for (const [key, unpin] of Object.entries(unpins)) {
		if (unpin.hostId !== hostId || state.dataUpdatedAt < unpin.at) continue;
		const session = state.data.find((candidate) => candidate.id === unpin.sessionId);
		if (!session?.isPinned || !isUnpinnedHere(unpin, session.pinnedAt)) settled.push(key);
		else tellHost(queryClient, unpin);
	}
	remove(settled);
}

export function watchRemoteUnpins(queryClient: QueryClient): () => void {
	return queryClient.getQueryCache().subscribe((event) => {
		if (event.type !== "updated" || event.action.type !== "success") return;
		const [scope, hostId, list] = event.query.queryKey;
		if (scope === "remote-workspaces" && typeof hostId === "string" && list === "sessions") {
			reconcile(queryClient, hostId, event.query.state as QueryState<RemoteSessionPin[] | undefined>);
		}
	});
}
