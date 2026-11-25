import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, KeyRound } from "@/components/app-icons";
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../../ui/tabs";
import { CloudHarnessLoginPanel, type CloudHarness } from "../CloudHarnessLoginPanel";
import { SettingsRow } from "../SettingsRow";
import { CloudAgentAccountsPanel } from "./CloudAgentAccountsPanel";
import { CloudHarnessModelsPanel } from "./CloudHarnessModelsPanel";
import { FOCUS_HIGHLIGHT_MS, HARNESS_TAB_CLASS, HARNESS_TABS_CLASS, HarnessDetailGroup, HarnessDetailRow, HarnessStateText, useWideLayout } from "./harness-ui";

type CloudDetailTab = "account" | "models";

export function CloudHarnessContent({ focusAgentId, search, onDetailChange }: { focusAgentId?: string; search: string; onDetailChange?: (open: boolean) => void }) {
	const { t } = useTranslation();
	const { org } = useCloudOrg();
	const connections = useProviderConnections();
	if (!org?.id) return <p className="px-3 py-6 text-center text-sm text-settings-muted">{t("settings.cloudAgents.signIn")}</p>;
	if (connections.error) return <p className="px-3 py-6 text-sm text-error" role="alert">{String(connections.error)}</p>;
	if (connections.isPending) return null;
	return <>
		{/* Mounted once connections are known, so the layout measures its width on mount. */}
		<CloudHarnessPages connections={connections.data} focusAgentId={focusAgentId} onDetailChange={onDetailChange} orgId={org.id} search={search} />
		{hasSavedGitHubToken(connections.data) ? <div className="mt-3 shrink-0 border-t border-border px-3 pt-3"><SavedGitHubTokenRow /></div> : null}
	</>;
}

