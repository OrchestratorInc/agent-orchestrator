import type { QueryClient, QueryState } from "@tanstack/react-query";
import { remoteWorkspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { isUnpinnedHere, type RemoteUnpin, useRemoteUnpinStore } from "../stores/remote-unpin-store";
import { clientForHost, connectedHost } from "./host-clients";
import { sessionUiKey } from "./hosts";

// Unpinning a session on another host never waits for that host. The row leaves
// Pinned here at once (remote-unpin-store), and the host is told when it answers:
// now if it is connected, otherwise whenever its session list next loads.

/** A host that refused or failed is asked again at most this often. */
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
			// 404: the host no longer has the session, so there is no pin left to drop.
			if (error && response.status !== 404) throw error;
			// The fresh list settles the removal (reconcile below).
			return queryClient.invalidateQueries({ queryKey: remoteWorkspaceQueryKey(unpin.hostId) });
		})
		.catch((error: unknown) => console.warn("Remote unpin not confirmed yet; it is kept here and retried:", error))
		.finally(() => inFlight.delete(key));
}

/** Unpins a session that another host owns, without needing that host to answer. */
export function unpinRemoteSession(queryClient: QueryClient, hostId: string, sessionId: string, pinnedAt?: string): void {
	useRemoteUnpinStore.getState().add(hostId, sessionId, pinnedAt);
	const unpin = useRemoteUnpinStore.getState().unpins[sessionUiKey(sessionId, hostId)];
	lastAttempt.delete(sessionUiKey(sessionId, hostId));
	if (unpin) tellHost(queryClient, unpin);
}

/** Pinning again replaces an unpin that the host has not seen yet. */
export function forgetRemoteUnpin(hostId: string, sessionId: string): void {
	useRemoteUnpinStore.getState().remove([sessionUiKey(sessionId, hostId)]);
}

/**
 * Settles kept removals against a host's session list read after them: a
 * session the host no longer lists or no longer pins is done, as is one pinned
 * again later from elsewhere. One the host still pins is sent again.
 */
function reconcile(queryClient: QueryClient, hostId: string, state: QueryState<RemoteSessionPin[] | undefined>): void {
	if (!state.data) return;
	const { unpins, remove } = useRemoteUnpinStore.getState();
	const settled: string[] = [];
	for (const [key, unpin] of Object.entries(unpins)) {
		// A list read before the unpin (the cached copy shown while connecting) cannot settle it.
		if (unpin.hostId !== hostId || state.dataUpdatedAt < unpin.at) continue;
		const session = state.data.find((candidate) => candidate.id === unpin.sessionId);
		if (!session?.isPinned || !isUnpinnedHere(unpin, session.pinnedAt)) settled.push(key);
		else tellHost(queryClient, unpin);
	}
	remove(settled);
}

/** Watches remote session lists so kept removals reach their hosts. Returns the unsubscribe. */
export function watchRemoteUnpins(queryClient: QueryClient): () => void {
	return queryClient.getQueryCache().subscribe((event) => {
		if (event.type !== "updated" || event.action.type !== "success") return;
		const [scope, hostId, list] = event.query.queryKey;
		if (scope === "remote-workspaces" && typeof hostId === "string" && list === "sessions") {
			reconcile(queryClient, hostId, event.query.state as QueryState<RemoteSessionPin[] | undefined>);
		}
	});
}
