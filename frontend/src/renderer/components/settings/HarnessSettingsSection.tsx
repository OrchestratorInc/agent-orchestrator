import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { BookOpen, Check, Copy, KeyRound, LoaderCircle, LogIn, Search, Trash2, TriangleAlert, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import {
	agentReadinessQueryKeyForHost,
	cacheAgentReadiness,
	ensureAgentReadiness,
	useAgentReadinessQuery,
} from "../../hooks/useAgentReadinessQuery";
import { agentAuthPlansQueryKeyForHost, probeAgentAuth, useAgentAuthPlans, useStartAgentAuth } from "../../hooks/useAgentAuth";
import { agentModelsQueryPrefix } from "../../hooks/useAgentModelsQuery";
import { fetchSessionMemory, formatCPU, formatMemory, sessionMemoryQueryOptions } from "../../hooks/useSessionMemory";
import { closeShellTerminal, shellTerminalsQueryKeyForHost, type ShellTerminal } from "../../hooks/useShellTerminals";
import type { TerminalSessionState } from "../../hooks/useTerminalSession";
import { agentLabel, AGENT_OPTIONS, type AgentId } from "../../lib/agent-options";
import { CLOUD_AGENT_PROVIDERS, isCloudHarnessConnected } from "../../lib/cloud-agents";
import { useCloudCp } from "../../hooks/useCloudCp";
import { useCloudOrg } from "../../hooks/useCloudOrg";
import { providerConnectionsQueryKey, useProviderConnections } from "../../hooks/useProviderConnections";
import { GitHubTokenField } from "../onboarding/GitHubTokenField";
import { CloudHarnessLoginPanel, type CloudHarness } from "./CloudHarnessLoginPanel";
import { SettingsRow } from "./SettingsRow";
import { apiErrorCode, apiErrorMessage } from "../../lib/api-client";
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
import { Tabs, TabsList, TabsTrigger } from "../ui/tabs";
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
	titleHidden = false,
}: {
	focusAgentId?: string;
	hostId?: string;
	initialView?: HarnessView;
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
	return <SettingsSection title={t("settings.harness")} titleHidden={titleHidden} sectionId="harness">
		<div className="sticky top-0 z-10 flex items-center gap-2 bg-card pb-2">
			<label className="flex h-9! min-w-0 flex-1 items-center gap-2 rounded-md border border-(--color-border-settings-input) bg-(--color-bg-settings-input) px-3">
				<Search aria-hidden="true" className="size-4 shrink-0 text-settings-muted" />
				<span className="sr-only">{t("settings.harness.search")}</span>
				<input aria-label={t("settings.harness.search")} className="min-w-0 flex-1 bg-transparent text-sm text-settings-label outline-none placeholder:text-settings-muted" placeholder={t("settings.harness.searchPlaceholder")} value={search} onChange={(event) => setSearch(event.target.value)} />
			</label>
			{cloudEnabled ? <Tabs value={cloudView ? "cloud" : "local"} onValueChange={(value) => setView(value as HarnessView)}><TabsList aria-label={t("settings.harness.viewLabel")}><TabsTrigger value="local">{t("settings.harness.viewLocal")}</TabsTrigger><TabsTrigger value="cloud">{t("settings.harness.viewCloud")}</TabsTrigger></TabsList></Tabs> : null}
		</div>
		{!cloudView && (connected.length > 0 || remoteOffline) ? <SettingsOptionMenu
				aria-label={t("remote.host")}
				value={selectedHostId}
				options={[{ value: LOCAL_HOST, label: t("settings.harness.thisComputer") }, ...connected.map((id) => ({ value: id, label: labelForHost(id) ?? id })), ...(remoteOffline ? [{ value: selectedHostId, label: t("remote.hostLabel", { hostId: selectedHostId }) }] : [])]}
				onChange={setSelectedHostId}
				triggerClassName="w-fit max-w-full"
			/> : null}
		{!cloudView && selectedHostId !== LOCAL_HOST && !remoteOffline ? <p className="text-xs text-muted-foreground">{t("settings.harness.remoteBrowserAuthNote")}</p> : null}
		{cloudView ? <CloudHarnessContent focusAgentId={focusAgentId} search={search} /> : remoteOffline ? <p className="text-xs text-error" role="alert">{t("remote.hostOffline")}</p> : <LocalHarnessContent key={selectedHostId} focusAgentId={focusAgentId} hostId={selectedHostId === LOCAL_HOST ? undefined : selectedHostId} search={search} />}
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

function LocalHarnessContent({ focusAgentId, hostId, search }: { focusAgentId?: string; hostId?: string; search: string }) {
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
	const rowsRef = useRef<HTMLDivElement>(null);
	const focusHandledRef = useRef(false);
	const highlightTimerRef = useRef<number | null>(null);
	const [highlightedAgentId, setHighlightedAgentId] = useState<AgentId | null>(null);
	const [expandedAgentId, setExpandedAgentId] = useState<AgentId | null>(null);
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
					queryClient.invalidateQueries({ queryKey: hostId ? ["agent-models", hostId, agentId] : agentModelsQueryPrefix(agentId) }),
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
		const row = Array.from(rowsRef.current?.querySelectorAll<HTMLElement>("[data-agent]") ?? [])
			.find((candidate) => candidate.dataset.agent === targetAgentId);
		if (!row) return;

		focusHandledRef.current = true;
		row.scrollIntoView({ behavior: "smooth", block: "center" });
		const primaryAction = row.querySelector<HTMLElement>("[data-harness-primary-action]:not(:disabled)");
		(primaryAction ?? row).focus({ preventScroll: true });
		setHighlightedAgentId(targetAgentId);
		highlightTimerRef.current = window.setTimeout(() => setHighlightedAgentId(null), FOCUS_HIGHLIGHT_MS);
	}, [agents.isPending, authPlans.isPending, installers.isPending, jobs.isPending, targetAgentId]);

	useEffect(() => () => {
		if (highlightTimerRef.current !== null) window.clearTimeout(highlightTimerRef.current);
	}, []);

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
				if (!authWorkflowRef.current || authWorkflowRef.current.agentId === agentId) setExpandedAgentId(agentId);
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
		setExpandedAgentId((current) => current === agentId ? null : current);
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
				setExpandedAgentId(agentId);
				return false;
			}
			setOperationAttempts((current) => ({ ...current, [agentId]: { operation, startedAt: data.startedAt } }));
			updateJob(data);
			if (data.status === "succeeded") refreshInstalledAgent(agentId);
			return true;
		} catch (error) {
			setActionErrors((current) => ({ ...current, [agentId]: error instanceof Error ? error.message : t("settings.harness.startFailed") }));
			setExpandedAgentId(agentId);
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

	const startAuth = async (agentId: AgentId) => {
		if (authWorkflowRef.current || authStartPendingRef.current || pendingActions.current.has(agentId) || isActive(jobMap.get(agentId))) return;
		authStartPendingRef.current = true;
		updateAuthState(agentId, { pending: true, error: null });
		try {
			const plan = agentAuthPlans.get(agentId);
			if (plan?.launchMode === "documentation") {
				await aoBridge.app.openExternal(plan.documentationUrl);
				return;
			}
			const result = await startAgentAuth.mutateAsync(agentId);
			if (!mountedRef.current) {
				await closeAuthTerminal(result.terminal.handleId, hostId);
				queryClient.setQueryData<ShellTerminal[]>(shellKey, (current) => current?.filter((terminal) => terminal.handleId !== result.terminal.handleId));
				void queryClient.invalidateQueries({ queryKey: shellKey });
				return;
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
			setExpandedAgentId(agentId);
			void queryClient.invalidateQueries({ queryKey: shellKey });
		} catch (error) {
			if (mountedRef.current) updateAuthState(agentId, { error: error instanceof Error ? error.message : t("settings.harness.authFailed") });
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
		if (authWorkflowRef.current?.terminal.handleId !== workflow.terminal.handleId) return;
		setAuthWorkflow((current) => current?.terminal.handleId === workflow.terminal.handleId ? { ...current, phase: "verifying", reason: undefined } : current);
		// MiMo can confirm a stored provider key locally without validating it upstream.
		const loggedIn = (candidate: AgentAuthProbeResult | undefined) =>
			candidate?.agent.authStatus === "authorized" || (workflow.agentId === "mimo-code" && candidate?.agent.authStatus === "configured");
		let result = await checkAuth(workflow.agentId, { fresh: true });
		for (let attempt = 1; attempt < AUTH_VERIFY_ATTEMPTS && !loggedIn(result); attempt++) {
			await new Promise((resolve) => window.setTimeout(resolve, AUTH_VERIFY_RETRY_MS));
			if (authWorkflowRef.current?.terminal.handleId !== workflow.terminal.handleId) return;
			result = await checkAuth(workflow.agentId, { fresh: true });
		}
		if (authWorkflowRef.current?.terminal.handleId !== workflow.terminal.handleId) return;
		if (loggedIn(result)) {
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
			phase: result?.agent.authStatus === "unauthorized" ? "unauthorized" : "unverified",
			reason: result?.agent.authStatus === "unauthorized" ? t("settings.harness.notLoggedIn") : t("settings.harness.loginUnknown"),
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
		if (readinessAgents.get(authWorkflow.agentId)?.authentication.state === "authorized") void closeAuth(authWorkflow);
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

	return (
		<>
			{installers.error || authPlans.error || agents.error || jobs.error ? (
				<div className="flex items-center gap-2 rounded-md border border-error/30 bg-error/10 px-3 py-2 text-xs text-error">
					<TriangleAlert className="size-4" aria-hidden="true" />
					{jobs.error instanceof Error ? jobs.error.message : t("settings.harness.loadFailed")}
				</div>
			) : null}

			<div className="settings-grouped-rows flex w-full flex-col" ref={rowsRef}>
			{rows.map((agentId) => {
				const plan = plans.get(agentId);
				const job = jobMap.get(agentId);
				const isInstalled = installed.has(agentId);
				const availableMethods = plan?.methods.filter((method) => method.available) ?? [];
				const installMethod = availableMethods.find((method) => method.recommended) ?? availableMethods[0];
				const methodId = operationMethodId(agentId);
				const maintenanceMethod = plan?.methods.find((method) => method.id === methodId);
				const canUpdate = supportsOperation(maintenanceMethod, "update");
				const canUninstall = supportsOperation(maintenanceMethod, "uninstall");
				const ownershipReason = !methodId ? t("settings.harness.ownershipUnknown") : undefined;
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
				const isExpanded = expandedAgentId === agentId && !busy;
				const readinessAgent = readinessAgents.get(agentId);
				const incompatibleVersionReason = readinessAgent?.installation.reasonCode === "install_incompatible_version"
					? readinessAgent.installation.reason : undefined;
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
				const showUpdate = updateAvailable;
				const hasDiagnostics = Boolean(job && jobFailed && (job.error || job.output || job.method || job.expectedDestination));
				const authSummary = authStatus === "configured" ? t("settings.harness.configured")
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
				const authProgress = rowAuthWorkflow?.phase === "verifying" ? t("settings.harness.checkingLogin")
					: rowAuthWorkflow?.phase === "closing" ? t("settings.harness.authClosing")
					: rowAuthWorkflow && rowAuthWorkflow.phase !== "running" ? rowAuthWorkflow.reason ?? t("settings.harness.loginUnknown")
					: t("settings.harness.loginInProgress");
				const statusLabel = isInstalled ? rowError ?? (authBusy ? authProgress : authSummary)
					: installationPending ? t("settings.harness.installationUnknown")
					: job?.status === "interrupted" ? t("settings.harness.interrupted")
					: rowError ?? incompatibleVersionReason ?? (!installMethod ? plan?.reason ?? t("settings.harness.manualRequired") : "");
				const versionText = currentVersion ? versionLabel(currentVersion) : t("settings.harness.versionUnavailable");
				const versionUnverified = advisoryQuery?.isError || job?.status === "verifying" || (currentOperation === "update" && (busy || failed));
				const updatedAt = lastUpdatedAt[agentId];
				const actionClass = "h-8 min-w-20 px-3 focus-visible:ring-2 focus-visible:ring-ring";
				const primaryAction = busy ? (
					<span role="status"><Button type="button" size="sm" variant="outline" className={actionClass} disabled><LoaderCircle className="animate-spin" aria-hidden="true" />{progressLabel}</Button></span>
				) : authBusy ? (
					<Button type="button" size="sm" className={actionClass} disabled>
						{rowAuthWorkflow && !["running", "verifying", "closing"].includes(rowAuthWorkflow.phase) ? null : <LoaderCircle className="animate-spin" aria-hidden="true" />}
						{rowAuthWorkflow?.phase === "verifying" ? t("settings.harness.checkingLogin") : isSetupAction ? t("settings.harness.setup") : t("settings.harness.login")}
					</Button>
				) : !isInstalled ? (
					!installationPending && installMethod ? <Button type="button" data-harness-primary-action="" size="sm" className={actionClass} onClick={() => void startAgentOperation(agentId, installMethod.id, "install")}>
						{failed ? t("settings.harness.retryInstall") : t("settings.harness.install")}
					</Button> : !installationPending && plan?.command ? <Button type="button" size="sm" variant="outline" className={actionClass} onClick={() => void copyText(agentId, plan.command!)}>
						{copiedAgent === agentId ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}{t(copiedAgent === agentId ? "settings.harness.copied" : "settings.harness.copyCommand")}
					</Button> : null
				) : needsLogin ? (
					<Button type="button" data-harness-primary-action="" data-terminal-focus-handoff="true" size="sm" className={actionClass} disabled={!authPlan?.available || Boolean(authWorkflow)} onClick={() => void startAuth(agentId)}>
						{t(isSetupAction ? "settings.harness.setup" : authState?.error ? "settings.harness.retryLogin" : "settings.harness.login")}
					</Button>
				) : showUpdate && !authPlans.isPending && !authPlans.isError ? (
					canUpdate ? <Button type="button" data-harness-primary-action="" size="sm" className={actionClass} onClick={() => requestInstalledOperation(agentId, "update")}>
						{t(failed && currentOperation === "update" ? "settings.harness.retryUpdate" : "settings.harness.update")}
					</Button> : plan?.documentationUrl ? <Button type="button" data-harness-primary-action="" size="sm" variant="outline" className={actionClass} title={updateReason} onClick={() => void aoBridge.app.openExternal(plan.documentationUrl)}>{t("settings.harness.manualUpdate")}</Button> : null
				) : null;
				const identity = <>
					<AgentAvatar className="size-7 shrink-0" decorative provider={agentId} />
					<span className="min-w-0 flex-1 text-left">
						<span className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
							<span className="text-sm font-medium text-settings-label" id={`harness-agent-${agentId}`}>{agentLabel(agentId)}</span>
							{isInstalled ? <span className="break-all font-mono text-[11px] text-settings-muted" title={t(versionUnverified ? "settings.harness.lastObservedVersion" : "settings.harness.installedVersion")}>{versionText}{versionUnverified && currentVersion ? ` · ${t("settings.harness.unverified")}` : ""}</span> : null}
						</span>
						{statusLabel || (isInstalled && updateAvailable) ? <span className="mt-0.5 flex flex-wrap items-baseline gap-x-1.5 text-xs text-settings-muted">
							{statusLabel ? <span className={cn("break-words", Boolean(rowError) && "text-error")}>{statusLabel}</span> : null}
							{isInstalled && updateAvailable ? <>
								{statusLabel ? <span aria-hidden="true">·</span> : null}
								<span className="font-medium text-settings-accent">{t("settings.harness.versionAvailable", { version: versionLabel(advisory!.latestVersion!) })}</span>
							</> : null}
						</span> : null}
					</span>
				</>;
				const diagnostics = hasDiagnostics ? <div className="space-y-2">
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
				</div> : null;
				return <div
					aria-labelledby={`harness-agent-${agentId}`}
					className={cn("settings-row-bar min-h-14 flex-wrap gap-3 transition-[background-color,box-shadow] duration-200", isExpanded && "harness-row-expanded", highlightedAgentId === agentId && "bg-accent-weak ring-2 ring-inset ring-accent")}
					data-agent={agentId}
					data-focus-highlighted={highlightedAgentId === agentId ? "" : undefined}
					key={agentId}
					tabIndex={-1}
				>
					{isInstalled ? <button
						type="button"
						className="flex min-w-0 flex-1 items-center gap-3 rounded-md text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
						aria-label={t(isExpanded ? "settings.harness.collapseOptions" : "settings.harness.expandOptions", { agent: agentLabel(agentId) })}
						aria-expanded={isExpanded}
						aria-controls={`harness-options-${agentId}`}
						disabled={busy}
						onClick={() => setExpandedAgentId(isExpanded ? null : agentId)}
					>{identity}</button> : <div className="flex min-w-0 flex-1 items-center gap-3">{identity}</div>}
					{primaryAction}
					{isInstalled || rowAuthWorkflow ? <div id={`harness-options-${agentId}`} hidden={!isExpanded} className={cn("basis-full space-y-3 pl-10", !isExpanded && "hidden")}>
						{!authBusy ? <>
							<div className="flex flex-wrap items-center gap-x-5 gap-y-3">
								<dl className="flex min-w-0 flex-1 flex-wrap items-start gap-x-6 gap-y-2 text-xs">
									<div><dt className="text-settings-muted">{t(versionUnverified ? "settings.harness.lastObservedVersion" : "settings.harness.installedVersion")}</dt><dd className="mt-1 break-all font-mono text-settings-label">{versionText}{versionUnverified && currentVersion ? ` · ${t("settings.harness.unverified")}` : ""}</dd></div>
									<div><dt className="text-settings-muted">{t("settings.harness.lastUpdated")}</dt><dd className="mt-1 text-settings-label">
										{updatedAt ? <time dateTime={updatedAt} title={new Date(updatedAt).toLocaleString(i18n.resolvedLanguage)}>{new Date(updatedAt).toLocaleDateString(i18n.resolvedLanguage, { day: "numeric", month: "short", year: "numeric" })}</time> : t("settings.harness.notRecorded")}
									</dd></div>
								</dl>
								<Button type="button" size="sm" variant="outline" className="h-8 border-error/40 px-3 text-error hover:border-error/60 hover:bg-error/10 hover:text-error focus-visible:ring-2 focus-visible:ring-error" disabled={!canUninstall} aria-describedby={!canUninstall ? `harness-uninstall-reason-${agentId}` : undefined} onClick={() => requestInstalledOperation(agentId, "uninstall")}>
									<Trash2 aria-hidden="true" />{t(failed && currentOperation === "uninstall" ? "settings.harness.retryUninstall" : "settings.harness.uninstall")}
								</Button>
							</div>
							{!canUninstall ? <p id={`harness-uninstall-reason-${agentId}`} className="text-xs text-settings-muted">{uninstallReason}</p> : null}
							{showUpdate && !canUpdate && updateReason !== uninstallReason ? <p className="text-xs text-settings-muted">{updateReason}</p> : null}
							{updateUnknown && !advisoryQuery?.isPending ? <p className="text-xs text-settings-muted">{t("settings.harness.updateCheckUnavailable")}</p> : null}
						</> : null}
						{rowError && !operationRequest ? <p role="alert" className="text-xs text-error">{rowError}</p> : null}
						{jobFailed && !authBusy ? <Button type="button" size="sm" variant="outline" disabled={busy} onClick={() => void verifyInstall(agentId)}>{t("settings.harness.verifyAgain")}</Button> : null}
						{authPlan?.action === "instructions" && authPlan.documentationUrl ? <Button type="button" size="sm" variant="outline" onClick={() => void aoBridge.app.openExternal(authPlan.documentationUrl)}><BookOpen aria-hidden="true" />{t("settings.harness.instructions")}</Button> : null}
						{diagnostics}
						{rowAuthWorkflow ? <HarnessAuthTerminalPanel
							workflow={rowAuthWorkflow}
							hostId={hostId}
							onClose={() => void closeAuth(rowAuthWorkflow)}
							onRetry={() => void closeAuth(rowAuthWorkflow).then((closed) => { if (closed) void startAuth(agentId); })}
							onTerminalState={(state) => {
								if (state === "exited" && authWorkflowRef.current?.phase === "running") void finishAuth(rowAuthWorkflow);
							}}
						/> : null}
					</div> : failed ? <div className="basis-full space-y-2 pl-10">
						<Button type="button" size="sm" variant="outline" disabled={busy} onClick={() => void verifyInstall(agentId)}>{t("settings.harness.verifyAgain")}</Button>
						{diagnostics}
					</div> : null}
				</div>;
			})}
				{rows.length === 0 ? <p className="px-3 py-6 text-center text-sm text-settings-muted">{t("settings.harness.noResults")}</p> : null}
			</div>
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
	const createMux = useCallback(() => {
		const base = hostId && baseUrlForHost(hostId);
		if (!base) throw new Error("Remote host disconnected");
		return createTerminalMux(muxUrlFromApiBase(base));
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
		: workflow.phase === "verifying" ? t("settings.harness.checkingLogin")
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
			<div className="h-[300px] min-h-0"><TerminalPane createMux={hostId ? createMux : undefined} daemonReady={hostId ? true : shell ? shell.daemonStatus.state === "ready" : true} focusRequested={workflow.phase === "running" && terminalState === "attached"} fontSize={12} inputRequest={inputRequest} onInputRequestResult={handleInputRequestResult} onTerminalStateChange={handleTerminalState} terminalTarget={{ kind: "shell", handleId: workflow.terminal.handleId, generation: workflow.terminal.createdAt, title: workflow.terminal.title }} theme={theme} /></div>
			{retryable ? <div className="flex items-center justify-end border-t border-(--color-border-settings-input) bg-surface/90 px-3 py-2"><Button type="button" size="sm" variant="outline" onClick={workflow.phase === "cleanup_failed" ? onClose : onRetry}>{workflow.phase === "cleanup_failed" ? t("settings.harness.retry") : workflow.action === "setup" ? t("settings.harness.setup") : t("settings.harness.login")}</Button></div> : null}
		</div>
	);
}
