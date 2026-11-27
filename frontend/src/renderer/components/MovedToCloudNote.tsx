import { useTranslation } from "react-i18next";
import { useCloudSessionsQuery } from "../hooks/useWorkspaceQuery";
import { useNavigateToSession } from "../lib/navigate-to-session";
import { cn } from "../lib/utils";
import type { WorkspaceSession } from "../types/workspace";
import { Button } from "./ui/button";

/**
 * Says an archived session moved to Orchestrator Cloud, in place of Restore,
 * with a link to its Cloud session while this account can see it.
 */
export function MovedToCloudNote({ session, className }: { session: Pick<WorkspaceSession, "movedTo">; className?: string }) {
	const { t } = useTranslation();
	const navigateToSession = useNavigateToSession();
	const moved = session.movedTo;
	const cloudSessions = useCloudSessionsQuery({ enabled: Boolean(moved) });
	if (!moved) return null;
	const cloudSession = (cloudSessions.data ?? []).find((candidate) => candidate.id === moved.sessionId && candidate.orgId === moved.orgId);
	return (
		<div className={cn("flex flex-wrap items-center gap-x-3 gap-y-1", className)} data-testid="moved-to-cloud">
			<span className="text-xs text-muted-foreground">{t("move.movedToCloudHelp")}</span>
			{cloudSession ? (
				<Button
					type="button"
					variant="secondary"
					size="sm"
					onClick={(event) => {
						// Inside an archived card, whose own click opens the session here.
						event.stopPropagation();
						navigateToSession(cloudSession.projectId, cloudSession.id);
					}}
				>
					{t("move.openInCloud")}
				</Button>
			) : null}
		</div>
	);
}
