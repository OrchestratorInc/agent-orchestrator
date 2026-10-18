import { useQueryClient } from "@tanstack/react-query";
import { KeyRound } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../../../hooks/useCloudCp";
import { useCloudOrg } from "../../../hooks/useCloudOrg";
import { providerConnectionsQueryKey, useProviderConnections } from "../../../hooks/useProviderConnections";
import { agentLabel, type AgentId } from "../../../lib/agent-options";
import { CLOUD_AGENT_PROVIDERS, isCloudHarnessConnected } from "../../../lib/cloud-agents";
import { cn } from "../../../lib/utils";
import { AgentAvatar } from "../../AgentAvatar";
import { GitHubTokenField } from "../../onboarding/GitHubTokenField";
import { Button } from "../../ui/button";
import { CloudHarnessLoginPanel, type CloudHarness } from "../CloudHarnessLoginPanel";
import { SettingsRow } from "../SettingsRow";
import { FOCUS_HIGHLIGHT_MS } from "./harness-ui";

export function CloudHarnessContent({ focusAgentId, search }: { focusAgentId?: string; search: string }) {
	const { t } = useTranslation();
	const { org } = useCloudOrg();
	const connections = useProviderConnections();
	const [loginAgent, setLoginAgent] = useState<CloudHarness | null>(null);
	const [expandedAgentId, setExpandedAgentId] = useState<CloudHarness | null>(null);
	const [highlightedAgentId, setHighlightedAgentId] = useState<AgentId | null>(null);
	const rowsRef = useRef<HTMLDivElement>(null);
	const focusHandledRef = useRef(false);
	const targetAgentId = CLOUD_AGENT_PROVIDERS.find((agentId) => agentId === focusAgentId);
	const rows = CLOUD_AGENT_PROVIDERS.filter((agentId) =>
		agentId === targetAgentId || agentId === loginAgent || agentLabel(agentId).toLowerCase().includes(search.trim().toLowerCase()),
	);
	useEffect(() => {
		if (focusHandledRef.current || !targetAgentId || !org?.id || connections.isPending) return;
		const row = rowsRef.current?.querySelector<HTMLElement>(`[data-agent="${targetAgentId}"]`);
		if (!row) return;
		focusHandledRef.current = true;
		row.scrollIntoView({ behavior: "smooth", block: "center" });
		(row.querySelector<HTMLElement>("[data-harness-primary-action]:not(:disabled)") ?? row).focus({ preventScroll: true });
		setHighlightedAgentId(targetAgentId);
		const timer = window.setTimeout(() => setHighlightedAgentId(null), FOCUS_HIGHLIGHT_MS);
		return () => window.clearTimeout(timer);
	}, [connections.isPending, org?.id, targetAgentId]);

	return <>
		{!org?.id ? <p className="px-3 py-6 text-center text-sm text-settings-muted">{t("settings.cloudAgents.signIn")}</p>
			: connections.error ? <p className="px-3 py-6 text-sm text-error" role="alert">{String(connections.error)}</p>
			: connections.isPending ? null
			: <div className="settings-grouped-rows flex w-full flex-col" ref={rowsRef}>
				{rows.map((agentId) => {
					const connected = isCloudHarnessConnected(connections.data, agentId);
					const expanded = expandedAgentId === agentId;
					const openLogin = () => { setLoginAgent(agentId); setExpandedAgentId(agentId); };
					return <div
						aria-labelledby={`harness-agent-${agentId}`}
						className={cn("settings-row-bar min-h-14 flex-wrap gap-3 transition-[background-color,box-shadow] duration-200", highlightedAgentId === agentId && "bg-accent-weak ring-2 ring-inset ring-accent")}
						data-agent={agentId}
						data-focus-highlighted={highlightedAgentId === agentId ? "" : undefined}
						key={agentId}
						tabIndex={-1}
					>
						<button type="button" className="flex min-w-0 flex-1 items-center gap-3 rounded-md text-left outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-label={t(expanded ? "settings.harness.collapseOptions" : "settings.harness.expandOptions", { agent: agentLabel(agentId) })} aria-expanded={expanded} aria-controls={`cloud-harness-options-${agentId}`} onClick={() => setExpandedAgentId(expanded ? null : agentId)}>
							<AgentAvatar className="size-7 shrink-0" decorative provider={agentId} />
							<span className="min-w-0 flex-1">
								<span className="block text-sm font-medium text-settings-label" id={`harness-agent-${agentId}`}>{agentLabel(agentId)}</span>
								<span className="mt-0.5 flex flex-wrap gap-x-1.5 text-xs text-settings-muted"><span>{connected ? t("settings.harness.loggedIn") : t("settings.harness.cloudNotConnected")}</span><span aria-hidden="true">·</span><span>{t("settings.harness.apiVersionNotReported")}</span></span>
							</span>
						</button>
						{!connected && loginAgent !== agentId ? <Button type="button" data-harness-primary-action="" size="sm" className="h-8 min-w-20 px-3 focus-visible:ring-2 focus-visible:ring-ring" onClick={openLogin}>{t("settings.harness.login")}</Button> : null}
						<div id={`cloud-harness-options-${agentId}`} hidden={!expanded} className={cn("basis-full pl-10", !expanded && "hidden")}>
							{loginAgent === agentId ? <CloudHarnessLoginPanel agent={agentId} onClose={() => setLoginAgent(null)} /> : connected ? <Button type="button" size="sm" variant="outline" className="h-8 px-3" onClick={openLogin}>{t("settings.harness.refreshLogin")}</Button> : null}
						</div>
					</div>;
				})}
				{rows.length === 0 ? <p className="px-3 py-6 text-center text-sm text-settings-muted">{t("settings.harness.noResults")}</p> : null}
			</div>}
		{org?.id ? <div className="mt-3 border-t border-border px-3 pt-3"><CloudGitHubPatRow /></div> : null}
	</>;
}

