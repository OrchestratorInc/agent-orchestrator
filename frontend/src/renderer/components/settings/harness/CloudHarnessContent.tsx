import { useMutation, useQueryClient } from "@tanstack/react-query";
import { KeyRound } from "@/components/app-icons";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../../../hooks/useCloudCp";
import { describeCloudHarness, useCloudHarnessVersions } from "../../../hooks/useCloudHarnessVersions";
import { versionLabel } from "../../../hooks/useHarnessUpdates";
import { useCloudOffering } from "../../../hooks/useCloudOffering";
import { useCloudOrg } from "../../../hooks/useCloudOrg";
import { providerConnectionsQueryKey, useProviderConnections } from "../../../hooks/useProviderConnections";
import { CloudCpError, type CloudCpProviderConnection } from "../../../lib/cloud-cp";
import { agentLabel, type AgentId } from "../../../lib/agent-options";
import { CLOUD_AGENT_PROVIDERS, isCloudHarnessConnected } from "../../../lib/cloud-agents";
import { cn } from "../../../lib/utils";
import { AgentAvatar } from "../../AgentAvatar";
import { Button } from "../../ui/button";
import { Pressable } from "../../ui/pressable";
import { CloudHarnessLoginPanel, type CloudHarness } from "../CloudHarnessLoginPanel";
import { SettingsRow } from "../SettingsRow";
import { CloudAgentAccountsPanel } from "./CloudAgentAccountsPanel";
import { FOCUS_HIGHLIGHT_MS } from "./harness-ui";

export function CloudHarnessContent({ focusAgentId, search }: { focusAgentId?: string; search: string }) {
	const { t } = useTranslation();
	const { org } = useCloudOrg();
	const connections = useProviderConnections();
	const versions = useCloudHarnessVersions();
	// Each harness can hold several accounts: its options list them instead of one login.
	const { agentAccounts } = useCloudOffering();
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
					const cloudVersion = describeCloudHarness(versions.data, agentId)?.currentVersion;
					const expanded = expandedAgentId === agentId;
					const openLogin = () => {
						// With accounts the list logs in: it opens on adding one while there is none.
						if (!agentAccounts) setLoginAgent(agentId);
						setExpandedAgentId(agentId);
					};
					return <div
						aria-labelledby={`harness-agent-${agentId}`}
						className={cn("settings-row-bar min-h-14 flex-wrap gap-3 transition-[background-color,box-shadow] duration-200", highlightedAgentId === agentId && "bg-accent-weak ring-2 ring-inset ring-accent")}
						data-agent={agentId}
						data-focus-highlighted={highlightedAgentId === agentId ? "" : undefined}
						key={agentId}
						tabIndex={-1}
					>
						<Pressable className="flex min-w-0 flex-1 items-center gap-3 rounded-md" aria-label={t(expanded ? "settings.harness.collapseOptions" : "settings.harness.expandOptions", { agent: agentLabel(agentId) })} aria-expanded={expanded} aria-controls={`cloud-harness-options-${agentId}`} onClick={() => setExpandedAgentId(expanded ? null : agentId)}>
							<AgentAvatar className="size-7 shrink-0" decorative provider={agentId} />
							<span className="min-w-0 flex-1">
								<span className="block text-sm font-medium text-settings-label" id={`harness-agent-${agentId}`}>{agentLabel(agentId)}</span>
								<span className="mt-0.5 flex flex-wrap gap-x-1.5 text-xs text-settings-muted"><span>{connected ? t("settings.harness.loggedIn") : t("settings.harness.cloudNotConnected")}</span><span aria-hidden="true">·</span><span>{cloudVersion ? versionLabel(cloudVersion) : t("settings.harness.apiVersionNotReported")}</span></span>
							</span>
						</Pressable>
						{!connected && loginAgent !== agentId ? <Button type="button" data-harness-primary-action="" size="default" className="min-w-20" onClick={openLogin}>{t("settings.harness.login")}</Button> : null}
						<div id={`cloud-harness-options-${agentId}`} hidden={!expanded} className={cn("basis-full pl-10", !expanded && "hidden")}>
							{agentAccounts ? (expanded ? <CloudAgentAccountsPanel agent={agentId} addFirst={!connected} /> : null) : loginAgent === agentId ? <CloudHarnessLoginPanel agent={agentId} onClose={() => setLoginAgent(null)} /> : connected ? <Button type="button" size="default" variant="secondary" onClick={openLogin}>{t("settings.harness.refreshLogin")}</Button> : null}
						</div>
					</div>;
				})}
				{rows.length === 0 ? <p className="px-3 py-6 text-center text-sm text-settings-muted">{t("settings.harness.noResults")}</p> : null}
			</div>}
		{org?.id && hasSavedGitHubToken(connections.data) ? <div className="mt-3 border-t border-border px-3 pt-3"><SavedGitHubTokenRow /></div> : null}
	</>;
}

/** A GitHub token saved for Cloud before it reached GitHub through the GitHub App, whatever its last check said. */
function hasSavedGitHubToken(connections: CloudCpProviderConnection[] | undefined): boolean {
	return (connections ?? []).some((connection) => connection.provider === "github" && connection.label === "default");
}

/** The control plane has no saved GitHub token for this person (DELETE /me/github-pat answered 404 not_found). */
function isTokenAlreadyGone(error: unknown): boolean {
	return error instanceof CloudCpError && error.status === 404 && error.code === "not_found";
}

/**
 * Nothing saves a GitHub token for Cloud any more, but the control plane keeps
 * one saved earlier and can still use it, so its owner can remove it here. The
 * row leaves once the token is gone.
 */
function SavedGitHubTokenRow() {
	const { t } = useTranslation();
	const { client } = useCloudCp();
	const queryClient = useQueryClient();
	const remove = useMutation({
		mutationFn: async () => {
			try {
				await client.deleteGitHubPAT();
			} catch (error) {
				// Already gone (removed from another device) is what Remove asked for.
				if (!isTokenAlreadyGone(error)) throw error;
			}
		},
		// Refetch either way, so the row matches what the control plane holds.
		onSettled: () => queryClient.invalidateQueries({ queryKey: providerConnectionsQueryKey }),
	});
	return <SettingsRow
		data-saved-github-token=""
		description={remove.isError
			? <span className="text-error" role="alert">{t("settings.cloudAgents.savedGitHubToken.errorRemove")}</span>
			: t("settings.cloudAgents.savedGitHubToken.hint")}
		icon={KeyRound}
		label={t("settings.cloudAgents.savedGitHubToken.title")}
	>
		<Button disabled={remove.isPending} onClick={() => remove.mutate()} type="button" variant="destructive-ghost">
			{remove.isPending ? t("settings.cloudAgents.savedGitHubToken.removing") : t("settings.cloudAgents.savedGitHubToken.remove")}
		</Button>
	</SettingsRow>;
}
