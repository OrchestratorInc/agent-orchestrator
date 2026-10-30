import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../../hooks/useCloudCp";
import { useNavigateToSession } from "../../lib/navigate-to-session";
import { useUiStore } from "../../stores/ui-store";
import type { WorkspaceShareSource, WorkspaceSummary } from "../../types/workspace";

const STORAGE_PREFIX = "ao.cloud.seenShares.v1:";
const MAX_TOASTS = 3;
/** The sidebar's shared-projects query (useSharedCloudWorkspacesQuery), watched rather than run again. */
const SHARED_WORKSPACES_KEY = "cloud-shared-workspaces";

type ShareArrival = { key: string; workspace: WorkspaceSummary; sessionId?: string; source: WorkspaceShareSource };

/** Every share in the list, one per project or session, keyed by its grant. */
export function shareArrivals(workspaces: WorkspaceSummary[]): ShareArrival[] {
	const arrivals: ShareArrival[] = [];
	for (const workspace of workspaces) {
		const share = workspace.shared;
		if (!share) continue;
		if (share.wholeProject) arrivals.push({ key: `${workspace.id}:project:${share.wholeProject.grantId ?? ""}`, workspace, source: share.wholeProject });
		for (const [sessionId, source] of Object.entries(share.sessions ?? {})) {
			arrivals.push({ key: `${workspace.id}:${sessionId}:${source.grantId ?? ""}`, workspace, sessionId, source });
		}
	}
	return arrivals;
}

function readSeen(storageKey: string): Set<string> | null {
	try {
		const raw = window.localStorage.getItem(storageKey);
		return raw ? new Set(JSON.parse(raw) as string[]) : null;
	} catch {
		return null;
	}
}

function writeSeen(storageKey: string, keys: Iterable<string>) {
	try {
		window.localStorage.setItem(storageKey, JSON.stringify([...keys].slice(-2000)));
	} catch {
		// A full or blocked store only means a share may be announced again.
	}
}

/**
 * Says once when someone shares a session or project with this person while
 * AO is open, with a way to open it. The first list after signing in is
 * remembered silently, so nothing old is announced.
 */
export function SharedWithYouNotifier() {
	const { t } = useTranslation();
	const { baseUrl, userId, ready } = useCloudCp();
	const queryClient = useQueryClient();
	const navigateToSession = useNavigateToSession();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const navigateRef = useRef(navigateToSession);
	navigateRef.current = navigateToSession;

	useEffect(() => {
		if (!ready || !userId) return;
		const storageKey = `${STORAGE_PREFIX}${baseUrl}:${userId}`;
		const announce = (workspaces: WorkspaceSummary[]) => {
			const arrivals = shareArrivals(workspaces);
			const seen = readSeen(storageKey);
			if (seen === null) {
				writeSeen(storageKey, arrivals.map((arrival) => arrival.key));
				return;
			}
			const fresh = arrivals.filter((arrival) => !seen.has(arrival.key));
			if (fresh.length === 0) return;
			for (const arrival of fresh.slice(0, MAX_TOASTS)) {
				const session = arrival.sessionId ? arrival.workspace.sessions.find((candidate) => candidate.id === arrival.sessionId) : undefined;
				const target = session ?? arrival.workspace.sessions.find((candidate) => candidate.kind === "orchestrator") ?? arrival.workspace.sessions[0];
				const name = arrival.source.byName || t("teams.someone");
				const thing = session?.title || arrival.workspace.name;
				const title = arrival.source.viaTeamName
					? t("teams.sharedToastViaTeam", { name, thing, team: arrival.source.viaTeamName })
					: t("teams.sharedToastWithYou", { name, thing });
				showGlobalToast(title, undefined, {
					durationMs: 10_000,
					dedupeKey: `shared-${arrival.key}`,
					...(target ? { action: { label: t("teams.open"), run: () => navigateRef.current(arrival.workspace.id, target.id) } } : {}),
				});
			}
			writeSeen(storageKey, [...seen, ...fresh.map((arrival) => arrival.key)]);
		};
		const matches = (key: readonly unknown[]) => key[0] === SHARED_WORKSPACES_KEY && key[1] === baseUrl;
		const cache = queryClient.getQueryCache();
		for (const query of cache.findAll({ predicate: (query) => matches(query.queryKey) })) {
			const data = query.state.data as WorkspaceSummary[] | undefined;
			if (data) announce(data);
		}
		return cache.subscribe((event) => {
			if (event.type !== "updated" || event.action.type !== "success" || !matches(event.query.queryKey)) return;
			const data = event.query.state.data as WorkspaceSummary[] | undefined;
			if (data) announce(data);
		});
	}, [baseUrl, queryClient, ready, showGlobalToast, t, userId]);

	return null;
}
