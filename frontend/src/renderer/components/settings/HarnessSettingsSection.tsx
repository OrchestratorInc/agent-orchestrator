import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { ArrowUpCircle, BookOpen, Check, ChevronLeft, ChevronRight, Copy, KeyRound, LoaderCircle, LogIn, LogOut, RefreshCw, Search, Trash2, TriangleAlert, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import {
	agentReadinessQueryKeyForHost,
	cacheAgentReadiness,
	ensureAgentReadiness,
	useAgentReadinessQuery,
} from "../../hooks/useAgentReadinessQuery";
import { agentAuthPlansQueryKeyForHost, probeAgentAuth, useAgentAuthPlans, useStartAgentAuth } from "../../hooks/useAgentAuth";
import { agentModelsQueryOptions, refreshAgentModels, invalidateAgentModelCatalogs } from "../../hooks/useAgentModelsQuery";
import { fetchSessionMemory, formatCPU, formatMemory, sessionMemoryQueryOptions } from "../../hooks/useSessionMemory";
import { closeShellTerminal, shellTerminalsQueryKeyForHost, type ShellTerminal } from "../../hooks/useShellTerminals";
import type { TerminalSessionState } from "../../hooks/useTerminalSession";
import { agentLabel, AGENT_OPTIONS, type AgentId } from "../../lib/agent-options";
import { CLOUD_AGENT_PROVIDERS, isCloudHarnessConnected } from "../../lib/cloud-agents";
import { useCloudCp } from "../../hooks/useCloudCp";
import { useCloudOrg } from "../../hooks/useCloudOrg";
import { providerConnectionsQueryKey, useProviderConnections } from "../../hooks/useProviderConnections";
import { GitHubTokenField } from "../onboarding/GitHubTokenField";
import { HarnessModelDefaults } from "./HarnessModelDefaults";
import { HarnessUninstallGuide } from "./HarnessUninstallGuide";
import { CloudHarnessLoginPanel, type CloudHarness } from "./CloudHarnessLoginPanel";
import { SettingsRow } from "./SettingsRow";
import { apiErrorCode, apiErrorMessage, getApiBaseUrl } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import { baseUrlForHost, clientForSessionHost, labelForHost } from "../../lib/host-clients";
import { useConnectedHosts } from "../../hooks/useHostConnection";
import { LOCAL_HOST } from "../../lib/hosts";
import { createTerminalMux, muxUrlFromApiBase } from "../../lib/terminal-mux";
import { cn } from "../../lib/utils";
import { useShellMaybe } from "../../lib/shell-context";
import { useResolvedTheme } from "../../stores/ui-store";
import { AgentAvatar } from "../AgentAvatar";
import { ConfirmDialog } from "../ConfirmDialog";
import { TerminalPane } from "../TerminalPane";
import { Button } from "../ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../ui/tabs";
import { useCloudGate } from "../../hooks/useCloudGate";
import { SettingsSection } from "./SettingsSection";
import { SettingsOptionMenu } from "./SettingsOptionMenu";
import { fetchInstallers, fetchInstallJobs, fetchUpdateAdvisory, hasConfirmedHarnessUpdate, hasDefinitiveUpdateStatus, installerQueryKey, installJobsQueryKey, maintenanceMethodId, updateAdvisoryQueryKey, useHarnessUpdates, useHarnessActionRequest, versionLabel } from "../../hooks/useHarnessUpdates";
export { updateAdvisoryRefreshInterval } from "../../hooks/useHarnessUpdates";

type AgentInstallPlan = components["schemas"]["AgentInstallPlan"];
type InstallJob = components["schemas"]["InstallJob"];
type AgentUpdateAdvisory = components["schemas"]["AgentUpdateAdvisory"];
type AgentOperation = "install" | "reinstall" | "update" | "uninstall";
type InstalledOperation = "update" | "uninstall";
type AgentInstallMethod = AgentInstallPlan["methods"][number];
type OperationRequest = { agentId: AgentId; method: string };

const POLL_INTERVAL_MS = 1_000;
const AUTH_TERMINAL_LIFETIME_MS = 15 * 60_000;
// The first check right after a login terminal exits can fail transiently (the
// daemon's own readiness retry succeeds seconds later), so a login is re-checked
// a few times before the panel reports it did not take.
const AUTH_VERIFY_ATTEMPTS = 4;
const AUTH_VERIFY_RETRY_MS = 1_500;
const FOCUS_HIGHLIGHT_MS = 2_000;

type AgentAuthState = { pending: boolean; error: string | null };
type AgentAuthStates = Partial<Record<AgentId, AgentAuthState>>;
type AgentAuthProbeResult = Awaited<ReturnType<typeof probeAgentAuth>>;
const AUTH_STATE_RANK = {
	authorized: 0,
	not_applicable: 0,
	unauthorized: 1,
	configured: 2,
	unknown: 2,
} as const;
const INSTALL_STATE_RANK = {
	installed: 0,
	unknown: 1,
	not_installed: 2,
} as const;
type AuthTerminalWorkflow = {
	agentId: AgentId;
	action: string;
	terminal: components["schemas"]["ShellTerminalResponse"];
	guidance: string;
	terminalInput?: string;
	phase: "running" | "verifying" | "unauthorized" | "unverified" | "closing" | "cleanup_failed" | "timed_out";
	reason?: string;
	startedAt: number;
};

async function closeAuthTerminal(handleId: string, hostId?: string): Promise<void> {
	try {
		await closeShellTerminal(handleId, hostId);
	} catch (error) {
		if (apiErrorCode(error) !== "SHELL_TERMINAL_NOT_FOUND") throw error;
	}
}

function upsertJob(current: InstallJob[] | undefined, next: InstallJob): InstallJob[] {
	return [...(current ?? []).filter((job) => job.target !== next.target), next];
}

function isActive(job: InstallJob | undefined): boolean {
	return job?.status === "running" || job?.status === "installing" || job?.status === "verifying";
}

function supportsOperation(method: AgentInstallMethod | undefined, operation: InstalledOperation): boolean {
	return Boolean(method?.available && (operation === "update" ? method.updateAvailable : method.uninstallAvailable));
}

function diagnosticsText(agentId: AgentId, job: InstallJob): string {
	return [
		`${agentLabel(agentId)} installation diagnostics`,
		job.method ? `Method: ${job.method}` : "",
		job.expectedDestination ? `Expected destination: ${job.expectedDestination}` : "",
		job.error ? `Error: ${job.error}` : "",
		job.output ? `Output:\n${job.output}` : "",
	].filter(Boolean).join("\n");
}

/**
	* What the machine looked like when the diagnostics were copied: an install
	* that dies on a host with no memory left reads very differently from one
	* that dies on an idle laptop. Fetched once on the click rather than polled,
	* and left out entirely where the daemon cannot measure the host. English,
	* like the rest of this report: it is read by whoever fixes the bug.
	*/
async function machineText(queryClient: QueryClient, remoteClient?: ReturnType<typeof clientForSessionHost>): Promise<string> {
	let reading: Pick<Awaited<ReturnType<typeof fetchSessionMemory>>, "app" | "system" | "sessions">;
	try {
		reading = remoteClient ? await fetchRemoteMachine(remoteClient) : await queryClient.fetchQuery(sessionMemoryQueryOptions());
	} catch {
		return "";
	}
	const { app, system, sessions } = reading;
	if (!app && !system) return "";
	const lines = ["Machine"];
	if (app && system) {
		lines.push(`Memory: AO ${formatMemory(app.rssBytes)} · available ${formatMemory(system.availableBytes)} of ${formatMemory(system.totalBytes)}`);
	} else if (app) {
		lines.push(`Memory: AO ${formatMemory(app.rssBytes)}`);
	}
	if (system) {
		// Windows has no load average and reports the sentinel -1 rather than a
		// fabricated 0; leave the figure out of the report entirely there.
		const load = system.load1 >= 0 ? ` · load ${system.load1.toFixed(2)}` : "";
		lines.push(`CPU: ${formatCPU(system.cpuPercent)} of ${system.cpuCount} cores${load}`);
		if (system.swapBytesPerSec > 0) lines.push(`Swapping: ${formatMemory(system.swapBytesPerSec)}/s`);
	}
	if (sessions.length > 0) {
		lines.push(`Live sessions: ${sessions.length} · ${formatMemory(sessions.reduce((sum, s) => sum + s.rssBytes, 0))}`);
	}
	return lines.join("\n");
}

/** A remote install's diagnostics describe that host, so its numbers come
	* from its own daemon. Fetched directly: the shared query and the CPU graph
	* hold this computer's readings only. */
async function fetchRemoteMachine(client: ReturnType<typeof clientForSessionHost>) {
	const { data, error } = await client.GET("/api/v1/usage/sessions/memory", { params: { query: {} } });
	if (error) throw error;
	return { sessions: data?.sessions ?? [], system: data?.system, app: data?.app };
}

export type HarnessView = "local" | "cloud";

export function HarnessSettingsSection({
	focusAgentId,
	hostId,
	initialView = "local",
	startLogin = false,
	titleHidden = false,
}: {
	focusAgentId?: string;
	hostId?: string;
	initialView?: HarnessView;
	/** Start focusAgentId's local login flow once its row is ready (a shortcut from a login error elsewhere). */
	startLogin?: boolean;
	titleHidden?: boolean;
}) {
	const { t } = useTranslation();
	const { cloudEnabled } = useCloudGate();
	const [view, setView] = useState<HarnessView>(initialView);
	const [search, setSearch] = useState("");
	useEffect(() => setView(initialView), [initialView]);
	const cloudView = cloudEnabled && view === "cloud";
	const connected = useConnectedHosts();
	const [selectedHostId, setSelectedHostId] = useState(hostId ?? LOCAL_HOST);
	useEffect(() => setSelectedHostId(hostId ?? LOCAL_HOST), [hostId]);
	const remoteOffline = selectedHostId !== LOCAL_HOST && !connected.includes(selectedHostId);
	// An open harness page replaces the list, so its search and filters step aside.
	const [detailOpen, setDetailOpen] = useState(false);
	const showToolbar = cloudView || !detailOpen;
	return <SettingsSection title={t("settings.harness")} titleHidden={titleHidden} sectionId="harness">
		{showToolbar ? <div className="sticky top-0 z-10 -mt-[18px] flex items-center gap-2 bg-(--color-bg-primary) pb-2 pt-[18px]">
			<label className="flex h-9! min-w-0 flex-1 items-center gap-2 rounded-md border border-(--color-border-settings-input) bg-(--color-bg-settings-input) px-3">
				<Search aria-hidden="true" className="size-4 shrink-0 text-settings-muted" />
				<span className="sr-only">{t("settings.harness.search")}</span>
				<input aria-label={t("settings.harness.search")} className="min-w-0 flex-1 bg-transparent text-sm text-settings-label outline-none placeholder:text-settings-muted" placeholder={t("settings.harness.searchPlaceholder")} value={search} onChange={(event) => setSearch(event.target.value)} />
			</label>
			{cloudEnabled ? <Tabs value={cloudView ? "cloud" : "local"} onValueChange={(value) => setView(value as HarnessView)}><TabsList aria-label={t("settings.harness.viewLabel")}><TabsTrigger value="local">{t("settings.harness.viewLocal")}</TabsTrigger><TabsTrigger value="cloud">{t("settings.harness.viewCloud")}</TabsTrigger></TabsList></Tabs> : null}
		</div> : null}
		{showToolbar && !cloudView && (connected.length > 0 || remoteOffline) ? <SettingsOptionMenu
				aria-label={t("remote.host")}
				value={selectedHostId}
				options={[{ value: LOCAL_HOST, label: t("settings.harness.thisComputer") }, ...connected.map((id) => ({ value: id, label: labelForHost(id) ?? id })), ...(remoteOffline ? [{ value: selectedHostId, label: t("remote.hostLabel", { hostId: selectedHostId }) }] : [])]}
				onChange={setSelectedHostId}
				triggerClassName="w-fit max-w-full"
			/> : null}
		{showToolbar && !cloudView && selectedHostId !== LOCAL_HOST && !remoteOffline ? <p className="text-xs text-muted-foreground">{t("settings.harness.remoteBrowserAuthNote")}</p> : null}
		{cloudView ? <CloudHarnessContent focusAgentId={focusAgentId} search={search} /> : remoteOffline ? <p className="text-xs text-error" role="alert">{t("remote.hostOffline")}</p> : <LocalHarnessContent key={selectedHostId} focusAgentId={focusAgentId} hostId={selectedHostId === LOCAL_HOST ? undefined : selectedHostId} search={search} onDetailChange={setDetailOpen} startLogin={startLogin && selectedHostId === (hostId ?? LOCAL_HOST)} />}
	</SettingsSection>;
}

function CloudHarnessContent({ focusAgentId, search }: { focusAgentId?: string; search: string }) {
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

function LocalHarnessContent({ focusAgentId, hostId, search, onDetailChange, startLogin = false }: { focusAgentId?: string; hostId?: string; search: string; startLogin?: boolean; onDetailChange?: (open: boolean) => void }) {
	const { i18n, t } = useTranslation();
	const queryClient = useQueryClient();
	const client = clientForSessionHost(hostId);
	const readinessKey = useMemo(() => agentReadinessQueryKeyForHost(hostId), [hostId]);
	const installerKey = useMemo(() => hostId ? [...installerQueryKey, hostId] : installerQueryKey, [hostId]);
	const jobsKey = useMemo(() => hostId ? [...installJobsQueryKey, hostId] : installJobsQueryKey, [hostId]);
	const authPlansKey = useMemo(() => agentAuthPlansQueryKeyForHost(hostId), [hostId]);
	const shellKey = useMemo(() => shellTerminalsQueryKeyForHost(hostId), [hostId]);
	const agents = useAgentReadinessQuery(true, hostId);
	const installers = useQuery({ queryKey: installerKey, queryFn: () => fetchInstallers(hostId), staleTime: 60_000 });
	const jobs = useQuery({ queryKey: jobsKey, queryFn: () => fetchInstallJobs(hostId), retry: false });
	const authPlans = useAgentAuthPlans(hostId);
	const startAgentAuth = useStartAgentAuth(hostId);
	const startAgentLogout = useStartAgentAuth(hostId, "logout");
	const [logoutRequest, setLogoutRequest] = useState<AgentId | null>(null);
	const [authStates, setAuthStates] = useState<AgentAuthStates>({});
	const [actionErrors, setActionErrors] = useState<Partial<Record<AgentId, string>>>({});
	const [expandedDiagnostics, setExpandedDiagnostics] = useState<Partial<Record<AgentId, boolean>>>({});
	const [copiedAgent, setCopiedAgent] = useState<AgentId | null>(null);
	const [authWorkflow, setAuthWorkflow] = useState<AuthTerminalWorkflow | null>(null);
	const authWorkflowRef = useRef<AuthTerminalWorkflow | null>(null);
	const authStartPendingRef = useRef(false);
	const mountedRef = useRef(true);
	authWorkflowRef.current = authWorkflow;
	const activeInstallJobs = useRef(new Set<AgentId>());
	const expandedFailures = useRef(new Map<AgentId, string>());
	const pendingActions = useRef(new Set<AgentId>());
	const authChecksInFlight = useRef(new Map<AgentId, Promise<AgentAuthProbeResult | undefined>>());
	const [pendingAgentIds, setPendingAgentIds] = useState<Set<AgentId>>(new Set());
	const detailRef = useRef<HTMLDivElement>(null);
	const focusHandledRef = useRef(false);
	const focusPrimaryActionRef = useRef(false);
	const highlightTimerRef = useRef<number | null>(null);
	const [highlightedAgentId, setHighlightedAgentId] = useState<AgentId | null>(null);
	// The list opens one harness at a time; its page has Account, Models and Health tabs.
	const [selectedAgentId, setSelectedAgentId] = useState<AgentId | null>(null);
	const [detailTab, setDetailTab] = useState<HarnessDetailTab>("account");
	const selectedAgentRef = useRef<AgentId | null>(null);
	selectedAgentRef.current = selectedAgentId;
	const openDetail = (agentId: AgentId, tab: HarnessDetailTab = "account") => {
		selectedAgentRef.current = agentId;
		setSelectedAgentId(agentId);
		setDetailTab(tab);
	};
	const closeDetail = () => {
		selectedAgentRef.current = null;
		setSelectedAgentId(null);
	};
	// Errors and login terminals for a harness live on its Account tab. Show
	// them unless the user is looking at a different harness.
	const revealAgent = (agentId: AgentId) => {
		if (selectedAgentRef.current !== null && selectedAgentRef.current !== agentId) return;
		openDetail(agentId);
	};
	const [operationRequest, setOperationRequest] = useState<OperationRequest | null>(null);
	// Operation identity is local to jobs started here; restored jobs use neutral copy.
	const [operationAttempts, setOperationAttempts] = useState<Partial<Record<AgentId, { operation: AgentOperation; startedAt?: string }>>>({});
	// The API has no durable update history. Only record verified updates we
	// actually observed here; a restored job/check timestamp is not an update date.
	const [lastUpdatedAt, setLastUpdatedAt] = useState<Partial<Record<AgentId, string>>>({});
	const popupRequest = useHarnessActionRequest((state) => state.request);
	const [refreshedClient, setRefreshedClient] = useState<typeof client | null>(null);
	const pageRefreshed = refreshedClient === client;

	const plans = useMemo(() => new Map(installers.data?.map((plan) => [plan.agentId, plan]) ?? []), [installers.data]);
	const jobMap = useMemo(() => new Map(jobs.data?.map((job) => [job.target, job]) ?? []), [jobs.data]);
	const agentAuthPlans = useMemo(() => new Map(authPlans.data?.map((plan) => [plan.agentId, plan]) ?? []), [authPlans.data]);
	const readinessAgents = useMemo(() => new Map(agents.data?.agents.map((agent) => [agent.id, agent]) ?? []), [agents.data]);
	const installed = useMemo(
		() => new Set<AgentId>(agents.data?.agents.filter((agent) => agent.installation.state === "installed").map((agent) => agent.id as AgentId) ?? []),
		[agents.data],
	);
	// The page uses the initial installed snapshot immediately, like Install
	// and Login. A full-catalog background refresh must not delay its first
	// version lookup. The app-wide notification has its own readiness gate.
	const advisoryMap = useHarnessUpdates(installed, hostId);
	const operationMethodId = (agentId: AgentId): string => {
		const advisory = queryClient.getQueryData<AgentUpdateAdvisory>(updateAdvisoryQueryKey(agentId, hostId));
		return maintenanceMethodId(advisory);
	};
	const needsAuthentication = (agentId: AgentId): boolean => {
		const plan = agentAuthPlans.get(agentId);
		const status = readinessAgents.get(agentId)?.authentication.state;
		return Boolean(plan && plan.action !== "instructions" && status === "unauthorized");
	};
	const normalizedSearch = search.trim().toLowerCase();
	const targetAgentId = AGENT_OPTIONS.find((agentId) => agentId === focusAgentId) ?? null;
	const rows = AGENT_OPTIONS
		.filter((agentId) => agentId === targetAgentId || agentId === authWorkflow?.agentId || agentLabel(agentId).toLowerCase().includes(normalizedSearch))
		.sort((left, right) => {
			const leftAgent = readinessAgents.get(left);
			const rightAgent = readinessAgents.get(right);
			const authOrder = AUTH_STATE_RANK[leftAgent?.authentication.state ?? "unknown"]
				- AUTH_STATE_RANK[rightAgent?.authentication.state ?? "unknown"];
			if (authOrder !== 0) return authOrder;
			return INSTALL_STATE_RANK[leftAgent?.installation.state ?? "unknown"]
				- INSTALL_STATE_RANK[rightAgent?.installation.state ?? "unknown"];
		});
	const updateAuthState = useCallback((agentId: AgentId, patch: Partial<AgentAuthState>) => {
		setAuthStates((current) => ({
			...current,
			[agentId]: { pending: false, error: null, ...current[agentId], ...patch },
		}));
	}, []);
	const activeKey = useMemo(
		() => (jobs.data ?? []).filter((job) => isActive(job)).map((job) => job.target).sort().join(","),
		[jobs.data],
	);
	const refreshInstalledAgent = useCallback((agentId: AgentId) => {
		setActionErrors((current) => ({ ...current, [agentId]: undefined }));
		void client.POST("/api/v1/agents/{agent}/probe", {
			params: { path: { agent: agentId } },
		}).finally(async () => {
			try {
				const readiness = await ensureAgentReadiness([agentId], "display", hostId);
				cacheAgentReadiness(queryClient, readiness, hostId);
			} catch {
				await queryClient.invalidateQueries({ queryKey: readinessKey });
			} finally {
				await Promise.all([
					queryClient.invalidateQueries({ queryKey: updateAdvisoryQueryKey(agentId, hostId) }),
					queryClient.invalidateQueries({ queryKey: installerKey }),
					queryClient.invalidateQueries({ queryKey: authPlansKey }),
					invalidateAgentModelCatalogs(queryClient, agentId, hostId),
				]);
			}
		});
	}, [authPlansKey, client, hostId, installerKey, queryClient, readinessKey]);

	useEffect(() => {
		let active = true;
		let retryTimer: ReturnType<typeof setTimeout> | undefined;
		setRefreshedClient(null);
		const invalidateHarnessQueries = () => Promise.all([
			queryClient.invalidateQueries({ queryKey: readinessKey }),
			queryClient.invalidateQueries({ queryKey: installerKey }),
			queryClient.invalidateQueries({ queryKey: jobsKey }),
			queryClient.invalidateQueries({ queryKey: authPlansKey }),
		]);
		// Page-open refresh stays silent, but a failed refresh must not leave
		// stale or unknown readiness in place: fall back to ensure, and re-fetch
		// the readiness snapshot if that fails too.
		const recoverReadiness = async () => {
			try {
				const readiness = await ensureAgentReadiness([], "display", hostId);
				if (active) cacheAgentReadiness(queryClient, readiness, hostId);
				return true;
			} catch {
				if (active) await queryClient.invalidateQueries({ queryKey: readinessKey });
				return false;
			}
		};
		const refresh = async () => {
			let complete = false;
			try {
				const { error } = await client.POST("/api/v1/agents/refresh");
				if (!active) return;
				complete = error ? await recoverReadiness() : true;
				if (complete) await invalidateHarnessQueries();
			} catch {
				if (active) complete = await recoverReadiness();
			}
			if (!active) return;
			if (complete) {
				// Revalidate after readiness without clearing the previous result.
				void queryClient.invalidateQueries({ queryKey: ["agent-update-advisory", hostId ?? LOCAL_HOST] });
				if (active) setRefreshedClient(client);
			} else retryTimer = setTimeout(() => void refresh(), 30_000);
		};
		void refresh();
		return () => { active = false; clearTimeout(retryTimer); };
	}, [authPlansKey, client, hostId, installerKey, jobsKey, queryClient, readinessKey]);
	useEffect(() => {
		if (focusHandledRef.current || !targetAgentId) return;
		if (agents.isPending || installers.isPending || jobs.isPending || authPlans.isPending) return;
		focusHandledRef.current = true;
		focusPrimaryActionRef.current = true;
		openDetail(targetAgentId);
		setHighlightedAgentId(targetAgentId);
		highlightTimerRef.current = window.setTimeout(() => setHighlightedAgentId(null), FOCUS_HIGHLIGHT_MS);
	}, [agents.isPending, authPlans.isPending, installers.isPending, jobs.isPending, targetAgentId]);

	useEffect(() => {
		if (!focusPrimaryActionRef.current || !selectedAgentId) return;
		const detail = detailRef.current;
		if (!detail) return;
		focusPrimaryActionRef.current = false;
		detail.scrollIntoView?.({ behavior: "smooth", block: "start" });
		const primaryAction = detail.querySelector<HTMLElement>("[data-harness-primary-action]:not(:disabled)");
		(primaryAction ?? detail.querySelector<HTMLElement>("[role=tab][data-state=active]"))?.focus({ preventScroll: true });
	}, [selectedAgentId]);

	// Wide layouts keep the list beside the open harness; narrow ones swap between them.
	const layoutRef = useRef<HTMLDivElement>(null);
	const split = useWideLayout(layoutRef);
	useEffect(() => {
		if (!split || selectedAgentRef.current !== null || rows.length === 0) return;
		openDetail(rows[0]);
	});
	useEffect(() => {
		onDetailChange?.(selectedAgentId !== null && !split);
	}, [onDetailChange, selectedAgentId, split]);
	useEffect(() => () => onDetailChange?.(false), [onDetailChange]);

	useEffect(() => () => {
		if (highlightTimerRef.current !== null) window.clearTimeout(highlightTimerRef.current);
	}, []);

	// A "Log in" shortcut elsewhere (the task composer's model error) lands here
	// and starts the same login flow the row's own button would, exactly once.
	const startAuthRef = useRef<(agentId: AgentId) => Promise<boolean>>(async () => false);
	const autoLoginHandledRef = useRef(false);
	useEffect(() => {
		if (!startLogin || autoLoginHandledRef.current || !targetAgentId) return;
		if (agents.isPending || authPlans.isPending) return;
		autoLoginHandledRef.current = true;
		const plan = agentAuthPlans.get(targetAgentId);
		if (!plan || !plan.available || plan.action === "instructions") return;
		void startAuthRef.current(targetAgentId);
	}, [agentAuthPlans, agents.isPending, authPlans.isPending, startLogin, targetAgentId]);

	useEffect(() => {
		if (!activeKey) return;
		const timer = window.setInterval(() => void jobs.refetch(), POLL_INTERVAL_MS);
		return () => window.clearInterval(timer);
	}, [activeKey, jobs.refetch]);

	useEffect(() => {
		for (const job of jobs.data ?? []) {
			const agentId = job.target as AgentId;
			if (isActive(job)) {
				activeInstallJobs.current.add(agentId);
				continue;
			}
			const completedWhileMounted = activeInstallJobs.current.delete(agentId);
			if (job.status !== "succeeded" || !completedWhileMounted) continue;
			refreshInstalledAgent(agentId);
		}
	}, [jobs.data, refreshInstalledAgent]);

	useEffect(() => {
		for (const job of jobs.data ?? []) {
			const agentId = job.target as AgentId;
			const attempt = operationAttempts[agentId];
			if (!attempt?.startedAt || attempt.startedAt !== job.startedAt) continue;
			if (isActive(job)) expandedFailures.current.delete(agentId);
			if ((job.status === "failed" || job.status === "unsupported" || job.status === "interrupted")
				&& expandedFailures.current.get(agentId) !== attempt.startedAt) {
				expandedFailures.current.set(agentId, attempt.startedAt);
				if (!authWorkflowRef.current || authWorkflowRef.current.agentId === agentId) revealAgent(agentId);
			}
			if (job.status !== "succeeded") continue;
			if (attempt.operation === "update") {
				if (job.finishedAt && Number.isFinite(Date.parse(job.finishedAt))) {
					setLastUpdatedAt((current) => current[agentId] === job.finishedAt ? current : { ...current, [agentId]: job.finishedAt! });
				}
			} else {
				setLastUpdatedAt((current) => current[agentId] === undefined ? current : { ...current, [agentId]: undefined });
			}
		}
	}, [jobs.data, operationAttempts]);

	useEffect(() => {
		setExpandedDiagnostics((current) => {
			let changed = false;
			const next = { ...current };
			for (const agentId of installed) {
				if (next[agentId]) {
					delete next[agentId];
					changed = true;
				}
			}
			return changed ? next : current;
		});
	}, [installed]);

	const updateJob = (job: InstallJob) => {
		setActionErrors((current) => ({ ...current, [job.target as AgentId]: undefined }));
		queryClient.setQueryData<InstallJob[]>(jobsKey, (current) => upsertJob(current, job));
	};

	const beginAction = (agentId: AgentId): boolean => {
		if (pendingActions.current.has(agentId)) return false;
		pendingActions.current.add(agentId);
		setPendingAgentIds(new Set(pendingActions.current));
		return true;
	};

	const endAction = (agentId: AgentId) => {
		pendingActions.current.delete(agentId);
		setPendingAgentIds(new Set(pendingActions.current));
	};

	const startAgentOperation = async (agentId: AgentId, method: string, operation: AgentOperation): Promise<boolean> => {
		if (isActive(jobMap.get(agentId)) || authWorkflowRef.current?.agentId === agentId || authStartPendingRef.current) return false;
		if ((operation === "update" || operation === "uninstall") && (!installed.has(agentId)
			|| operationMethodId(agentId) !== method
			|| !supportsOperation(plans.get(agentId)?.methods.find((candidate) => candidate.id === method), operation))) return false;
		if (operation === "update" && (needsAuthentication(agentId) || authPlans.isPending || authPlans.isError)) return false;
		if (!beginAction(agentId)) return false;
		setOperationAttempts((current) => ({ ...current, [agentId]: { operation } }));
		setActionErrors((current) => ({ ...current, [agentId]: undefined }));
		try {
			let expectedVersion: string | undefined;
			if (operation === "update") {
				const key = updateAdvisoryQueryKey(agentId, hostId);
				const target = queryClient.getQueryData<AgentUpdateAdvisory>(key)?.latestVersion;
				await queryClient.cancelQueries({ queryKey: key });
				const fresh = await fetchUpdateAdvisory(agentId, hostId, true);
				queryClient.setQueryData(key, fresh);
				if (!mountedRef.current || !hasConfirmedHarnessUpdate(fresh) || fresh.latestVersion !== target || maintenanceMethodId(fresh) !== method) return false;
				expectedVersion = fresh.latestVersion;
			}
			const { data, error } = await client.POST("/api/v1/agents/{agent}/install", {
				params: { path: { agent: agentId } },
				body: { method, operation, ...(expectedVersion ? { expectedVersion } : {}) },
			});
			if (error || !data) {
				setActionErrors((current) => ({ ...current, [agentId]: apiErrorMessage(error, t(operation === "uninstall" ? "settings.harness.uninstallFailed" : operation === "update" ? "settings.harness.updateFailed" : "settings.harness.startFailed")) }));
				revealAgent(agentId);
				return false;
			}
			setOperationAttempts((current) => ({ ...current, [agentId]: { operation, startedAt: data.startedAt } }));
			updateJob(data);
			if (data.status === "succeeded") refreshInstalledAgent(agentId);
			return true;
		} catch (error) {
			setActionErrors((current) => ({ ...current, [agentId]: error instanceof Error ? error.message : t("settings.harness.startFailed") }));
			revealAgent(agentId);
			return false;
		} finally {
			endAction(agentId);
		}
	};

	const requestInstalledOperation = (agentId: AgentId, operation: InstalledOperation) => {
		if (!installed.has(agentId) || pendingActions.current.has(agentId) || isActive(jobMap.get(agentId))
			|| authWorkflowRef.current?.agentId === agentId || authStartPendingRef.current) return;
		const methods = plans.get(agentId)?.methods ?? [];
		const method = operationMethodId(agentId);
		if (!method || !supportsOperation(methods.find((candidate) => candidate.id === method), operation)) return;
		setActionErrors((current) => ({ ...current, [agentId]: undefined }));
		if (operation === "update") {
			void startAgentOperation(agentId, method, operation);
			return;
		}
		setOperationRequest({ agentId, method });
	};

	const requestMethods = operationRequest ? plans.get(operationRequest.agentId)?.methods ?? [] : [];
	const requestMethod = requestMethods.find((method) => method.id === operationRequest?.method);
	const requestBusy = operationRequest ? pendingAgentIds.has(operationRequest.agentId) : false;
	const canConfirmOperation = Boolean(operationRequest && installed.has(operationRequest.agentId)
		&& !requestBusy && !isActive(jobMap.get(operationRequest.agentId))
		&& !authStates[operationRequest.agentId]?.pending && authWorkflow?.agentId !== operationRequest.agentId
		&& operationMethodId(operationRequest.agentId) === operationRequest.method
		&& supportsOperation(requestMethod, "uninstall"));

	const verifyInstall = async (agentId: AgentId) => {
		if (!beginAction(agentId)) return;
		setActionErrors((current) => ({ ...current, [agentId]: undefined }));
		try {
			const { data, error } = await client.POST("/api/v1/agents/{agent}/verify", {
				params: { path: { agent: agentId } },
			});
			if (error || !data) {
				setActionErrors((current) => ({ ...current, [agentId]: apiErrorMessage(error, t("settings.harness.verifyFailed", { agent: agentLabel(agentId) })) }));
				return;
			}
			updateJob(data);
			if (data.status === "succeeded") refreshInstalledAgent(agentId);
		} finally {
			endAction(agentId);
		}
	};

	/** Diagnostics plus the machine they were taken on. */
	const copyDiagnostics = async (agentId: AgentId, job: InstallJob) => {
		const machine = await machineText(queryClient, hostId ? client : undefined);
		await copyText(agentId, [diagnosticsText(agentId, job), machine].filter(Boolean).join("\n\n"));
	};

	const copyText = async (agentId: AgentId, text: string) => {
		await aoBridge.clipboard.writeText(text);
		setCopiedAgent(agentId);
		window.setTimeout(() => setCopiedAgent((current) => (current === agentId ? null : current)), 1_500);
	};

	const startAuth = async (agentId: AgentId, action: "login" | "logout" = "login"): Promise<boolean> => {
		if (authWorkflowRef.current || authStartPendingRef.current || pendingActions.current.has(agentId) || isActive(jobMap.get(agentId))) return false;
		if (action === "logout" && !agentAuthPlans.get(agentId)?.logoutCommand) return false;
		authStartPendingRef.current = true;
		updateAuthState(agentId, { pending: true, error: null });
		try {
			const plan = agentAuthPlans.get(agentId);
			if (action === "login" && plan?.launchMode === "documentation") {
				await aoBridge.app.openExternal(plan.documentationUrl);
				return true;
			}
			const result = await (action === "logout" ? startAgentLogout : startAgentAuth).mutateAsync(agentId);
			if (!mountedRef.current) {
				await closeAuthTerminal(result.terminal.handleId, hostId);
				queryClient.setQueryData<ShellTerminal[]>(shellKey, (current) => current?.filter((terminal) => terminal.handleId !== result.terminal.handleId));
				void queryClient.invalidateQueries({ queryKey: shellKey });
				return false;
			}
			const workflow: AuthTerminalWorkflow = {
				agentId,
				action: result.action,
				terminal: result.terminal,
				guidance: result.guidance ?? "",
				terminalInput: result.terminalInput,
				phase: "running",
				startedAt: Date.now(),
			};
			authWorkflowRef.current = workflow;
			setAuthWorkflow(workflow);
			revealAgent(agentId);
			void queryClient.invalidateQueries({ queryKey: shellKey });
			return true;
		} catch (error) {
			if (mountedRef.current) updateAuthState(agentId, { error: error instanceof Error ? error.message : t("settings.harness.authFailed") });
			return false;
		} finally {
			authStartPendingRef.current = false;
			if (mountedRef.current) updateAuthState(agentId, { pending: false });
		}
	};

	useEffect(() => {
		if (!popupRequest || popupRequest.agentId !== focusAgentId || popupRequest.hostId !== hostId || !pageRefreshed) return;
		const advisoryQuery = advisoryMap.get(popupRequest.agentId);
		if (agents.isPending || agents.isFetching || installers.isPending || jobs.isPending || authPlans.isPending || advisoryQuery?.isFetching) return;
		useHarnessActionRequest.getState().setRequest(null);
		if (agents.isError || installers.isError || jobs.isError || authPlans.isError || !installed.has(popupRequest.agentId)) return;
		// Re-evaluate the action using the page's freshly loaded readiness. An
		// update click must not bypass login or target a different release.
		if (needsAuthentication(popupRequest.agentId)) {
			void startAuth(popupRequest.agentId);
		} else if (popupRequest.action === "update" && !advisoryQuery?.isError && hasConfirmedHarnessUpdate(advisoryQuery?.data)
			&& advisoryQuery?.data?.latestVersion === popupRequest.latestVersion) {
			void startAgentOperation(popupRequest.agentId, operationMethodId(popupRequest.agentId), "update");
		}
	}, [popupRequest, focusAgentId, hostId, pageRefreshed, advisoryMap, agents, installers, jobs, authPlans, installed, needsAuthentication, startAuth, startAgentOperation, operationMethodId]);
	startAuthRef.current = startAuth;

	const checkAuth = useCallback(async (
		agentId: AgentId,
		{ fresh = false }: { fresh?: boolean } = {},
	): Promise<AgentAuthProbeResult | undefined> => {
		const existing = authChecksInFlight.current.get(agentId);
		if (existing && !fresh) return existing;
		const check = (async () => {
			if (existing) await existing;
			try {
				const result = await probeAgentAuth(agentId, hostId);
				// The probe also makes the daemon rediscover this agent's model
				// catalogs; drop the renderer's copies so a stale login error in
				// an open composer or picker clears without a manual refresh.
				void invalidateAgentModelCatalogs(queryClient, agentId, hostId);
				const readiness = await ensureAgentReadiness([agentId], "display", hostId);
				cacheAgentReadiness(queryClient, readiness, hostId);
				return result;
			} catch {
				return undefined;
			}
		})();
		authChecksInFlight.current.set(agentId, check);
		const finishCheck = () => {
			if (authChecksInFlight.current.get(agentId) === check) authChecksInFlight.current.delete(agentId);
		};
		void check.then(finishCheck, finishCheck);
		return check;
	}, [hostId, queryClient]);

	const finishAuth = useCallback(async (workflow: AuthTerminalWorkflow) => {
		if (authWorkflowRef.current?.terminal.handleId !== workflow.terminal.handleId || authWorkflowRef.current.phase !== "running") return;
		authWorkflowRef.current = { ...workflow, phase: "verifying", reason: undefined };
		setAuthWorkflow(authWorkflowRef.current);
		// MiMo can confirm a stored provider key locally without validating it upstream.
		const completed = (candidate: AgentAuthProbeResult | undefined) => workflow.action === "logout"
			? candidate?.agent?.authStatus === "unauthorized"
			: candidate?.agent?.authStatus === "authorized" || (workflow.agentId === "mimo-code" && candidate?.agent?.authStatus === "configured");
		let result = await checkAuth(workflow.agentId, { fresh: true });
		for (let attempt = 1; attempt < AUTH_VERIFY_ATTEMPTS && !completed(result); attempt++) {
			await new Promise((resolve) => window.setTimeout(resolve, AUTH_VERIFY_RETRY_MS));
			if (authWorkflowRef.current?.terminal.handleId !== workflow.terminal.handleId) return;
			result = await checkAuth(workflow.agentId, { fresh: true });
		}
		if (authWorkflowRef.current?.terminal.handleId !== workflow.terminal.handleId) return;
		if (completed(result)) {
			try {
				await closeAuthTerminal(workflow.terminal.handleId, hostId);
			} catch (error) {
				setAuthWorkflow((current) => current?.terminal.handleId === workflow.terminal.handleId ? { ...current, phase: "cleanup_failed", reason: error instanceof Error ? error.message : t("settings.harness.authFailed") } : current);
				return;
			}
			authWorkflowRef.current = null;
			setAuthWorkflow(null);
			void queryClient.invalidateQueries({ queryKey: shellKey });
			return;
		}
		setAuthWorkflow((current) => current?.terminal.handleId === workflow.terminal.handleId ? {
			...current,
			phase: result?.agent?.authStatus === "unauthorized" ? "unauthorized" : "unverified",
			reason: workflow.action === "logout" ? t("settings.harness.logoutUnverified") : result?.agent?.authStatus === "unauthorized" ? t("settings.harness.notLoggedIn") : t("settings.harness.loginUnknown"),
		} : current);
	}, [checkAuth, hostId, queryClient, shellKey, t]);

	const closeAuth = useCallback(async (workflow: AuthTerminalWorkflow): Promise<boolean> => {
		if (authWorkflowRef.current?.terminal.handleId !== workflow.terminal.handleId) return false;
		setAuthWorkflow((current) => current?.terminal.handleId === workflow.terminal.handleId ? { ...current, phase: "closing", reason: undefined } : current);
		try {
			await closeAuthTerminal(workflow.terminal.handleId, hostId);
			void queryClient.invalidateQueries({ queryKey: shellKey });
			await checkAuth(workflow.agentId, { fresh: true });
			if (authWorkflowRef.current?.terminal.handleId === workflow.terminal.handleId) {
				authWorkflowRef.current = null;
				setAuthWorkflow(null);
			}
			return true;
		} catch (error) {
			setAuthWorkflow((current) => current?.terminal.handleId === workflow.terminal.handleId ? { ...current, phase: "cleanup_failed", reason: error instanceof Error ? error.message : t("settings.harness.authFailed") } : current);
			return false;
		}
	}, [checkAuth, hostId, queryClient, shellKey, t]);

	// A login the panel could not confirm may still have taken: the daemon keeps
	// re-checking readiness. Once the harness reads as logged in, close the panel
	// instead of leaving a stale "signed out" terminal on screen.
	useEffect(() => {
		if (!authWorkflow || (authWorkflow.phase !== "unauthorized" && authWorkflow.phase !== "unverified")) return;
		const expected = authWorkflow.action === "logout" ? "unauthorized" : "authorized";
		if (readinessAgents.get(authWorkflow.agentId)?.authentication.state === expected) void closeAuth(authWorkflow);
	}, [authWorkflow, readinessAgents, closeAuth]);

	useEffect(() => {
		if (!authWorkflow || authWorkflow.phase !== "running") return;
		const handleId = authWorkflow.terminal.handleId;
		const remaining = Math.max(0, AUTH_TERMINAL_LIFETIME_MS - (Date.now() - authWorkflow.startedAt));
		const timeout = window.setTimeout(async () => {
			if (authWorkflowRef.current?.terminal.handleId !== handleId) return;
			setAuthWorkflow((current) => current?.terminal.handleId === handleId ? { ...current, phase: "closing", reason: undefined } : current);
			try {
				await closeAuthTerminal(handleId, hostId);
				setAuthWorkflow((current) => current?.terminal.handleId === handleId ? { ...current, phase: "timed_out", reason: t("settings.harness.authTimedOut") } : current);
				void queryClient.invalidateQueries({ queryKey: shellKey });
				await checkAuth(authWorkflow.agentId, { fresh: true });
			} catch (error) {
				setAuthWorkflow((current) => current?.terminal.handleId === handleId ? { ...current, phase: "cleanup_failed", reason: error instanceof Error ? error.message : t("settings.harness.authFailed") } : current);
			}
		}, remaining);
		return () => window.clearTimeout(timeout);
	}, [authWorkflow, checkAuth, hostId, queryClient, shellKey, t]);

	useEffect(() => {
		mountedRef.current = true;
		return () => {
			mountedRef.current = false;
			const workflow = authWorkflowRef.current;
			if (workflow) void closeAuthTerminal(workflow.terminal.handleId, hostId).catch(() => undefined);
		};
	}, [hostId]);

	const describe = (agentId: AgentId) => {
		const plan = plans.get(agentId);
		const job = jobMap.get(agentId);
		const isInstalled = installed.has(agentId);
		const availableMethods = plan?.methods.filter((method) => method.available) ?? [];
		const installMethod = availableMethods.find((method) => method.recommended) ?? availableMethods[0];
		const methodId = operationMethodId(agentId);
		const maintenanceMethod = plan?.methods.find((method) => method.id === methodId);
		const canUpdate = supportsOperation(maintenanceMethod, "update");
		const canUninstall = supportsOperation(maintenanceMethod, "uninstall");
		// A harness with no installable method (built into AO, or an installer
		// that must run interactively here) explains itself; ownership is not the question.
		const manualOnly = Boolean(plan?.methods.length) && plan!.methods.every((method) => method.id === "manual");
		const ownershipReason = !methodId ? (manualOnly && plan?.reason ? plan.reason : t("settings.harness.ownershipUnknown")) : undefined;
		const updateReason = ownershipReason || maintenanceMethod?.updateReason || maintenanceMethod?.reason || t("settings.harness.updateUnsupported");
		const uninstallReason = ownershipReason || maintenanceMethod?.uninstallReason || maintenanceMethod?.reason || t("settings.harness.uninstallUnsupported");
		const advisoryQuery = advisoryMap.get(agentId);
		const advisory = advisoryQuery?.data;
		const updateUnknown = advisoryQuery?.isError || !hasDefinitiveUpdateStatus(advisory);
		// A failed background fetch is not a new observation. Keep the last
		// action; the click-time probe still verifies target and ownership.
		const updateAvailable = hasConfirmedHarnessUpdate(advisory);
		const currentVersion = advisory?.currentVersion?.trim();
		const pending = pendingAgentIds.has(agentId);
		const active = isActive(job);
		const busy = pending || active;
		const actionError = actionErrors[agentId];
		const attempt = operationAttempts[agentId];
		const currentOperation = attempt && (pending || actionError || attempt.startedAt === job?.startedAt) ? attempt.operation : undefined;
		const jobFailed = job?.status === "failed" || job?.status === "unsupported" || job?.status === "interrupted";
		const failed = jobFailed || Boolean(actionError);
		const readinessAgent = readinessAgents.get(agentId);
		const availableMethodsLabel = availableMethods.length > 0
			? new Intl.ListFormat(i18n.resolvedLanguage ?? "en", { style: "short", type: "conjunction" }).format(availableMethods.map((method) => method.label))
			: undefined;
		// Unknown inventory is not proof that installing is safe. Preserve the
		// existing cached-readiness fallback when fetching the snapshot fails.
		const installationPending = !agents.error && (agents.isPending || readinessAgent?.installation.state === "unknown");
		const authPlan = agentAuthPlans.get(agentId);
		const isSetupAction = authPlan?.action === "setup";
		const authState = authStates[agentId];
		const authStatus = readinessAgent?.authentication.state;
		const rowAuthWorkflow = authWorkflow?.agentId === agentId ? authWorkflow : null;
		const authBusy = Boolean(rowAuthWorkflow || authState?.pending);
		const needsLogin = needsAuthentication(agentId);
		const showUpdate = updateAvailable && !authPlans.isPending && !authPlans.isError;
		const hasDiagnostics = Boolean(job && jobFailed && (job.error || job.output || job.method || job.expectedDestination));
		const authSummary = authStatus === "configured" ? t("settings.harness.loggedIn")
			: authStatus === "authorized" ? t(isSetupAction ? "settings.harness.configured" : "settings.harness.loggedIn")
			: authStatus === "not_applicable" || (!authPlans.isPending && (!authPlan || authPlan.action === "instructions")) ? t("settings.harness.installed")
			: authPlan && !authPlan.available ? (authPlan.reason ?? t("settings.harness.authFailed"))
			: authStatus === "unauthorized" ? t(isSetupAction ? "settings.harness.notConfigured" : "settings.harness.loginRequired")
			: t(isSetupAction ? "settings.harness.configurationUnknown" : "settings.harness.loginUnknown");
		const progressLabel = job?.status === "verifying" ? t("settings.harness.verifying")
			: currentOperation === "update" ? t("settings.harness.updating")
			: currentOperation === "uninstall" ? t("settings.harness.uninstalling")
			: currentOperation === "install" ? t("settings.harness.installing") : t("settings.harness.working");
		const rowError = actionError ?? authState?.error ?? (jobFailed ? job?.error ?? t("settings.harness.installFailed") : undefined);
		const listTone: HarnessTone = !isInstalled ? (rowError ? "error" : "off")
			: rowError || needsLogin ? "error" : "ok";
		const authProgress = rowAuthWorkflow?.phase === "verifying" ? t(rowAuthWorkflow.action === "logout" ? "settings.harness.checkingLogout" : "settings.harness.checkingLogin")
			: rowAuthWorkflow?.phase === "closing" ? t("settings.harness.authClosing")
			: rowAuthWorkflow && rowAuthWorkflow.phase !== "running" ? rowAuthWorkflow.reason ?? t("settings.harness.loginUnknown")
			: t(rowAuthWorkflow?.action === "logout" ? "settings.harness.logoutInProgress" : "settings.harness.loginInProgress");
		const statusLabel = isInstalled ? rowError ?? (authBusy ? authProgress : authSummary)
			: installationPending ? t("settings.harness.installationUnknown")
			: job?.status === "interrupted" ? t("settings.harness.interrupted")
			: rowError ?? (installMethod && availableMethodsLabel ? t("settings.harness.availableWith", { method: availableMethodsLabel }) : plan?.reason ?? t("settings.harness.manualRequired"));
		// Why a harness is not installed (or cannot be), without repeating the error alert.
		const installNote = isInstalled || installationPending ? undefined
			: job?.status === "interrupted" ? t("settings.harness.interrupted")
			: installMethod && availableMethodsLabel ? t("settings.harness.availableWith", { method: availableMethodsLabel }) : plan?.reason ?? t("settings.harness.manualRequired");
		const versionText = currentVersion ? versionLabel(currentVersion) : t("settings.harness.versionUnavailable");
		const versionUnverified = advisoryQuery?.isError || job?.status === "verifying" || (currentOperation === "update" && (busy || failed));
		const updatedAt = lastUpdatedAt[agentId];
		const actionClass = "h-8 min-w-20 px-3 focus-visible:ring-2 focus-visible:ring-ring";
		const progressAction = <span role="status"><Button type="button" size="sm" variant="outline" className={actionClass} disabled><LoaderCircle className="animate-spin" aria-hidden="true" />{progressLabel}</Button></span>;
		const authBusyAction = <Button type="button" size="sm" className={actionClass} disabled>
			{rowAuthWorkflow && !["running", "verifying", "closing"].includes(rowAuthWorkflow.phase) ? null : <LoaderCircle className="animate-spin" aria-hidden="true" />}
			{rowAuthWorkflow?.phase === "verifying" ? t(rowAuthWorkflow.action === "logout" ? "settings.harness.checkingLogout" : "settings.harness.checkingLogin") : rowAuthWorkflow?.action === "logout" ? t("settings.harness.logout") : isSetupAction ? t("settings.harness.setup") : t("settings.harness.login")}
		</Button>;
		// Install, update and login each own one row in the Account tab. While a
		// job or login runs, the row that started it shows progress instead.
		const installationAction = busy ? progressAction
			: authBusy ? null
			: !isInstalled ? (
				!installationPending && installMethod ? <Button type="button" data-harness-primary-action="" size="sm" className={actionClass} onClick={() => void startAgentOperation(agentId, installMethod.id, "install")}>
					{failed ? t("settings.harness.retryInstall") : t("settings.harness.install")}
				</Button> : !installationPending && plan?.command ? <Button type="button" size="sm" variant="outline" className={actionClass} onClick={() => void copyText(agentId, plan.command!)}>
					{copiedAgent === agentId ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}{t(copiedAgent === agentId ? "settings.harness.copied" : "settings.harness.copyCommand")}
				</Button> : null
			) : showUpdate && !needsLogin ? (
				canUpdate ? <Button type="button" data-harness-primary-action="" size="sm" className={actionClass} onClick={() => requestInstalledOperation(agentId, "update")}>
					{t(failed && currentOperation === "update" ? "settings.harness.retryUpdate" : "settings.harness.update")}
				</Button> : plan?.documentationUrl ? <Button type="button" data-harness-primary-action="" size="sm" variant="outline" className={actionClass} title={updateReason} onClick={() => void aoBridge.app.openExternal(plan.documentationUrl)}>{t("settings.harness.manualUpdate")}</Button> : null
			) : null;
		const canRelogin = Boolean(authPlan?.available && authPlan.action !== "instructions" && (authStatus === "authorized" || authStatus === "configured"));
		const accountAction = authBusy ? authBusyAction
			: busy ? null
			: needsLogin ? <Button type="button" data-harness-primary-action="" data-terminal-focus-handoff="true" size="sm" className={actionClass} disabled={!authPlan?.available || Boolean(authWorkflow)} onClick={() => void startAuth(agentId)}>
				{t(isSetupAction ? "settings.harness.setup" : authState?.error ? "settings.harness.retryLogin" : "settings.harness.login")}
			</Button>
			: canRelogin ? <Button type="button" data-terminal-focus-handoff="true" size="sm" variant="outline" className={actionClass} disabled={Boolean(authWorkflow)} onClick={() => void startAuth(agentId)}>
				{t(isSetupAction ? "settings.harness.openSetup" : "settings.harness.loginAgain")}
			</Button> : null;
		return {
			agentId, plan, job, isInstalled, methodId, maintenanceMethod, canUpdate, canUninstall, manualOnly, updateReason, uninstallReason,
			advisoryQuery, advisory, updateUnknown, updateAvailable, currentVersion, busy, failed, jobFailed, currentOperation, readinessAgent,
			authPlan, authStatus, authSummary, rowAuthWorkflow, authBusy, showUpdate, hasDiagnostics, rowError, versionText, versionUnverified,
			updatedAt, installationAction, accountAction, progressLabel, installationPending, installNote, listTone, statusLabel,
		};
	};
	type HarnessDetails = ReturnType<typeof describe>;

	const formatTimestamp = (value?: string | null) => {
		if (!value || !Number.isFinite(Date.parse(value))) return undefined;
		const date = new Date(value);
		return <time dateTime={value} title={date.toLocaleString(i18n.resolvedLanguage)}>{date.toLocaleString(i18n.resolvedLanguage, { day: "numeric", month: "short", hour: "numeric", minute: "2-digit" })}</time>;
	};

	const diagnosticsPanel = (details: HarnessDetails) => {
		const { agentId, job } = details;
		if (!details.hasDiagnostics) return null;
		return <div className="space-y-2">
			<Button type="button" aria-expanded={expandedDiagnostics[agentId] === true} size="sm" variant="ghost" onClick={() => setExpandedDiagnostics((current) => ({ ...current, [agentId]: !current[agentId] }))}>
				{t(expandedDiagnostics[agentId] ? "settings.harness.hideDiagnostics" : "settings.harness.showDiagnostics")}
			</Button>
			{expandedDiagnostics[agentId] ? <div className="rounded-md border border-(--color-border-settings-input) bg-(--color-bg-settings-input) p-3 text-xs text-settings-muted">
				{job?.method ? <p><span className="font-medium text-settings-label">{t("settings.harness.method")}:</span> {job.method}</p> : null}
				{job?.expectedDestination ? <p className="break-all"><span className="font-medium text-settings-label">{t("settings.harness.expectedDestination")}:</span> {job.expectedDestination}</p> : null}
				{job?.error ? <p className="mt-2 whitespace-pre-wrap text-error">{job.error}</p> : null}
				{job?.output ? <pre className="settings-thin-scrollbar mt-2 max-h-40 overflow-auto overscroll-contain whitespace-pre-wrap break-words font-mono">{job.output}</pre> : null}
				<Button type="button" className="mt-2" size="sm" variant="outline" onClick={() => job && void copyDiagnostics(agentId, job)}><Copy aria-hidden="true" />{t("settings.harness.copyDiagnostics")}</Button>
			</div> : null}
		</div>;
	};

	const accountTab = (details: HarnessDetails) => {
		const { agentId, plan, isInstalled, authPlan, rowAuthWorkflow, authBusy } = details;
		const methodLabel = details.maintenanceMethod?.label;
		const showAccount = isInstalled;
		return <div className="flex flex-col gap-5">
			<HarnessDetailGroup title={t("settings.harness.installation")}>
				<HarnessDetailRow
					label={isInstalled ? t(details.versionUnverified ? "settings.harness.lastObservedVersion" : "settings.harness.installedVersion")
						: details.installationPending ? t("settings.harness.installationUnknown") : t("settings.harness.notInstalled")}
					description={isInstalled ? (methodLabel ? t("settings.harness.installedWith", { method: methodLabel }) : undefined) : details.installNote}
				>
					{isInstalled ? <span className="break-all font-mono text-xs text-settings-label">{details.versionText}{details.versionUnverified && details.currentVersion ? ` · ${t("settings.harness.unverified")}` : ""}</span> : null}
					{isInstalled && details.updateAvailable ? <span className="whitespace-nowrap text-xs font-medium text-settings-accent">{t("settings.harness.versionAvailable", { version: versionLabel(details.advisory!.latestVersion!) })}</span> : null}
					{isInstalled && !details.updateUnknown && !details.updateAvailable && !details.busy ? <span className="text-xs text-settings-muted">{t("settings.harness.latest")}</span> : null}
					{details.installationAction}
				</HarnessDetailRow>
				{isInstalled && details.updatedAt ? <HarnessDetailRow label={t("settings.harness.lastUpdated")}>
					<span className="text-xs text-settings-label">{formatTimestamp(details.updatedAt)}</span>
				</HarnessDetailRow> : null}
			</HarnessDetailGroup>
			{isInstalled && details.showUpdate && !details.canUpdate && details.updateReason !== details.uninstallReason ? <p className="-mt-3 text-xs text-settings-muted">{details.updateReason}</p> : null}
			{isInstalled && details.updateUnknown && !details.advisoryQuery?.isPending && !details.manualOnly ? <p className="-mt-3 text-xs text-settings-muted">{t("settings.harness.updateCheckUnavailable")}</p> : null}

			{showAccount || rowAuthWorkflow ? <HarnessDetailGroup title={t("settings.harness.account")}>
				<HarnessDetailRow label={authBusy && rowAuthWorkflow ? t(rowAuthWorkflow.action === "logout" ? "settings.harness.logoutInProgress" : "settings.harness.loginInProgress") : details.authSummary}>
					{details.accountAction}
					{isInstalled && authPlan?.logoutCommand && authPlan.available && details.authStatus !== "unauthorized" ? <Button type="button" size="sm" variant="outline" disabled={details.busy || Boolean(authWorkflow)} onClick={() => setLogoutRequest(agentId)}><LogOut aria-hidden="true" />{t("settings.harness.logout")}</Button> : null}
				</HarnessDetailRow>
			</HarnessDetailGroup> : null}
			{authPlan?.action === "instructions" && authPlan.documentationUrl ? <div><Button type="button" size="sm" variant="outline" onClick={() => void aoBridge.app.openExternal(authPlan.documentationUrl)}><BookOpen aria-hidden="true" />{t("settings.harness.instructions")}</Button></div> : null}
			{rowAuthWorkflow ? <HarnessAuthTerminalPanel
				workflow={rowAuthWorkflow}
				hostId={hostId}
				onClose={() => void closeAuth(rowAuthWorkflow)}
				onRetry={() => void closeAuth(rowAuthWorkflow).then((closed) => { if (closed) { if (rowAuthWorkflow.action === "logout") setLogoutRequest(agentId); else void startAuth(agentId); } })}
				onTerminalState={(state) => {
					if (state === "exited" && authWorkflowRef.current?.phase === "running") void finishAuth(rowAuthWorkflow);
				}}
			/> : null}

			{details.rowError && !operationRequest ? <p role="alert" className="text-xs text-error">{details.rowError}</p> : null}
			{details.jobFailed && !authBusy ? <div><Button type="button" size="sm" variant="outline" disabled={details.busy} onClick={() => void verifyInstall(agentId)}>{t("settings.harness.verifyAgain")}</Button></div> : null}
			{diagnosticsPanel(details)}

			{isInstalled && !authBusy && !details.busy ? <HarnessDetailGroup title={t("settings.harness.dangerZone")}>
				<HarnessDetailRow
					label={t("settings.harness.uninstallAgent", { agent: agentLabel(agentId) })}
					description={details.canUninstall && methodLabel ? t("settings.harness.uninstallWith", { method: methodLabel }) : undefined}
				>
					<Button type="button" size="sm" variant="outline" className="h-8 border-error/40 px-3 text-error hover:border-error/60 hover:bg-error/10 hover:text-error focus-visible:ring-2 focus-visible:ring-error" disabled={!details.canUninstall} aria-describedby={!details.canUninstall ? `harness-uninstall-reason-${agentId}` : undefined} onClick={() => requestInstalledOperation(agentId, "uninstall")}>
						<Trash2 aria-hidden="true" />{t(details.failed && details.currentOperation === "uninstall" ? "settings.harness.retryUninstall" : "settings.harness.uninstall")}
					</Button>
				</HarnessDetailRow>
			</HarnessDetailGroup> : null}
			{isInstalled && !authBusy && !details.busy && !details.canUninstall ? (plan?.uninstallGuide && (!details.methodId || details.methodId === "official-installer")
				? <div id={`harness-uninstall-reason-${agentId}`} className="-mt-3"><HarnessUninstallGuide agent={agentLabel(agentId)} guide={plan.uninstallGuide} fallbackDocsUrl={plan.documentationUrl} /></div>
				: <p id={`harness-uninstall-reason-${agentId}`} className="-mt-3 text-xs text-settings-muted">{details.uninstallReason}</p>) : null}
		</div>;
	};

	const healthText = (details: HarnessDetails) => {
		const { agentId, readinessAgent, advisory } = details;
		return [
			`${agentLabel(agentId)} health`,
			`Installation: ${readinessAgent?.installation.state ?? "unknown"}${readinessAgent?.installation.checkedAt ? ` (checked ${readinessAgent.installation.checkedAt})` : ""}`,
			readinessAgent?.installation.reason ? `Installation reason: ${readinessAgent.installation.reason}` : "",
			`Authentication: ${readinessAgent?.authentication.state ?? "unknown"}`,
			readinessAgent?.authentication.reason ? `Authentication reason: ${readinessAgent.authentication.reason}` : "",
			`Installed version: ${details.currentVersion ?? "unknown"}`,
			`Latest version: ${advisory?.latestVersion ?? "unknown"}`,
			advisory?.status ? `Update status: ${advisory.status}${advisory.reason ? ` (${advisory.reason})` : ""}` : "",
			`Maintenance method: ${details.methodId || "unverified"}`,
			details.advisory?.binaryPath ? `Executable: ${details.advisory.binaryPath}` : "",
		].filter(Boolean).join("\n");
	};

	const healthTab = (details: HarnessDetails) => {
		const { agentId, readinessAgent, advisory, isInstalled } = details;
		const installation = readinessAgent?.installation;
		const installState: { tone: HarnessTone; label: string } = installation?.state === "installed" ? { tone: "ok", label: t("settings.harness.installed") }
			: installation?.state === "not_installed" ? { tone: "off", label: t("settings.harness.notInstalled") }
			: { tone: "off", label: t("settings.harness.installationUnknown") };
		const authState = details.authStatus === "authorized" || details.authStatus === "configured" || details.authStatus === "not_applicable" ? "ok"
			: details.authStatus === "unauthorized" ? "error" : "off";
		const checking = agents.isFetching || Boolean(details.advisoryQuery?.isFetching);
		// Only facts AO has observed: a row with nothing to report is left out rather than filled with a placeholder.
		const lastChecked = formatTimestamp(installation?.checkedAt);
		const updateChecked = formatTimestamp(advisory?.checkedAt);
		const executable = advisory?.binaryPath || details.job?.expectedDestination;
		const hasDetails = Boolean(executable || (isInstalled && details.currentVersion) || advisory?.latestVersion || details.maintenanceMethod || updateChecked);
		return <div className="flex flex-col gap-5">
			<HarnessDetailGroup title={t("settings.harness.health.status")}>
				<HarnessDetailRow label={t("settings.harness.installation")} description={installation?.state !== "installed" ? installation?.reason || undefined : undefined}>
					<HarnessStateText tone={installState.tone}>{installState.label}</HarnessStateText>
				</HarnessDetailRow>
				{isInstalled ? <HarnessDetailRow label={t("settings.harness.health.authentication")} description={authState === "error" ? readinessAgent?.authentication.reason || undefined : undefined}>
					<HarnessStateText tone={authState}>{details.authSummary}</HarnessStateText>
				</HarnessDetailRow> : null}
				{lastChecked ? <HarnessDetailRow label={t("settings.harness.health.lastChecked")}>
					<span className="text-xs text-settings-label">{lastChecked}{installation?.freshness === "stale" ? ` · ${t("settings.harness.health.stale")}` : ""}</span>
				</HarnessDetailRow> : null}
			</HarnessDetailGroup>
			{hasDetails ? <HarnessDetailGroup title={t("settings.harness.health.details")}>
				{executable ? <HarnessDetailRow label={t("settings.harness.health.executable")}>
					<span className="min-w-0 break-all font-mono text-xs text-settings-label">{executable}</span>
				</HarnessDetailRow> : null}
				{isInstalled && details.currentVersion ? <HarnessDetailRow label={t(details.versionUnverified ? "settings.harness.lastObservedVersion" : "settings.harness.installedVersion")}>
					<span className="break-all font-mono text-xs text-settings-label">{details.versionText}</span>
				</HarnessDetailRow> : null}
				{advisory?.latestVersion ? <HarnessDetailRow label={t("settings.harness.health.latestVersion")}>
					<span className="break-all font-mono text-xs text-settings-label">{versionLabel(advisory.latestVersion)}</span>
					{details.updateAvailable ? <span className="whitespace-nowrap text-xs font-medium text-settings-accent">{t("settings.harness.updateAvailable")}</span> : null}
				</HarnessDetailRow> : null}
				{details.maintenanceMethod ? <HarnessDetailRow label={t("settings.harness.installMethod")}>
					<span className="text-xs text-settings-label">{details.maintenanceMethod.label}</span>
				</HarnessDetailRow> : null}
				{updateChecked ? <HarnessDetailRow label={t("settings.harness.health.updateChecked")}>
					<span className="text-xs text-settings-label">{updateChecked}</span>
				</HarnessDetailRow> : null}
			</HarnessDetailGroup> : null}
			<div className="flex flex-wrap gap-2">
				<Button type="button" size="sm" variant="outline" disabled={checking || details.busy} onClick={() => refreshInstalledAgent(agentId)}>
					{checking ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <RefreshCw aria-hidden="true" />}
					{t(checking ? "settings.harness.health.checking" : "settings.harness.health.checkAgain")}
				</Button>
				<Button type="button" size="sm" variant="ghost" onClick={() => void (async () => {
					const machine = await machineText(queryClient, hostId ? client : undefined);
					await copyText(agentId, [healthText(details), machine].filter(Boolean).join("\n\n"));
				})()}>
					{copiedAgent === agentId ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}{t(copiedAgent === agentId ? "settings.harness.copied" : "settings.harness.copyDiagnostics")}
				</Button>
			</div>
			<p className="-mt-2 text-xs text-settings-muted">{t("settings.harness.health.hint")}</p>
		</div>;
	};

	const selectedDetails = selectedAgentId ? describe(selectedAgentId) : null;

	return (
		<>
			{installers.error || authPlans.error || agents.error || jobs.error ? (
				<div className="flex items-center gap-2 rounded-md border border-error/30 bg-error/10 px-3 py-2 text-xs text-error">
					<TriangleAlert className="size-4" aria-hidden="true" />
					{jobs.error instanceof Error ? jobs.error.message : t("settings.harness.loadFailed")}
				</div>
			) : null}

			<div ref={layoutRef} className={cn("w-full", split && "grid grid-cols-[minmax(13rem,15rem)_minmax(0,1fr)] items-start gap-6")}>
				{split || !selectedDetails ? (
					<div className={cn("flex w-full flex-col", split ? "sticky top-14 max-h-[calc(100dvh-10rem)] gap-0.5 overflow-y-auto overscroll-contain" : "settings-grouped-rows")}>
						{rows.map((agentId) => {
							const details = describe(agentId);
							const selected = split && selectedAgentId === agentId;
							return <button
								type="button"
								aria-label={t("settings.harness.openDetails", { agent: agentLabel(agentId) })}
								aria-current={selected ? "true" : undefined}
								className={cn(
									"flex w-full items-center gap-3 text-left outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
									split ? "h-9 rounded-md px-2 hover:bg-accent" : "settings-row-bar min-h-12 hover:bg-(--color-bg-settings-row-hover)",
									selected && "bg-accent",
								)}
								data-agent={agentId}
								key={agentId}
								title={details.statusLabel || undefined}
								onClick={() => openDetail(agentId)}
							>
								<AgentAvatar className={cn("shrink-0", split ? "size-5" : "size-6")} decorative provider={agentId} />
								<span className="min-w-0 flex-1 truncate text-sm text-settings-label">{agentLabel(agentId)}</span>
								{details.isInstalled && details.updateAvailable ? <ArrowUpCircle
									className="size-3.5 shrink-0 text-settings-accent"
									aria-label={t("settings.harness.versionAvailable", { version: versionLabel(details.advisory!.latestVersion!) })}
									role="img"
								/> : null}
								{details.busy ? <span role="status" className="flex shrink-0 items-center"><LoaderCircle className="size-3.5 animate-spin text-settings-muted" aria-hidden="true" /><span className="sr-only">{details.progressLabel}</span></span>
									: details.authBusy ? <LoaderCircle className="size-3.5 shrink-0 animate-spin text-settings-muted" aria-hidden="true" />
									: <HarnessStatusDot tone={details.listTone} />}
								{split ? null : <ChevronRight className="size-4 shrink-0 text-settings-muted" aria-hidden="true" />}
							</button>;
						})}
						{rows.length === 0 ? <p className="px-3 py-6 text-center text-sm text-settings-muted">{t("settings.harness.noResults")}</p> : null}
					</div>
				) : null}
				{selectedDetails ? (
					<div
						className="flex w-full min-w-0 flex-col gap-4"
						data-agent={selectedDetails.agentId}
						data-focus-highlighted={highlightedAgentId === selectedDetails.agentId ? "" : undefined}
						ref={detailRef}
					>
						{split ? null : <div>
							<Button type="button" size="sm" variant="ghost" className="-ml-2 h-7 gap-1 px-2 text-settings-muted" onClick={closeDetail}>
								<ChevronLeft aria-hidden="true" />{t("settings.harness.allHarnesses")}
							</Button>
						</div>}
						<div
							className={cn("-mx-2 flex items-center gap-3 rounded-lg px-2 py-1 transition-[background-color,box-shadow] duration-200", highlightedAgentId === selectedDetails.agentId && "bg-accent-weak ring-2 ring-inset ring-accent")}
						>
							<AgentAvatar className="size-8 shrink-0" decorative provider={selectedDetails.agentId} />
							<span className="min-w-0 flex-1 text-base font-semibold text-settings-label" id={`harness-agent-${selectedDetails.agentId}`}>{agentLabel(selectedDetails.agentId)}</span>
						</div>
						<Tabs value={detailTab} onValueChange={(value) => setDetailTab(value as HarnessDetailTab)}>
							<TabsList aria-label={t("settings.harness.detailTabs", { agent: agentLabel(selectedDetails.agentId) })}>
								<TabsTrigger value="account">{t("settings.harness.tabAccount")}</TabsTrigger>
								<TabsTrigger value="models">{t("settings.harness.tabModels")}</TabsTrigger>
								<TabsTrigger value="health">{t("settings.harness.tabHealth")}</TabsTrigger>
							</TabsList>
							<TabsContent value="account" className="pt-4">{accountTab(selectedDetails)}</TabsContent>
							<TabsContent value="models" className="pt-4">
								<HarnessModelsPanel
									agentId={selectedDetails.agentId}
									hostId={hostId}
									installed={selectedDetails.isInstalled}
									needsLogin={needsAuthentication(selectedDetails.agentId)}
									onOpenAccount={() => setDetailTab("account")}
								/>
							</TabsContent>
							<TabsContent value="health" className="pt-4">{healthTab(selectedDetails)}</TabsContent>
						</Tabs>
					</div>
				) : null}
			</div>
			<ConfirmDialog
				open={logoutRequest !== null}
				title={t("settings.harness.logoutConfirmTitle", { agent: logoutRequest ? agentLabel(logoutRequest) : "" })}
				description={t("settings.harness.logoutConfirmDescription", { host: hostId ? labelForHost(hostId) ?? hostId : t("settings.harness.thisComputer") })}
				confirmLabel={t("settings.harness.logout")}
				destructive
				busy={logoutRequest ? authStates[logoutRequest]?.pending : false}
				confirmDisabled={!logoutRequest || !agentAuthPlans.get(logoutRequest)?.available || !agentAuthPlans.get(logoutRequest)?.logoutCommand || Boolean(authWorkflow) || (logoutRequest ? pendingAgentIds.has(logoutRequest) || isActive(jobMap.get(logoutRequest)) : false)}
				error={logoutRequest ? authStates[logoutRequest]?.error : null}
				onConfirm={() => { if (logoutRequest) void startAuth(logoutRequest, "logout").then((started) => { if (started) setLogoutRequest(null); }); }}
				onOpenChange={(open) => { if (!open && !authStartPendingRef.current) setLogoutRequest(null); }}
			/>
			<ConfirmDialog
				open={operationRequest !== null}
				title={t("settings.harness.uninstallConfirmTitle", { agent: operationRequest ? agentLabel(operationRequest.agentId) : "" })}
				description={null}
				confirmLabel={t("settings.harness.uninstall")}
				destructive
				busy={requestBusy}
				confirmDisabled={!canConfirmOperation}
				error={operationRequest ? actionErrors[operationRequest.agentId] : null}
				onConfirm={() => {
					if (!operationRequest || !canConfirmOperation) return;
					void startAgentOperation(operationRequest.agentId, operationRequest.method, "uninstall").then((started) => { if (started) setOperationRequest(null); });
				}}
				onOpenChange={(open) => { if (!open && !requestBusy) setOperationRequest(null); }}
			/>
		</>
	);
}

type HarnessDetailTab = "account" | "models" | "health";
type HarnessTone = "ok" | "error" | "off";

const SPLIT_LAYOUT_MIN_WIDTH = 720;

/** True once the element is wide enough to show the harness list beside the open harness. */
function useWideLayout(ref: { current: HTMLElement | null }): boolean {
	const [wide, setWide] = useState(false);
	useEffect(() => {
		const element = ref.current;
		if (!element || typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(([entry]) => setWide((entry?.contentRect.width ?? 0) >= SPLIT_LAYOUT_MIN_WIDTH));
		observer.observe(element);
		return () => observer.disconnect();
	}, [ref]);
	return wide;
}

function HarnessStatusDot({ tone }: { tone: HarnessTone }) {
	return <span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full", tone === "ok" ? "bg-success" : tone === "error" ? "bg-error" : "bg-settings-muted/50")} />;
}

function HarnessDetailGroup({ title, children }: { title: string; children: ReactNode }) {
	return <section className="flex flex-col gap-1.5">
		<h3 className="text-xs font-medium leading-4 text-settings-muted">{title}</h3>
		<div className="settings-grouped-rows flex w-full flex-col">{children}</div>
	</section>;
}

function HarnessDetailRow({ label, description, children }: { label: string; description?: string; children?: ReactNode }) {
	return <div className="settings-row-bar min-h-12 flex-wrap gap-x-4 gap-y-1">
		<span className="min-w-0 flex-1">
			<span className="block text-sm leading-5 text-settings-label">{label}</span>
			{description ? <span className="mt-0.5 block text-pretty text-xs leading-4 text-settings-muted">{description}</span> : null}
		</span>
		<span className="flex min-w-0 flex-wrap items-center justify-end gap-x-3 gap-y-1">{children}</span>
	</div>;
}

function HarnessStateText({ tone, children }: { tone: HarnessTone; children: ReactNode }) {
	return <span className="inline-flex items-center gap-2 text-xs text-settings-label">
		<span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full", tone === "ok" ? "bg-success" : tone === "error" ? "bg-error" : "bg-settings-muted/50")} />
		{children}
	</span>;
}

/** The harness catalog and machine defaults for future sessions. */
function HarnessModelsPanel({ agentId, hostId, installed, needsLogin, onOpenAccount }: {
	agentId: AgentId;
	hostId?: string;
	installed: boolean;
	needsLogin: boolean;
	onOpenAccount: () => void;
}) {
	const { i18n, t } = useTranslation();
	const queryClient = useQueryClient();
	const options = agentModelsQueryOptions(agentId, "", hostId);
	const catalog = useQuery({ ...options, enabled: installed && !needsLogin });
	const [refreshing, setRefreshing] = useState(false);
	const [refreshError, setRefreshError] = useState<string | null>(null);
	const agent = agentLabel(agentId);
	if (!installed || needsLogin) {
		return <div className="space-y-3">
			<HarnessModelDefaults key={`${hostId ?? LOCAL_HOST}:${agentId}`} agentId={agentId} agentLabel={agent} hostId={hostId} />
			<div className="flex flex-col items-center gap-3 px-3 py-8 text-center">
			<p className="text-sm text-settings-muted">{t(installed ? "settings.harness.models.needsLogin" : "settings.harness.models.notInstalled", { agent })}</p>
			<Button type="button" size="sm" variant="outline" onClick={onOpenAccount}>{t("settings.harness.tabAccount")}</Button>
		</div></div>;
	}
	const refresh = async () => {
		setRefreshing(true);
		setRefreshError(null);
		try {
			queryClient.setQueryData(options.queryKey, await refreshAgentModels(agentId, "", hostId));
		} catch (error) {
			setRefreshError(error instanceof Error ? error.message : t("settings.harness.models.loadFailed"));
		} finally {
			setRefreshing(false);
		}
	};
	const data = catalog.data;
	const updated = data?.lastSuccessAt ?? data?.fetchedAt;
	const busy = refreshing || data?.refreshState === "refreshing" || data?.refreshState === "queued";
	const error = refreshError ?? (catalog.error instanceof Error ? catalog.error.message : undefined) ?? data?.refreshError;
	return <div className="flex flex-col gap-3">
		<HarnessModelDefaults key={`${hostId ?? LOCAL_HOST}:${agentId}`} agentId={agentId} agentLabel={agent} hostId={hostId} catalog={data} />
		<div className="flex items-center justify-between gap-3">
			<h3 className="text-xs font-medium leading-4 text-settings-muted">{t("settings.harness.models.title")}</h3>
			<span className="flex items-center gap-2 text-xs text-settings-muted">
				{updated && Number.isFinite(Date.parse(updated)) ? <span>{t("settings.harness.models.updated", { time: new Date(updated).toLocaleString(i18n.resolvedLanguage, { day: "numeric", month: "short", hour: "numeric", minute: "2-digit" }) })}</span> : null}
				<Button type="button" size="sm" variant="ghost" className="h-7 px-2" disabled={busy || catalog.isPending} onClick={() => void refresh()}>
					{busy ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <RefreshCw aria-hidden="true" />}
					{t(busy ? "settings.harness.models.refreshing" : "settings.harness.models.refresh")}
				</Button>
			</span>
		</div>
		{error ? <p role="alert" className="text-xs text-error">{error}</p> : null}
		{catalog.isPending ? <p className="flex items-center gap-2 px-3 py-6 text-sm text-settings-muted"><LoaderCircle className="size-4 animate-spin" aria-hidden="true" />{t("settings.harness.models.loading")}</p>
			: !data?.models.length ? <p className="px-3 py-6 text-center text-sm text-settings-muted">{t("settings.harness.models.empty")}</p>
			: <ul className="settings-grouped-rows flex w-full flex-col" aria-label={t("settings.harness.models.title")}>
				{data.models.map((model) => <li key={model.id} className="settings-row-bar min-h-12 gap-4">
					<span className="min-w-0 flex-1">
						<span className="flex flex-wrap items-center gap-2 text-sm leading-5 text-settings-label">
							{model.label}
							{model.isDefault ? <span className="rounded-sm bg-(--color-bg-settings-input) px-1.5 py-0.5 text-[11px] text-settings-muted">{t("settings.harness.models.default")}</span> : null}
						</span>
						{model.label !== model.id ? <span className="block break-all font-mono text-[11px] text-settings-muted">{model.id}</span> : null}
						{/* Only facts the harness reports; nothing is shown in place of a missing value. */}
						{model.contextWindow || model.inputs?.length ? <span className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-xs text-settings-muted">
							{model.contextWindow ? <span>{t("settings.harness.models.context")}: {new Intl.NumberFormat(i18n.resolvedLanguage).format(model.contextWindow)}</span> : null}
							{model.inputs?.length ? <span>{t("settings.harness.models.inputs")}: {model.inputs.map((input) => input === "text" ? t("settings.harness.models.inputText") : input === "image" ? t("settings.harness.models.inputImage") : input).join(" · ")}</span> : null}
						</span> : null}
					</span>
					<span className="shrink-0 text-right text-xs text-settings-muted">
						{model.efforts?.length ? t("settings.harness.models.efforts", { levels: model.efforts.join(" · ") }) : t("settings.harness.models.noEffort")}
					</span>
				</li>)}
			</ul>}
		<p className="text-xs text-settings-muted">{t("settings.harness.models.hint")}</p>
	</div>;
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

function HarnessAuthTerminalPanel({ workflow, hostId, onClose, onRetry, onTerminalState }: {
	workflow: AuthTerminalWorkflow;
	hostId?: string;
	onClose: () => void;
	onRetry: () => void;
	onTerminalState: (state: TerminalSessionState) => void;
}) {
	const { t } = useTranslation();
	const theme = useResolvedTheme();
	const shell = useShellMaybe();
	// Logout can exit before attachment. Own its mux so a shell-list refresh
	// cannot prune the retained cache entry before we receive the exit event.
	const createMux = useCallback(() => {
		const base = hostId ? baseUrlForHost(hostId) : getApiBaseUrl();
		if (hostId && !base) throw new Error("Remote host disconnected");
		return createTerminalMux(muxUrlFromApiBase(base ?? ""));
	}, [hostId]);
	const panelRef = useRef<HTMLDivElement>(null);
	const inputRequestIdRef = useRef(0);
	const activeInputRequestIdRef = useRef<number | null>(null);
	const [terminalState, setTerminalState] = useState<TerminalSessionState>("connecting");
	const [inputRequest, setInputRequest] = useState<{ id: number; data: string }>();
	const [commandPending, setCommandPending] = useState(false);
	const [commandSent, setCommandSent] = useState(false);
	const handlerRef = useRef(onTerminalState);
	handlerRef.current = onTerminalState;
	const handleTerminalState = useCallback((state: TerminalSessionState) => {
		setTerminalState(state);
		handlerRef.current(state);
	}, []);
	useEffect(() => {
		panelRef.current?.scrollIntoView({ behavior: "smooth", block: "nearest" });
	}, [workflow.terminal.handleId]);
	// While the login runs, the terminal speaks for itself. Guidance is shown only
	// when it tells the user to act outside the terminal (the Open login button).
	const status = workflow.phase === "running"
		? workflow.terminalInput ? workflow.guidance : ""
		: workflow.phase === "verifying" ? t(workflow.action === "logout" ? "settings.harness.checkingLogout" : "settings.harness.checkingLogin")
			: workflow.phase === "closing" ? t("settings.harness.authClosing")
				: workflow.reason ?? t("settings.harness.loginUnknown");
	const retryable = workflow.phase === "unauthorized" || workflow.phase === "unverified" || workflow.phase === "timed_out" || workflow.phase === "cleanup_failed";
	const openAuthAction = () => {
		if (!workflow.terminalInput || terminalState !== "attached" || commandPending || commandSent) return;
		inputRequestIdRef.current += 1;
		activeInputRequestIdRef.current = inputRequestIdRef.current;
		setCommandPending(true);
		setInputRequest({ id: inputRequestIdRef.current, data: workflow.terminalInput });
	};
	const handleInputRequestResult = useCallback((id: number, accepted: boolean) => {
		if (activeInputRequestIdRef.current !== id) return;
		activeInputRequestIdRef.current = null;
		setInputRequest(undefined);
		setCommandPending(false);
		if (accepted) setCommandSent(true);
	}, []);
	return (
		<div ref={panelRef} className="mt-1 scroll-my-3 overflow-hidden rounded-md border border-(--color-border-settings-input) bg-terminal" data-testid="harness-auth-terminal">
			<div className="flex min-h-10 items-center justify-between gap-3 border-b border-(--color-border-settings-input) bg-surface/90 px-3 py-2">
				<div className="min-w-0"><p className="truncate text-xs font-medium text-settings-label">{workflow.terminal.title}</p>{status ? <p className="truncate text-[11px] text-settings-muted" aria-live="polite" role="status">{status}</p> : null}</div>
				<div className="flex shrink-0 items-center gap-2">
					{workflow.terminalInput && workflow.phase === "running" ? <Button type="button" size="sm" variant="outline" disabled={terminalState !== "attached" || commandPending || commandSent} onClick={openAuthAction}>{commandSent ? <Check aria-hidden="true" /> : <LogIn aria-hidden="true" />}{workflow.action === "setup" ? commandSent ? t("settings.harness.setupOpened") : t("settings.harness.openSetup") : commandSent ? t("settings.harness.loginOpened") : t("settings.harness.openLogin")}</Button> : null}
					<button type="button" aria-label={t("settings.close")} className="grid size-7 place-items-center rounded text-settings-muted hover:bg-interactive-hover" disabled={workflow.phase === "closing" || workflow.phase === "verifying"} onClick={onClose}><X className="size-4" aria-hidden="true" /></button>
				</div>
			</div>
			{/* Full-screen setup TUIs need enough rows for their provider pickers. */}
			<div className="h-[min(600px,75vh)] min-h-0" data-settings-inline-edit=""><TerminalPane createMux={hostId || workflow.action === "logout" ? createMux : undefined} daemonReady={hostId ? true : shell ? shell.daemonStatus.state === "ready" : true} focusRequested={workflow.phase === "running" && terminalState === "attached"} fontSize={12} inputRequest={inputRequest} onInputRequestResult={handleInputRequestResult} onTerminalStateChange={handleTerminalState} terminalTarget={{ kind: "shell", handleId: workflow.terminal.handleId, generation: workflow.terminal.createdAt, title: workflow.terminal.title }} theme={theme} /></div>
			{retryable ? <div className="flex items-center justify-end border-t border-(--color-border-settings-input) bg-surface/90 px-3 py-2"><Button type="button" size="sm" variant="outline" onClick={workflow.phase === "cleanup_failed" ? onClose : onRetry}>{workflow.phase === "cleanup_failed" ? t("settings.harness.retry") : workflow.action === "logout" ? t("settings.harness.logout") : workflow.action === "setup" ? t("settings.harness.setup") : t("settings.harness.login")}</Button></div> : null}
		</div>
	);
}
