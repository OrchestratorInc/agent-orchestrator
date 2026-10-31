import { Download } from "lucide-react";
import { useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import { useAgentReadinessQuery } from "../hooks/useAgentReadinessQuery";
import { agentLabel, type AgentId } from "../lib/agent-options";
import { useUiStore } from "../stores/ui-store";
import { closeSessionImportOffer, IMPORTABLE_AGENTS, sessionImportOfferClosed, subscribeSessionImportOffer } from "./settings/harness/SessionImportPanel";
import { TopbarButton } from "./TopbarButton";

const SIGNED_IN = new Set(["authorized", "not_applicable", "configured"]);

/** First-launch offer to bring earlier chats into AO, one button per agent that
 * is installed and signed in here. Each opens that agent's Import tab. It stays
 * until the user imports or dismisses it. */
export function SessionImportOffer() {
	const { t } = useTranslation();
	const closed = useSyncExternalStore(subscribeSessionImportOffer, sessionImportOfferClosed);
	const readiness = useAgentReadinessQuery(!closed);
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	const agents = (readiness.data?.agents ?? [])
		.filter((agent) => IMPORTABLE_AGENTS.has(agent.id as AgentId) && agent.installation.state === "installed" && SIGNED_IN.has(agent.authentication.state))
		.map((agent) => agent.id as AgentId);
	if (closed || agents.length === 0) return null;
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
						<div className="mt-2 flex flex-wrap items-center gap-2">
							{agents.map((agentId) => (
								<TopbarButton
									key={agentId}
									onClick={() => openGlobalSettings("harness", { focusAgentId: agentId, harnessTab: "import" })}
									variant="primary"
								>
									{t("sessionImport.offerAction", { agent: agentLabel(agentId) })}
								</TopbarButton>
							))}
							<TopbarButton onClick={closeSessionImportOffer} variant="accent">
								{t("sessionImport.offerDismiss")}
							</TopbarButton>
						</div>
					</div>
				</div>
			</div>
		</div>
	);
}
