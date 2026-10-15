import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, CircleArrowUp, LoaderCircle, TriangleAlert } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../../api/schema";
import { agentAuthPlansQueryKeyForHost, probeAgentAuth, useAgentAuthPlans, useStartAgentAuth } from "../../../hooks/useAgentAuth";
import { agentModelsQueryOptions, invalidateAgentModelCatalogs, refreshAgentModels } from "../../../hooks/useAgentModelsQuery";
import {
	agentReadinessQueryKeyForHost,
	cacheAgentReadiness,
	ensureAgentReadiness,
	useAgentReadinessQuery,
} from "../../../hooks/useAgentReadinessQuery";
import {
	installedAgentIds,
	installerQueryKeyForHost,
	installersQueryOptions,
	installJobsQueryKeyForHost,
	installJobsQueryOptions,
	isActiveInstallJob,
	isFailedInstallJob,
	maintenanceMethodId,
	needsHarnessLogin,
	updateAdvisoryQueryKey,
	upsertInstallJob,
	useHarnessUpdates,
	useStartHarnessUpdate,
	versionLabel,
	type AgentUpdateAdvisory,
	type InstallJob,
} from "../../../hooks/useHarnessUpdates";
import { shellTerminalsQueryKeyForHost, type ShellTerminal } from "../../../hooks/useShellTerminals";
import { AgentCheckError } from "../../../lib/agent-check-error";
import { agentLabel, AGENT_OPTIONS, type AgentId } from "../../../lib/agent-options";
import { apiErrorMessage } from "../../../lib/api-client";
import { aoBridge } from "../../../lib/bridge";
import { clientForSessionHost, labelForHost } from "../../../lib/host-clients";
import { LOCAL_HOST } from "../../../lib/hosts";
import { cn } from "../../../lib/utils";
import { AgentAvatar } from "../../AgentAvatar";
import { ConfirmDialog } from "../../ConfirmDialog";
import { Button } from "../../ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../../ui/tabs";
import { Tooltip, TooltipContent, TooltipTrigger } from "../../ui/tooltip";
import { describeHarness, supportsUpdate, type AgentAuthState, type AgentOperation, type HarnessDetails, type OperationAttempt } from "./describe-harness";
import { HarnessAccountTab } from "./HarnessAccountTab";
import { closeAuthTerminal, type AuthTerminalWorkflow } from "./HarnessAuthTerminalPanel";
import { diagnosticsText, healthText, machineText } from "./harness-diagnostics";
import { HarnessHealthTab } from "./HarnessHealthTab";
import { FOCUS_HIGHLIGHT_MS, HARNESS_TAB_CLASS, HARNESS_TABS_CLASS, HarnessStatusDot, useWideLayout, type HarnessDetailTab } from "./harness-ui";
import { HarnessModelsPanel, MULTI_PROVIDER_HARNESSES } from "./HarnessModelsPanel";

const AUTH_TERMINAL_LIFETIME_MS = 15 * 60_000;
// The first check right after a login terminal exits can fail transiently (the
// daemon's own readiness retry succeeds seconds later), so a login is re-checked
// a few times before the panel reports it did not take.
const AUTH_VERIFY_ATTEMPTS = 4;
const AUTH_VERIFY_RETRY_MS = 1_500;

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

