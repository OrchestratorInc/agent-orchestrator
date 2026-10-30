import { Eye, PencilLine } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useCloudSessionLive } from "../../hooks/useCloudSessionLive";
import type { WorkspaceSession } from "../../types/workspace";

type TaggedSession = Pick<WorkspaceSession, "id" | "cloud">;

/**
 * On a session someone shared with this person: what they can do in it and,
 * when it came through a team, which one. "Watch · via Platform". Nothing on
 * the person's own sessions.
 */
export function SessionAccessTag({ session }: { session: TaggedSession }) {
	if (session.cloud?.shared !== true) return null;
	return <SharedSessionAccessTag session={session} />;
}

/** The role is the session's live one, so it is always what the control plane enforces. */
function SharedSessionAccessTag({ session }: { session: TaggedSession }) {
	const { t } = useTranslation();
	const live = useCloudSessionLive(session.cloud?.orgId, session.id, true);
	if (!live) return null;
	const role = live.viewers.find((viewer) => viewer.userId === live.you)?.role;
	if (role !== "viewer" && role !== "editor") return null;
	const via = session.cloud?.sharedVia;
	const level = role === "viewer" ? t("share.accessViewer") : t("share.accessEditor");
	const label = via ? t("teams.accessVia", { access: level, team: via }) : level;
	return (
		<span
			className="inline-flex min-w-0 max-w-full shrink-0 items-center gap-1 rounded-full bg-muted px-2 py-0.5 text-2xs font-medium text-muted-foreground"
			title={label}
			data-session-access-tag={role}
		>
			{role === "viewer" ? <Eye className="size-2.5 shrink-0" aria-hidden="true" /> : <PencilLine className="size-2.5 shrink-0" aria-hidden="true" />}
			<span className="min-w-0 truncate">{label}</span>
		</span>
	);
}
