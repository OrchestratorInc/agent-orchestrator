import { Download } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import { useAgentReadinessQuery } from "../hooks/useAgentReadinessQuery";
import { agentLabel, type AgentId } from "../lib/agent-options";
import { useSessionImportStore } from "../stores/session-import-store";
import { useUiStore } from "../stores/ui-store";
import { AgentAvatar } from "./AgentAvatar";
import { useImportOfferShown } from "../lib/session-import";
import { IMPORTABLE_AGENTS } from "../lib/session-import-agents";
import { Button } from "./ui/button";

/** Logos shown before the rest collapse into "+N", so the card keeps one row
 * however many agents can import. */
const MAX_LOGOS = 4;

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
	return (
		<div className="flex w-full justify-center px-3">
			<div className="w-full max-w-[620px] rounded-welcome-panel border border-[var(--color-border-import-modal)] bg-[var(--color-bg-import-card)] px-4 py-4">
				<div className="flex items-start gap-3">
					<span className="grid size-9 shrink-0 place-items-center rounded-lg bg-[var(--color-bg-import-chip)] text-[var(--color-text-import-muted)]">
						<Download className="size-4" aria-hidden="true" />
					</span>
					<div className="min-w-0 flex-1">
						<p className="text-[14px] font-semibold text-[var(--color-text-import-title)]">{t("sessionImport.offerTitle")}</p>
						<p className="mt-0.5 text-[12px] leading-5 text-[var(--color-text-import-muted)]">{t("sessionImport.offerDescription")}</p>
						<div className="mt-3 flex flex-wrap items-center gap-2">
							<Button onClick={openFolderImport} size="sm" variant="primary">
								{t("sessionImport.offerChooseFolders")}
							</Button>
							<Button onClick={() => closeSessionImportOffer()} size="sm" variant="quiet">
								{t("sessionImport.offerDismiss")}
							</Button>
							<span
								aria-label={agents.map(agentLabel).join(", ")}
								className="ml-auto flex items-center gap-1 text-[12px] text-[var(--color-text-import-muted)]"
								role="img"
							>
								{agents.slice(0, MAX_LOGOS).map((agentId) => (
									<AgentAvatar className="size-5" decorative key={agentId} provider={agentId} />
								))}
								{hidden > 0 ? <span className="pl-0.5 tabular-nums">{t("sessionImport.offerMore", { count: hidden })}</span> : null}
							</span>
						</div>
					</div>
				</div>
			</div>
		</div>
	);
}