export function LocalHarnessContent({ focusAgentId, hostId, search, onDetailChange, startLogin = false }: { focusAgentId?: string; hostId?: string; search: string; startLogin?: boolean; onDetailChange?: (open: boolean) => void }) {
	const { i18n, t } = useTranslation();
	const queryClient = useQueryClient();
	const client = clientForSessionHost(hostId);
	const readinessKey = useMemo(() => agentReadinessQueryKeyForHost(hostId), [hostId]);
	const installerKey = useMemo(() => installerQueryKeyForHost(hostId), [hostId]);
	const jobsKey = useMemo(() => installJobsQueryKeyForHost(hostId), [hostId]);
	const authPlansKey = useMemo(() => agentAuthPlansQueryKeyForHost(hostId), [hostId]);
	const shellKey = useMemo(() => shellTerminalsQueryKeyForHost(hostId), [hostId]);
	const agents = useAgentReadinessQuery(true, hostId);
	const installers = useQuery(installersQueryOptions(hostId));
	const jobs = useQuery(installJobsQueryOptions(hostId));
	const startHarnessUpdate = useStartHarnessUpdate(hostId);
	const authPlans = useAgentAuthPlans(hostId);
	const startAgentAuth = useStartAgentAuth(hostId);
	const startAgentLogout = useStartAgentAuth(hostId, "logout");
	const [logoutRequest, setLogoutRequest] = useState<AgentId | null>(null);
	const [authStates, setAuthStates] = useState<AgentAuthStates>({});
	const [checkingAuth, setCheckingAuth] = useState<AgentId[]>([]);
	const [authCheckErrors, setAuthCheckErrors] = useState<Partial<Record<AgentId, string>>>({});
	const [actionErrors, setActionErrors] = useState<Partial<Record<AgentId, string>>>({});
	const [selectedMethods, setSelectedMethods] = useState<Partial<Record<AgentId, string>>>({});
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
	// Opening a page refreshes its models once readiness says it is installed.
	const modelsRefreshPending = useRef<AgentId | null>(null);
	const openDetail = (agentId: AgentId, tab: HarnessDetailTab = "account") => {
		selectedAgentRef.current = agentId;
		setSelectedAgentId(agentId);
		setDetailTab(tab);
		modelsRefreshPending.current = agentId;
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
	// Operation identity is local to jobs started here; restored jobs use neutral copy.
	const [operationAttempts, setOperationAttempts] = useState<Partial<Record<AgentId, OperationAttempt>>>({});
	// The API has no durable update history. Only record verified updates we
	// actually observed here; a restored job/check timestamp is not an update date.
	const [lastUpdatedAt, setLastUpdatedAt] = useState<Partial<Record<AgentId, string>>>({});

	const plans = useMemo(() => new Map(installers.data?.map((plan) => [plan.agentId, plan]) ?? []), [installers.data]);
	const jobMap = useMemo(() => new Map(jobs.data?.map((job) => [job.target, job]) ?? []), [jobs.data]);
	const agentAuthPlans = useMemo(() => new Map(authPlans.data?.map((plan) => [plan.agentId, plan]) ?? []), [authPlans.data]);
	const readinessAgents = useMemo(() => new Map(agents.data?.agents.map((agent) => [agent.id, agent]) ?? []), [agents.data]);
	const installed = useMemo(() => installedAgentIds(agents.data), [agents.data]);
	const refreshModelsOnOpen = (agentId: AgentId) => {
		if (!installed.has(agentId) || (needsAuthentication(agentId) && !MULTI_PROVIDER_HARNESSES.has(agentId))) return;
		const options = agentModelsQueryOptions(agentId, "", hostId, true);
		// Read the persisted cache first, then refresh without replacing the
		// visible list with a loading state. Concurrent opens share one request.
		void queryClient.fetchQuery({
			queryKey: ["agent-model-revalidation", hostId ?? LOCAL_HOST, agentId, "settings-open"],
			staleTime: 0,
			retry: false,
			queryFn: async () => {
				await queryClient.prefetchQuery(options);
				const catalog = await refreshAgentModels(agentId, "", hostId, true);
				if (catalog) queryClient.setQueryData(options.queryKey, catalog);
				return true;
			},
		}).catch((error: unknown) => {
			queryClient.setQueryData<components["schemas"]["AgentModelsResponse"]>(options.queryKey, (cached) => cached ? {
				...cached,
				refreshError: error instanceof Error ? error.message : t("settings.harness.models.loadFailed"),
			} : cached);
		});
	};
	useEffect(() => {
		const agentId = modelsRefreshPending.current;
		if (!agentId || agentId !== selectedAgentId || !installed.has(agentId)) return;
		modelsRefreshPending.current = null;
		refreshModelsOnOpen(agentId);
	});
	// The page uses the initial installed snapshot immediately, like Install
	// and Login. A full-catalog background refresh must not delay its first
	// version lookup. The app-wide notification has its own readiness gate.
	const advisoryMap = useHarnessUpdates(installed, hostId);
	const operationMethodId = (agentId: AgentId): string => {
		const advisory = queryClient.getQueryData<AgentUpdateAdvisory>(updateAdvisoryQueryKey(agentId, hostId));
		return maintenanceMethodId(advisory);
	};
	const needsAuthentication = (agentId: AgentId): boolean => needsHarnessLogin(agentAuthPlans.get(agentId), readinessAgents.get(agentId)?.authentication.state);
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
	const checkAuthRef = useRef<(agentId: AgentId) => Promise<unknown>>(async () => undefined);
	const refreshInstalledAgent = useCallback((agentId: AgentId) => {
		setActionErrors((current) => ({ ...current, [agentId]: undefined }));
		void checkAuthRef.current(agentId).finally(async () => {
			await Promise.all([
				queryClient.invalidateQueries({ queryKey: updateAdvisoryQueryKey(agentId, hostId) }),
				queryClient.invalidateQueries({ queryKey: installerKey }),
				queryClient.invalidateQueries({ queryKey: authPlansKey }),
				invalidateAgentModelCatalogs(queryClient, agentId, hostId),
			]);
		});
	}, [authPlansKey, hostId, installerKey, queryClient]);

	useEffect(() => {
		let active = true;
		let retryTimer: ReturnType<typeof setTimeout> | undefined;
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
		// No scrollIntoView: the open page always fills the dialog, whose body does not
		// scroll, and scrolling it anyway shifts the page up with no way back.
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
		for (const job of jobs.data ?? []) {
			const agentId = job.target as AgentId;
			if (isActiveInstallJob(job)) {
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
			if (isActiveInstallJob(job)) expandedFailures.current.delete(agentId);
			if (isFailedInstallJob(job) && expandedFailures.current.get(agentId) !== attempt.startedAt) {
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
		queryClient.setQueryData<InstallJob[]>(jobsKey, (current) => upsertInstallJob(current, job));
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
		if (isActiveInstallJob(queryClient.getQueryData<InstallJob[]>(jobsKey)?.find((job) => job.target === agentId))
			|| authWorkflowRef.current?.agentId === agentId || authStartPendingRef.current) return false;
		if (operation === "update" && (!installed.has(agentId)
			|| operationMethodId(agentId) !== method
			|| !supportsUpdate(plans.get(agentId)?.methods.find((candidate) => candidate.id === method)))) return false;
		if (operation === "update" && (needsAuthentication(agentId) || authPlans.isPending || authPlans.isError)) return false;
		if (!beginAction(agentId)) return false;
		setOperationAttempts((current) => ({ ...current, [agentId]: { operation } }));
		setActionErrors((current) => ({ ...current, [agentId]: undefined }));
		const fail = (message: string) => {
			setActionErrors((current) => ({ ...current, [agentId]: message }));
			revealAgent(agentId);
			return false;
		};
		try {
			let data: InstallJob;
			if (operation === "update") {
				const result = await startHarnessUpdate({
					agentId,
					methodId: method,
					canStart: () => mountedRef.current,
				});
				if (result.error) return fail(result.error);
				if (!result.job) return false;
				data = result.job;
			} else {
				const response = await client.POST("/api/v1/agents/{agent}/install", {
					params: { path: { agent: agentId } },
					body: { method, operation },
				});
				if (response.error || !response.data) return fail(apiErrorMessage(response.error, t("settings.harness.startFailed")));
				data = response.data;
			}
			setOperationAttempts((current) => ({ ...current, [agentId]: { operation, startedAt: data.startedAt } }));
			updateJob(data);
			if (data.status === "succeeded") refreshInstalledAgent(agentId);
			return true;
		} catch (error) {
			return fail(error instanceof Error ? error.message : t("settings.harness.startFailed"));
		} finally {
			endAction(agentId);
		}
	};

	const requestUpdate = (agentId: AgentId) => {
		if (!installed.has(agentId) || pendingActions.current.has(agentId) || isActiveInstallJob(jobMap.get(agentId))
			|| authWorkflowRef.current?.agentId === agentId || authStartPendingRef.current) return;
		const methods = plans.get(agentId)?.methods ?? [];
		const method = operationMethodId(agentId);
		if (!method || !supportsUpdate(methods.find((candidate) => candidate.id === method))) return;
		setActionErrors((current) => ({ ...current, [agentId]: undefined }));
		void startAgentOperation(agentId, method, "update");
	};

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
		await copyText(agentId, [diagnosticsText(agentId, job), healthReport(describe(agentId)), machine].filter(Boolean).join("\n\n"));
	};

	const copyText = async (agentId: AgentId, text: string) => {
		await aoBridge.clipboard.writeText(text);
		setCopiedAgent(agentId);
		window.setTimeout(() => setCopiedAgent((current) => (current === agentId ? null : current)), 1_500);
	};

	const startAuth = async (agentId: AgentId, action: "login" | "logout" = "login"): Promise<boolean> => {
		if (authWorkflowRef.current || authStartPendingRef.current || pendingActions.current.has(agentId) || isActiveInstallJob(jobMap.get(agentId))) return false;
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

	startAuthRef.current = startAuth;

	const checkAuth = useCallback(async (
		agentId: AgentId,
		{ fresh = false }: { fresh?: boolean } = {},
	): Promise<AgentAuthProbeResult | undefined> => {
		const existing = authChecksInFlight.current.get(agentId);
		if (existing && !fresh) return existing;
		setCheckingAuth((current) => current.includes(agentId) ? current : [...current, agentId]);
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
				setAuthCheckErrors((current) => ({ ...current, [agentId]: undefined }));
				return result;
			} catch (error) {
				const reason = error instanceof AgentCheckError ? error.message : "Authentication verification request could not complete. Check the selected host connection and try again.";
				setAuthCheckErrors((current) => ({ ...current, [agentId]: `${new Date().toISOString()} · ${reason}` }));
				return undefined;
			}
		})();
		authChecksInFlight.current.set(agentId, check);
		const finishCheck = () => {
			if (authChecksInFlight.current.get(agentId) === check) {
				authChecksInFlight.current.delete(agentId);
				setCheckingAuth((current) => current.filter((id) => id !== agentId));
			}
		};
		void check.then(finishCheck, finishCheck);
		return check;
	}, [hostId, queryClient]);

	checkAuthRef.current = (agentId) => checkAuth(agentId, { fresh: true });

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

	const describe = (agentId: AgentId) => describeHarness({
		agentId,
		t,
		language: i18n.resolvedLanguage ?? "en",
		plan: plans.get(agentId),
		job: jobMap.get(agentId),
		installed: installed.has(agentId),
		selectedMethodId: selectedMethods[agentId],
		advisoryQuery: advisoryMap.get(agentId),
		pending: pendingAgentIds.has(agentId),
		actionError: actionErrors[agentId],
		attempt: operationAttempts[agentId],
		readinessAgent: readinessAgents.get(agentId),
		readinessPending: agents.isPending,
		readinessFailed: Boolean(agents.error),
		authPlan: agentAuthPlans.get(agentId),
		authPlansPending: authPlans.isPending,
		authPlansFailed: authPlans.isError,
		authState: authStates[agentId],
		authWorkflow: authWorkflow?.agentId === agentId ? authWorkflow : null,
		updatedAt: lastUpdatedAt[agentId],
	});
	const healthReport = (details: HarnessDetails) => healthText(details, { hostId, authCheckError: authCheckErrors[details.agentId], t });

	const accountTab = (details: HarnessDetails) => {
		const { agentId, rowAuthWorkflow } = details;
		return <HarnessAccountTab
			details={details}
			hostId={hostId}
			authWorkflowActive={Boolean(authWorkflow)}
			copied={copiedAgent === agentId}
			diagnosticsExpanded={expandedDiagnostics[agentId] === true}
			onToggleDiagnostics={() => setExpandedDiagnostics((current) => ({ ...current, [agentId]: !current[agentId] }))}
			onCopyDiagnostics={(job) => void copyDiagnostics(agentId, job)}
			onInstall={(methodId) => void startAgentOperation(agentId, methodId, "install")}
			onSelectMethod={(methodId) => setSelectedMethods((current) => ({ ...current, [agentId]: methodId }))}
			onCopyCommand={(command) => void copyText(agentId, command)}
			onUpdate={() => requestUpdate(agentId)}
			onLogin={() => void startAuth(agentId)}
			onLogout={() => setLogoutRequest(agentId)}
			onVerify={() => void verifyInstall(agentId)}
			onCloseAuth={() => { if (rowAuthWorkflow) void closeAuth(rowAuthWorkflow); }}
			onRetryAuth={() => {
				if (!rowAuthWorkflow) return;
				void closeAuth(rowAuthWorkflow).then((closed) => {
					if (!closed) return;
					if (rowAuthWorkflow.action === "logout") setLogoutRequest(agentId); else void startAuth(agentId);
				});
			}}
			onTerminalState={(state) => {
				if (rowAuthWorkflow && state === "exited" && authWorkflowRef.current?.phase === "running") void finishAuth(rowAuthWorkflow);
			}}
		/>;
	};

	const selectedDetails = selectedAgentId ? describe(selectedAgentId) : null;
	const detailsOpen = Boolean(selectedDetails);
	// The list and the open page each scroll on their own, so the layout always
	// fills the Settings body and the body itself never scrolls. Its scrolling is
	// switched off in styles.css, but overflow: hidden keeps any earlier offset and
	// still yields to focus or scrollIntoView, so hold the body at the top.
	useLayoutEffect(() => {
		const body = layoutRef.current?.closest<HTMLElement>(".settings-thin-scrollbar");
		if (!body) return;
		const toTop = () => { if (body.scrollTop !== 0) body.scrollTop = 0; };
		toTop();
		body.addEventListener("scroll", toTop);
		return () => body.removeEventListener("scroll", toTop);
	}, []);

	return (
		<>
			{installers.error || authPlans.error || agents.error || jobs.error ? (
				<div className="flex items-center gap-2 rounded-md border border-error/30 bg-error/10 px-3 py-2 text-xs text-error">
					<TriangleAlert className="size-4" aria-hidden="true" />
					{jobs.error instanceof Error ? jobs.error.message : t("settings.harness.loadFailed")}
				</div>
			) : null}

			<div ref={layoutRef} data-harness-detail="" className={cn("relative flex w-full min-w-0 max-w-full min-h-0 flex-1 flex-col overflow-hidden", split && "grid-rows-[minmax(0,1fr)]", split && "grid grid-cols-[minmax(14rem,16rem)_minmax(0,1fr)] items-stretch overflow-clip rounded-xl border border-border bg-(--color-bg-primary)")}>
				{split || !selectedDetails ? (
					<div className={cn("flex w-full flex-col", split
						? "relative h-full min-h-0 max-h-full divide-y divide-border overflow-y-auto overscroll-contain bg-(--color-bg-secondary)"
						: "relative min-h-0 flex-1 divide-y divide-border overflow-y-auto overscroll-contain rounded-lg border border-border")}>
						{rows.map((agentId) => {
							const details = describe(agentId);
							const selected = split && selectedAgentId === agentId;
							const statusId = `harness-list-status-${agentId}`;
							return <Tooltip key={agentId}><TooltipTrigger asChild><button
								type="button"
								aria-label={t("settings.harness.openDetails", { agent: agentLabel(agentId) })}
								// The status mark is visual only; its words reach screen readers here.
								aria-describedby={statusId}
								aria-current={selected ? "true" : undefined}
								className={cn(
									"flex w-full items-center gap-3 text-left outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
									split ? "min-h-11 shrink-0 px-3" : "min-h-12 shrink-0 px-3",
									selected
										? (split ? "bg-(--color-bg-tertiary) font-medium" : "bg-accent font-medium")
										: "hover:bg-(--color-interactive-hover)",
								)}
								data-agent={agentId}
								onClick={() => openDetail(agentId)}
							>
								{/* sr-only is absolutely positioned: the list is `relative` so these stay inside its scroll box instead of stretching the app shell. */}
								<span id={statusId} className="sr-only">{details.statusLabel}</span>
								{/* Not-installed harnesses stay in place but recede. */}
								<AgentAvatar className={cn("shrink-0", split ? "size-5" : "size-6", !details.isInstalled && "opacity-45 grayscale")} decorative provider={agentId} />
								<span className={cn("min-w-0 flex-1 truncate text-sm", details.isInstalled || selected ? "text-settings-label" : "text-settings-muted")}>{agentLabel(agentId)}</span>
								{/* One status slot: progress replaces the update icon while an install, update or login runs. */}
								{details.busy ? <span role="status" className="flex size-4 shrink-0 items-center justify-center"><LoaderCircle className="size-4 animate-spin text-settings-muted" strokeWidth={1.75} aria-hidden="true" /><span className="sr-only">{details.progressLabel}</span></span>
									: details.authBusy ? <LoaderCircle className="size-4 shrink-0 animate-spin text-settings-muted" strokeWidth={1.75} aria-hidden="true" />
									: details.isInstalled && details.updateAvailable ? <CircleArrowUp
										className="size-4 shrink-0 text-settings-muted"
										strokeWidth={1.75}
										aria-label={t("settings.harness.versionAvailable", { version: versionLabel(details.advisory!.latestVersion!) })}
										role="img"
									/>
									: details.listTone === "error" ? <HarnessStatusDot tone="error" /> : null}
								{split ? null : <ChevronRight className="size-4 shrink-0 text-settings-muted" aria-hidden="true" />}
							</button></TooltipTrigger>{details.statusLabel ? <TooltipContent side="right">{details.statusLabel}</TooltipContent> : null}</Tooltip>;
						})}
						{rows.length === 0 ? <p className="px-3 py-6 text-center text-sm text-settings-muted">{t("settings.harness.noResults")}</p> : null}
					</div>
				) : null}
				{selectedDetails ? (
					<div
						className={cn("flex w-full min-w-0 flex-col gap-4", detailsOpen && "min-h-0 flex-1 overflow-hidden", split && "p-5")}
						data-agent={selectedDetails.agentId}
						data-focus-highlighted={highlightedAgentId === selectedDetails.agentId ? "" : undefined}
						ref={detailRef}
					>
						{split ? null : <div className="shrink-0">
							<Button type="button" size="sm" variant="ghost" className="-ml-2 h-7 gap-1 px-2 text-settings-muted" onClick={closeDetail}>
								<ChevronLeft aria-hidden="true" />{t("settings.harness.allHarnesses")}
							</Button>
						</div>}
						<div
							className={cn("-mx-2 flex shrink-0 items-center gap-3 rounded-lg px-2 py-1 transition-[background-color,box-shadow] duration-200", highlightedAgentId === selectedDetails.agentId && "bg-accent-weak ring-2 ring-inset ring-accent")}
						>
							<AgentAvatar className="size-7 shrink-0" decorative provider={selectedDetails.agentId} />
							{/* Pane title: one step above row labels (15px), below the page heading. */}
							<span className="min-w-0 flex-1 text-subtitle font-semibold leading-6 text-settings-label" id={`harness-agent-${selectedDetails.agentId}`}>{agentLabel(selectedDetails.agentId)}</span>
						</div>
						<Tabs className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden" value={detailTab} onValueChange={(value) => setDetailTab(value as HarnessDetailTab)}>
							<TabsList className={HARNESS_TABS_CLASS} aria-label={t("settings.harness.detailTabs", { agent: agentLabel(selectedDetails.agentId) })}>
								<TabsTrigger className={HARNESS_TAB_CLASS} value="account">{t("settings.harness.tabAccount")}</TabsTrigger>
								<TabsTrigger className={HARNESS_TAB_CLASS} value="models">{t("settings.harness.tabModels")}</TabsTrigger>
								<TabsTrigger className={HARNESS_TAB_CLASS} value="health">{t("settings.harness.tabHealth")}</TabsTrigger>
							</TabsList>
							<TabsContent value="account" className="settings-thin-scrollbar min-h-0 min-w-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain pt-4">{accountTab(selectedDetails)}</TabsContent>
							<TabsContent value="models" className="min-h-0 min-w-0 overflow-hidden pt-4 data-[state=active]:flex data-[state=active]:flex-col">
								<HarnessModelsPanel
									agentId={selectedDetails.agentId}
									hostId={hostId}
									installed={selectedDetails.isInstalled}
									needsLogin={needsAuthentication(selectedDetails.agentId)}
									onOpenAccount={() => setDetailTab("account")}
								/>
							</TabsContent>
							<TabsContent value="health" className="settings-thin-scrollbar min-h-0 min-w-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain pt-4">
								<HarnessHealthTab
									details={selectedDetails}
									checking={checkingAuth.includes(selectedDetails.agentId) || agents.isFetching || Boolean(selectedDetails.advisoryQuery?.isFetching)}
									authCheckFailed={Boolean(authCheckErrors[selectedDetails.agentId])}
									copied={copiedAgent === selectedDetails.agentId}
									onCheckAgain={() => refreshInstalledAgent(selectedDetails.agentId)}
									onCopyReport={() => void (async () => {
										const machine = await machineText(queryClient, hostId ? client : undefined);
										await copyText(selectedDetails.agentId, [healthReport(selectedDetails), machine].filter(Boolean).join("\n\n"));
									})()}
								/>
							</TabsContent>
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
				confirmDisabled={!logoutRequest || !agentAuthPlans.get(logoutRequest)?.available || !agentAuthPlans.get(logoutRequest)?.logoutCommand || Boolean(authWorkflow) || (logoutRequest ? pendingAgentIds.has(logoutRequest) || isActiveInstallJob(jobMap.get(logoutRequest)) : false)}
				error={logoutRequest ? authStates[logoutRequest]?.error : null}
				onConfirm={() => { if (logoutRequest) void startAuth(logoutRequest, "logout").then((started) => { if (started) setLogoutRequest(null); }); }}
				onOpenChange={(open) => { if (!open && !authStartPendingRef.current) setLogoutRequest(null); }}
			/>
		</>
	);
}
