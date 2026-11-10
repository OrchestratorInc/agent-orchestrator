import { Download } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import { useAgentReadinessQuery } from "../hooks/useAgentReadinessQuery";
import { agentLabel, type AgentId } from "../lib/agent-options";
import { cn } from "../lib/utils";
import { useSessionImportStore } from "../stores/session-import-store";
import { useUiStore } from "../stores/ui-store";
import { AgentAvatar } from "./AgentAvatar";
import { useImportOfferShown } from "../lib/session-import";
import { IMPORTABLE_AGENTS } from "../lib/session-import-agents";
import { Notice, NOTICE_MARK_HALO, NoticeActions, NoticeAdornment, NoticeDescription, NoticeDismiss, NoticeDismissAction, NoticeHeader, NoticeIcon, NoticeSlot, NoticeTitle } from "./Notice";
import { Button } from "./ui/button";

/** Logos shown before the rest collapse into "+N", as many as the agent
 * update stack shows. */
const MAX_LOGOS = 3;

/** Rendered by Home while there are no projects. Shows as a notice in the
 * corner, below any agent updates, until the user dismisses it or imports. */
export function SessionImportOffer() {
	const { t } = useTranslation();
	const closed = useUiStore((state) => state.sessionImportOfferClosed);
	const closeSessionImportOffer = useUiStore((state) => state.closeSessionImportOffer);
	const openFolderImport = useSessionImportStore((state) => state.openFolderImport);
	const readiness = useAgentReadinessQuery(!closed);
	const agents = (readiness.data?.agents ?? [])
		.filter((agent) => IMPORTABLE_AGENTS.has(agent.id as AgentId) && agent.installation.state === "installed" && agent.authentication.state !== "unauthorized")
		.map((agent) => agent.id as AgentId);
	useImportOfferShown("home", !closed && agents.length > 0);
	if (closed || agents.length === 0) return null;
	const hidden = agents.length - MAX_LOGOS;
	const title = t("sessionImport.offerTitle");
	return (
		<NoticeSlot id="session-import" label={title} order={1}>
			<Notice label={title} onDismiss={closeSessionImportOffer}>
				<NoticeHeader>
					<NoticeIcon>
						<span className="grid size-icon-xl place-items-center text-muted-foreground">
							<Download className="size-icon-md" aria-hidden="true" />
						</span>
					</NoticeIcon>
					<NoticeTitle>{title}</NoticeTitle>
					<NoticeAdornment aria-label={agents.map(agentLabel).join(", ")} className="gap-1" role="img">
						<span className="flex items-center -space-x-1">
							{agents.slice(0, MAX_LOGOS).map((agentId) => (
								<AgentAvatar className={cn("relative size-icon-base", NOTICE_MARK_HALO)} decorative key={agentId} provider={agentId} />
							))}
						</span>
						{hidden > 0 ? <span className="tabular-nums">{t("sessionImport.offerMore", { count: hidden })}</span> : null}
					</NoticeAdornment>
					<NoticeDismiss label={t("sessionImport.offerClose")} />
				</NoticeHeader>
				<NoticeDescription>{t("sessionImport.offerDescription")}</NoticeDescription>
				<NoticeActions>
					<NoticeDismissAction>{t("sessionImport.offerDismiss")}</NoticeDismissAction>
					<span className="flex-1" />
					<Button className="min-w-20" onClick={openFolderImport} size="sm" type="button">
						{t("sessionImport.offerChooseFolders")}
					</Button>
				</NoticeActions>
			</Notice>
		</NoticeSlot>
	);
}
