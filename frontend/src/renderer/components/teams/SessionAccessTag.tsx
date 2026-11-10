import { useQueryClient } from "@tanstack/react-query";
import { Eye, PencilLine } from "@/components/app-icons";
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../../hooks/useCloudCp";
import { useCloudSessionLive } from "../../hooks/useCloudSessionLive";
import { useTeamsEnabled } from "../../hooks/useTeamsEnabled";
import { sharedCloudWorkspacesQueryKey } from "../../hooks/useWorkspaceQuery";
import type { WorkspaceSession } from "../../types/workspace";

type TaggedSession = Pick<WorkspaceSession, "id" | "cloud" | "isTerminated">;

/**
 * On a session someone shared with this person: what they can do in it and,
 * when it came through a team, which one. "Watch · via Platform". Nothing on
 * the person's own sessions, nor on one that has ended (it has no live state
 * to read the role from, like the other live subscribers).
 */
export function SessionAccessTag({ session }: { session: TaggedSession }) {
	const teams = useTeamsEnabled();
	if (!teams || session.cloud?.shared !== true || session.isTerminated === true) return null;
	return <SharedSessionAccessTag session={session} />;
}

/**
 * The role is the session's live one, so it is always what the control plane
 * enforces. The team comes from the shared list, which refreshes less often:
 * when the role changes (a direct share raised it above the team's, say), the
 * list is read again at once, and until it answers the tag names no team
 * rather than one that may no longer be the reason.
 */
function SharedSessionAccessTag({ session }: { session: TaggedSession }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const { baseUrl } = useCloudCp();
	const live = useCloudSessionLive(session.cloud?.orgId, session.id, true);
	const liveRole = live?.viewers.find((viewer) => viewer.userId === live.you)?.role;
	const previousRole = useRef(liveRole);
	const [roleChangedAt, setRoleChangedAt] = useState(0);
	// When the list was last read; re-renders on each read, even an unchanged one.
	const listedAt = useSyncExternalStore(
		useCallback((onChange: () => void) => queryClient.getQueryCache().subscribe(onChange), [queryClient]),
		() => queryClient.getQueryState([...sharedCloudWorkspacesQueryKey, baseUrl])?.dataUpdatedAt ?? 0,
	);
	useEffect(() => {
		const before = previousRole.current;
		previousRole.current = liveRole;
		if (before === undefined || liveRole === undefined || before === liveRole) return;
		setRoleChangedAt(Date.now());
		void queryClient.invalidateQueries({ queryKey: sharedCloudWorkspacesQueryKey });
	}, [liveRole, queryClient]);
	if (!live) return null;
	const role = liveRole;
	if (role !== "viewer" && role !== "editor") return null;
	const via = listedAt >= roleChangedAt ? session.cloud?.sharedVia : undefined;
	const level = role === "viewer" ? t("share.accessViewer") : t("share.accessEditor");
	const label = via ? t("teams.accessVia", { access: level, team: via }) : level;
	return (
		<span
			className="mx-1 inline-flex min-w-0 max-w-56 items-center gap-1 rounded-full bg-muted px-2 py-0.5 text-2xs font-medium text-muted-foreground"
			title={label}
			data-session-access-tag={role}
		>
			{role === "viewer" ? <Eye className="size-2.5 shrink-0" aria-hidden="true" /> : <PencilLine className="size-2.5 shrink-0" aria-hidden="true" />}
			<span className="min-w-0 truncate">{label}</span>
		</span>
	);
}
