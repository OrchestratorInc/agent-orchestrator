import { ChevronDown, Download } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useAgentReadinessQuery } from "../hooks/useAgentReadinessQuery";
import { agentLabel, type AgentId } from "../lib/agent-options";
import { agentSignedIn } from "../lib/agent-select-options";
import { useUiStore } from "../stores/ui-store";
import { AgentAvatar } from "./AgentAvatar";
import { IMPORTABLE_AGENTS } from "../lib/session-import-agents";
import { Button } from "./ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "./ui/dropdown-menu";

/** Logos shown before the rest collapse into "+N", so the card keeps one row
 * however many agents can import. */
const MAX_LOGOS = 4;

/** First-launch offer to bring earlier chats into AO from the agents that are
 * installed and signed in here. One agent gets a direct button; several share a
 * single "Import chats" menu, so the card stays one row as agents are added.
 * Each choice opens that agent's Import tab. It stays until the user imports or
 * dismisses it. */
export function SessionImportOffer() {
	const { t } = useTranslation();
	const closed = useUiStore((state) => state.sessionImportOfferClosed);
	const closeSessionImportOffer = useUiStore((state) => state.closeSessionImportOffer);
	const readiness = useAgentReadinessQuery(!closed);
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	const agents = (readiness.data?.agents ?? [])
		.filter((agent) => IMPORTABLE_AGENTS.has(agent.id as AgentId) && agent.installation.state === "installed" && agentSignedIn(agent))
		.map((agent) => agent.id as AgentId);
	if (closed || agents.length === 0) return null;
	const openImport = (agentId: AgentId) => openGlobalSettings("harness", { focusAgentId: agentId, harnessTab: "import" });
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
							{agents.length === 1 ? (
								<Button onClick={() => openImport(agents[0])} size="sm" variant="primary">
									{t("sessionImport.offerAction", { agent: agentLabel(agents[0]) })}
								</Button>
							) : (
								<DropdownMenu>
									<DropdownMenuTrigger asChild>
										<Button size="sm" variant="primary">
											{t("sessionImport.offerChoose")}
											<ChevronDown className="size-3.5" aria-hidden="true" />
										</Button>
									</DropdownMenuTrigger>
									<DropdownMenuContent align="start" className="max-h-80 min-w-48 overflow-y-auto">
										{agents.map((agentId) => (
											<DropdownMenuItem key={agentId} onSelect={() => openImport(agentId)}>
												<AgentAvatar className="size-4" decorative provider={agentId} />
												{agentLabel(agentId)}
											</DropdownMenuItem>
										))}
									</DropdownMenuContent>
								</DropdownMenu>
							)}
							<Button onClick={() => closeSessionImportOffer()} size="sm" variant="quiet">
								{t("sessionImport.offerDismiss")}
							</Button>
							{agents.length > 1 ? (
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
							) : null}
						</div>
					</div>
				</div>
			</div>
		</div>
	);
}
