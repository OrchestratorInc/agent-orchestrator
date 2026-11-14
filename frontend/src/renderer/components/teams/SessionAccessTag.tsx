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

export function SessionAccessTag({ session }: { session: TaggedSession }) {
	const teams = useTeamsEnabled();
	if (!teams || session.cloud?.shared !== true || session.isTerminated === true) return null;
	return <SharedSessionAccessTag session={session} />;
}

function SharedSessionAccessTag({ session }: { session: TaggedSession }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const { baseUrl } = useCloudCp();
	const live = useCloudSessionLive(session.cloud?.orgId, session.id, true);
	const liveRole = live?.viewers.find((viewer) => viewer.userId === live.you)?.role;
	const previousRole = useRef(liveRole);
	const [roleChangedAt, setRoleChangedAt] = useState(0);
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
