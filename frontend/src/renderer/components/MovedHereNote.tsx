import { useTranslation } from "react-i18next";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { unsettledMoveHereKey, useMovedHereStore } from "../lib/move-finish";
import { useNavigateToSession } from "../lib/navigate-to-session";
import { cn } from "../lib/utils";
import type { WorkspaceSession } from "../types/workspace";
import { Button } from "./ui/button";

/**
 * Says an archived Cloud session moved to another computer, in place of
 * Restore. When it moved to this one and its session here still exists, it
 * says so and links to it.
 */
export function MovedHereNote({ session, className }: { session: Pick<WorkspaceSession, "id" | "cloud">; className?: string }) {
	const { t } = useTranslation();
	const navigateToSession = useNavigateToSession();
	const movedAway = Boolean(session.cloud?.movedAway);
	const here = useMovedHereStore((state) => (session.cloud ? state.moves[unsettledMoveHereKey(session.cloud.orgId, session.id)] : undefined));
	const local = useWorkspaceQuery({ includeCloud: false, enabled: movedAway && here !== undefined });
	if (!movedAway) return null;
	const continuation = here && (local.data ?? []).flatMap((workspace) => workspace.sessions).find((candidate) => candidate.id === here.sessionId);
	return (
		<div className={cn("flex flex-wrap items-center gap-x-3 gap-y-1", className)} data-testid="moved-away">
			<span className="text-xs text-muted-foreground">{continuation ? t("moveHere.done") : t("restoreUnavailable.movedAway")}</span>
			{continuation && here ? (
				<Button
					type="button"
					variant="secondary"
					size="sm"
					onClick={(event) => {
						// Inside an archived card, whose own click opens the Cloud copy.
						event.stopPropagation();
						navigateToSession(here.projectId, here.sessionId);
					}}
				>
					{t("move.open")}
				</Button>
			) : null}
		</div>
	);
}