function CloudHarnessPages({ connections, focusAgentId, onDetailChange, orgId, search }: {
	connections: CloudCpProviderConnection[] | undefined;
	focusAgentId?: string;
	onDetailChange?: (open: boolean) => void;
	orgId: string;
	search: string;
}) {
	const { t } = useTranslation();
	const versions = useCloudHarnessVersions();
	// Each harness can hold several accounts: its Account tab lists them instead of one login.
	const { agentAccounts } = useCloudOffering();
	const [loginAgent, setLoginAgent] = useState<CloudHarness | null>(null);
	const [highlightedAgentId, setHighlightedAgentId] = useState<AgentId | null>(null);
	// Like the local list: one harness open at a time, its page has Account and Models tabs.
	const [selectedAgentId, setSelectedAgentId] = useState<CloudHarness | null>(null);
	const [detailTab, setDetailTab] = useState<CloudDetailTab>("account");
	const detailRef = useRef<HTMLDivElement>(null);
	const focusHandledRef = useRef(false);
	const focusPrimaryActionRef = useRef(false);
	const targetAgentId = CLOUD_AGENT_PROVIDERS.find((agentId) => agentId === focusAgentId);
	const rows = CLOUD_AGENT_PROVIDERS.filter((agentId) =>
		agentId === targetAgentId || agentLabel(agentId).toLowerCase().includes(search.trim().toLowerCase()),
	);

	const openDetail = (agentId: CloudHarness, tab: CloudDetailTab = "account") => {
		if (loginAgent !== agentId) setLoginAgent(null);
		setSelectedAgentId(agentId);
		setDetailTab(tab);
	};
	const closeDetail = () => {
		setLoginAgent(null);
		setSelectedAgentId(null);
	};

	// Wide layouts keep the list beside the open harness; narrow ones swap between them.
	const layoutRef = useRef<HTMLDivElement>(null);
	const split = useWideLayout(layoutRef);
	useEffect(() => {
		if (!split || selectedAgentId !== null || rows.length === 0) return;
		openDetail(rows[0]);
	});
	useEffect(() => {
		onDetailChange?.(selectedAgentId !== null && !split);
	}, [onDetailChange, selectedAgentId, split]);
	useEffect(() => () => onDetailChange?.(false), [onDetailChange]);

	useEffect(() => {
		if (focusHandledRef.current || !targetAgentId) return;
		focusHandledRef.current = true;
		focusPrimaryActionRef.current = true;
		openDetail(targetAgentId);
		setHighlightedAgentId(targetAgentId);
		const timer = window.setTimeout(() => setHighlightedAgentId(null), FOCUS_HIGHLIGHT_MS);
		return () => window.clearTimeout(timer);
	}, [targetAgentId]);
	useEffect(() => {
		if (!focusPrimaryActionRef.current || !selectedAgentId) return;
		const detail = detailRef.current;
		if (!detail) return;
		focusPrimaryActionRef.current = false;
		// No scrollIntoView: the open page fills the dialog, as on the local side.
		const primaryAction = detail.querySelector<HTMLElement>("[data-harness-primary-action]:not(:disabled)");
		(primaryAction ?? detail.querySelector<HTMLElement>("[role=tab][data-state=active]"))?.focus({ preventScroll: true });
	}, [selectedAgentId]);

	const selected = selectedAgentId;
	const selectedConnected = selected ? isCloudHarnessConnected(connections, selected) : false;
	const selectedVersion = selected ? describeCloudHarness(versions.data, selected)?.currentVersion : undefined;
	const accountTab = (agentId: CloudHarness) => {
		// With accounts the list logs in: it opens on adding one while there is none.
		if (agentAccounts) return <CloudAgentAccountsPanel key={agentId} agent={agentId} addFirst={!selectedConnected} />;
		return <HarnessDetailGroup title={t("settings.harness.account")}>
			<HarnessDetailRow
				label={<HarnessStateText tone={selectedConnected ? "ok" : "off"}>{selectedConnected ? t("settings.harness.loggedIn") : t("settings.harness.cloudNotConnected")}</HarnessStateText>}
				description={selectedVersion ? versionLabel(selectedVersion) : t("settings.harness.apiVersionNotReported")}
				secondary={loginAgent === agentId ? <div className="basis-full pt-1"><CloudHarnessLoginPanel agent={agentId} onClose={() => setLoginAgent(null)} /></div> : undefined}
			>
				{loginAgent === agentId ? null : <Button type="button" data-harness-primary-action="" size="default" variant={selectedConnected ? "secondary" : "default"} className="min-w-20" onClick={() => setLoginAgent(agentId)}>
					{selectedConnected ? t("settings.harness.refreshLogin") : t("settings.harness.login")}
				</Button>}
			</HarnessDetailRow>
		</HarnessDetailGroup>;
	};

	return (
		<div ref={layoutRef} data-harness-detail="" className={cn("relative flex w-full min-w-0 max-w-full min-h-0 flex-1 flex-col overflow-hidden", split && "grid-rows-[minmax(0,1fr)]", split && "grid grid-cols-[minmax(14rem,16rem)_minmax(0,1fr)] items-stretch overflow-clip rounded-xl border border-border bg-(--color-bg-primary)")}>
			{split || !selected ? (
				<div className={cn("flex w-full flex-col", split
					? "relative h-full min-h-0 max-h-full harness-list-scrollbar divide-y divide-border overflow-y-auto overscroll-contain bg-(--color-bg-secondary)"
					: "relative min-h-0 flex-1 harness-list-scrollbar divide-y divide-border overflow-y-auto overscroll-contain rounded-lg border border-border")}>
					{rows.map((agentId) => {
						const connected = isCloudHarnessConnected(connections, agentId);
						const current = split && selected === agentId;
						const statusId = `cloud-harness-list-status-${agentId}`;
						return <Pressable key={agentId}
							aria-label={t("settings.harness.openDetails", { agent: agentLabel(agentId) })}
							// The receding avatar is visual only; its words reach screen readers here.
							aria-describedby={statusId}
							aria-current={current ? "true" : undefined}
							className={cn(
								"flex w-full items-center gap-3",
								split ? "min-h-11 shrink-0 px-3" : "min-h-12 shrink-0 px-3",
								current
									? (split ? "bg-(--color-bg-tertiary) font-medium" : "bg-accent font-medium")
									: "hover:bg-(--color-interactive-hover)",
							)}
							data-agent={agentId}
							onClick={() => openDetail(agentId)}
						>
							<span id={statusId} className="sr-only">{connected ? t("settings.harness.loggedIn") : t("settings.harness.cloudNotConnected")}</span>
							{/* Harnesses with no Cloud login stay in place but recede, like uninstalled local ones. */}
							<AgentAvatar className={cn("shrink-0", split ? "size-5" : "size-6", !connected && "opacity-45 grayscale")} decorative provider={agentId} />
							<span className={cn("min-w-0 flex-1 truncate text-sm", connected || current ? "text-settings-label" : "text-settings-muted")}>{agentLabel(agentId)}</span>
							{split ? null : <ChevronRight className="size-4 shrink-0 text-settings-muted" aria-hidden="true" />}
						</Pressable>;
					})}
					{rows.length === 0 ? <p className="px-3 py-6 text-center text-sm text-settings-muted">{t("settings.harness.noResults")}</p> : null}
				</div>
			) : null}
			{selected ? (
				<div
					className={cn("flex min-h-0 w-full min-w-0 flex-1 flex-col gap-4 overflow-hidden", split && "p-5")}
					data-agent={selected}
					data-focus-highlighted={highlightedAgentId === selected ? "" : undefined}
					ref={detailRef}
				>
					{split ? null : <div className="shrink-0">
						<Button type="button" size="sm" variant="quiet" className="-ml-2 gap-1 px-2" onClick={closeDetail}>
							<ChevronLeft aria-hidden="true" />{t("settings.harness.allHarnesses")}
						</Button>
					</div>}
					<div className={cn("-mx-2 flex shrink-0 items-center gap-3 rounded-lg px-2 py-1 transition-[background-color,box-shadow] duration-200", highlightedAgentId === selected && "bg-accent-weak ring-2 ring-inset ring-accent")}>
						<AgentAvatar className="size-7 shrink-0" decorative provider={selected} />
						<span className="min-w-0 flex-1 text-subtitle font-semibold leading-6 text-settings-label" id={`harness-agent-${selected}`}>{agentLabel(selected)}</span>
					</div>
					<Tabs className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden" value={detailTab} onValueChange={(value) => { if (value !== "account") setLoginAgent(null); setDetailTab(value as CloudDetailTab); }}>
						<TabsList className={HARNESS_TABS_CLASS} aria-label={t("settings.harness.detailTabs", { agent: agentLabel(selected) })}>
							<TabsTrigger className={HARNESS_TAB_CLASS} value="account">{t("settings.harness.tabAccount")}</TabsTrigger>
							<TabsTrigger className={HARNESS_TAB_CLASS} value="models">{t("settings.harness.tabModels")}</TabsTrigger>
						</TabsList>
						<TabsContent value="account" className="settings-thin-scrollbar min-h-0 min-w-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain pt-4">{accountTab(selected)}</TabsContent>
						<TabsContent value="models" className="min-h-0 min-w-0 overflow-hidden pt-4 data-[state=active]:flex data-[state=active]:flex-col">
							<CloudHarnessModelsPanel agentId={selected} orgId={orgId} connected={selectedConnected} onOpenAccount={() => setDetailTab("account")} />
						</TabsContent>
					</Tabs>
				</div>
			) : null}
		</div>
	);
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