/**
	* GitHub personal access token for cloud workers to clone private repositories.
	* Lives on the Harness page's cloud view (the only place cloud credentials are
	* managed) so GitHub connectivity stays reachable once a user is signed into a
	* cloud org. The PAT is personal: stored encrypted and never echoed back.
	*/
function CloudGitHubPatRow() {
	const { t } = useTranslation();
	const { client } = useCloudCp();
	const queryClient = useQueryClient();
	const userConnections = useProviderConnections();
	const [githubPAT, setGitHubPAT] = useState("");
	const [githubPATBusy, setGitHubPATBusy] = useState(false);
	const [githubPATError, setGitHubPATError] = useState<string | null>(null);

	const githubPATConnected = (userConnections.data ?? []).some(
		(connection) => connection.provider === "github" && connection.label === "default" && connection.validationState === "valid",
	);
	const saveGitHubPAT = async () => {
		if (githubPAT.trim() === "") return;
		setGitHubPATBusy(true);
		setGitHubPATError(null);
		try {
			await client.putGitHubPAT({ secret: githubPAT.trim() });
			setGitHubPAT("");
			await queryClient.invalidateQueries({ queryKey: providerConnectionsQueryKey });
		} catch (error) {
			setGitHubPATError(error instanceof Error ? error.message : t("settings.cloudAgents.github.errorSave"));
		} finally {
			setGitHubPATBusy(false);
		}
	};
	const removeGitHubPAT = async () => {
		setGitHubPATBusy(true);
		setGitHubPATError(null);
		try {
			await client.deleteGitHubPAT();
			await queryClient.invalidateQueries({ queryKey: providerConnectionsQueryKey });
		} catch (error) {
			setGitHubPATError(error instanceof Error ? error.message : t("settings.cloudAgents.github.errorRemove"));
		} finally {
			setGitHubPATBusy(false);
		}
	};
	return (
		<div className="flex w-full flex-col gap-1.5">
			<SettingsRow key="github-pat" icon={KeyRound} label={t("settings.cloudAgents.github.title")}>
				<span className="text-sm leading-5 text-settings-muted">{githubPATConnected ? t("settings.cloudAgents.github.connected") : t("settings.cloudAgents.github.notConnected")}</span>
			</SettingsRow>
			<GitHubTokenField
				id="settings-github-pat"
				bare
				className="mt-2"
				label={t("settings.cloudAgents.github.tokenLabel")}
				hint={t("settings.cloudAgents.github.tokenHint")}
				value={githubPAT}
				disabled={githubPATBusy}
				error={githubPATError}
				submitLabel={githubPATBusy ? t("settings.cloudAgents.github.saving") : t("settings.cloudAgents.github.save")}
				submitVariant="outline"
				submitDisabled={githubPATBusy}
				onChange={setGitHubPAT}
				onSubmit={() => void saveGitHubPAT()}
			/>
			{githubPATConnected ? (
				<div className="mt-2 flex justify-end">
					<Button type="button" variant="footer" disabled={githubPATBusy} onClick={() => void removeGitHubPAT()}>
						{t("settings.cloudAgents.github.remove")}
					</Button>
				</div>
			) : null}
		</div>
	);
}
