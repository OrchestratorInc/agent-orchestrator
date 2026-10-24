import { Feather as BrandFeather } from "@expo/vector-icons";
import { Checkbox, Column, Host, Picker } from "@expo/ui";
import { CloudApiError, type CoderTemplate, type CoderTemplateParameter, type CreateGitHubProjectInput, type CreateProjectInput, type GitHubInstallation, type GitHubRepository, type OrgCoderConfigResponse, type ProjectConfig } from "@aoagents/cloud-client";
import * as Crypto from "expo-crypto";
import { useRouter } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, Alert, AppState, BackHandler, Keyboard, Platform, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { backOr } from "../lib/backNavigation";
import { readyHarnesses } from "../lib/cloud/agentReadiness";
import { useCloudAuth, type CloudAuthState } from "../lib/cloud/authStore";
import { CloudAgentSelect } from "../lib/cloud/cloud-agent-select";
import { CloudAgentConnections } from "../lib/cloud/CloudAgentConnections";
import { loadGrantedRepositories } from "../lib/cloud/githubProjectRepositories";
import { createGitHubConnectionAttempt } from "../lib/cloud/githubConnection";
import { cloudAccountScopeKey } from "../lib/cloud/deployment";
import { offersProviderChoice } from "../lib/cloud/offering";
import { acceptCloudOperation, clearCloudOperation, prepareCloudOperation, readCloudOperationsForScope, reserveCloudOperation, type CloudOperation } from "../lib/cloud/operation";
import { cloudProjectInput, coderParameterProblem, coderSetupProblem, findCloudProjectForRepository, initialProjectCreationStep, normalizedGitHubRepository, validGitHubRepositoryURL } from "../lib/cloud/projectCreation";
import { cloudProjectSetupCache } from "../lib/cloud/projectSetupCache";
import { haptics } from "../lib/haptics";
import { Feather } from "../lib/icons";
import { openGitHub } from "../lib/openGitHub";
import { PickerOptions } from "../lib/PickerOptions";
import { useApp } from "../lib/store";
import type { Theme } from "../lib/theme";
import { useTheme, useThemeState, useThemedStyles } from "../lib/ThemeProvider";
import { textSelectionColors } from "../lib/textSelection";
import { Button, HeaderIconButton } from "../lib/ui";
import { fontScaleCap, iconSize, radius, sheetInset, space, touchTarget, type as typography } from "../lib/tokens";

export { SheetErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";

type Step = "unavailable" | "github" | "github-token" | "repository" | "agents";
type ProjectRequest = { kind: "app"; repositoryUrl: string; input: CreateGitHubProjectInput } | { kind: "manual"; repositoryUrl: string; input: CreateProjectInput };
type ReservedProject = { slot: string; operation: CloudOperation<ProjectRequest> };

const AGENT_LABELS: Record<string, string> = {
	"claude-code": "Claude Code",
	codex: "Codex",
	cursor: "Cursor",
	opencode: "OpenCode",
};

export default function CreateCloudProject() {
	const auth = useCloudAuth();
	// Remount before rendering a different owner's draft or cached metadata.
	const ownerKey = JSON.stringify([auth.baseUrl.replace(/\/+$/, ""), auth.accountId, auth.orgId, auth.sessionEpoch]);
	return <CreateCloudProjectForm key={ownerKey} auth={auth} cacheKey={auth.signedIn && auth.accountId && auth.orgId ? ownerKey : null} />;
}

function CreateCloudProjectForm({ auth, cacheKey }: { auth: CloudAuthState; cacheKey: string | null }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const insets = useSafeAreaInsets();
	const { refreshSource } = useApp();
	const { client, signedIn, orgId, orgLoading, orgError, retryOrgResolution, accountId, baseUrl, offering } = auth;
	const { scheme } = useThemeState();
	const scopeKey = accountId && orgId ? cloudAccountScopeKey({ baseUrl, userId: accountId, orgId }) : null;
	const manualDeployment = baseUrl.replace(/\/+$/, "") === "https://staging-api.aoagents.dev";
	const cachedSetup = useMemo(() => cacheKey ? cloudProjectSetupCache.read(cacheKey) : {}, [cacheKey]);
	const cachedAgents = cachedSetup.connections ? readyHarnesses(cachedSetup.connections.value) : [];
	const authRef = useRef(auth); authRef.current = auth;
	const mounted = useRef(true);
	const createAbort = useRef<AbortController | null>(null);
	const manualAbort = useRef<AbortController | null>(null);
	const navigationAck = useRef<ReservedProject | null>(null);
	const [reserved, setReservedState] = useState<ReservedProject | null>(null);
	const reservedRef = useRef<ReservedProject | null>(null);
	const setReserved = useCallback((value: ReservedProject | null) => {
		reservedRef.current = value;
		setReservedState(value);
	}, []);
	const [refreshError, setRefreshError] = useState<string | null>(null);
	const [configDefaults, setConfigDefaults] = useState<ProjectConfig>({});
	const [coderConfig, setCoderConfig] = useState<OrgCoderConfigResponse | null>(null);
	const [coderTemplates, setCoderTemplates] = useState<CoderTemplate[]>([]);
	const [templateId, setTemplateId] = useState("");
	const [parameters, setParameters] = useState<CoderTemplateParameter[]>([]);
	const [parameterValues, setParameterValues] = useState<Record<string, string>>({});
	const [workspacePrefix, setWorkspacePrefix] = useState("");
	const [computeLoading, setComputeLoading] = useState(false);
	const [computeError, setComputeError] = useState<string | null>(null);
	const [computeAttempt, setComputeAttempt] = useState(0);
	const [parametersLoading, setParametersLoading] = useState(false);
	const [parametersError, setParametersError] = useState<string | null>(null);
	const [step, setStep] = useState<Step>(manualDeployment ? "repository" : "github");
	const [setupAttempt, setSetupAttempt] = useState(0);
	const [requestRestored, setRequestRestored] = useState(false);
	const [restoreError, setRestoreError] = useState<string | null>(null);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [token, setToken] = useState("");
	const [repositoryUrl, setRepositoryUrl] = useState("");
	const [displayName, setDisplayName] = useState("");
	const [defaultBranch, setDefaultBranch] = useState("main");
	const [readOnly, setReadOnly] = useState(false);
	const [agents, setAgents] = useState<string[]>(cachedAgents);
	const [agentsLoading, setAgentsLoading] = useState(!cachedSetup.connections);
	const [agentsError, setAgentsError] = useState<string | null>(null);
	const [workerAgent, setWorkerAgent] = useState(cachedAgents[0] ?? "");
	const [orchestratorAgent, setOrchestratorAgent] = useState(cachedAgents[0] ?? "");
	const [agentPicker, setAgentPicker] = useState<"worker" | "orchestrator" | null>(null);
	const [connectingAgents, setConnectingAgents] = useState(false);
	const [installations, setInstallations] = useState<GitHubInstallation[]>(cachedSetup.github?.value.installations ?? []);
	const [repositories, setRepositories] = useState<GitHubRepository[]>(cachedSetup.github?.value.repositories ?? []);
	const [repositorySearch, setRepositorySearch] = useState("");
	const [repositoryPickerOpen, setRepositoryPickerOpen] = useState(false);
	const [selectedRepositoryId, setSelectedRepositoryId] = useState("");
	const [projectSource, setProjectSource] = useState<"app" | "manual">("app");
	const [githubError, setGithubError] = useState<string | null>(null);
	const [connecting, setConnecting] = useState(false);
	const [openingGitHub, setOpeningGitHub] = useState(false);
	const [loadingRepositories, setLoadingRepositories] = useState(false);
	const [githubLoading, setGithubLoading] = useState(!manualDeployment && !cachedSetup.github);
	const [hasSavedToken, setHasSavedToken] = useState(cachedSetup.connections ? initialProjectCreationStep(cachedSetup.connections.value) === "repository" : false);
	const connectAttempt = useRef(0);
	const connectionBaseline = useRef<Map<string, string> | null>(null);
	const requestAbort = useRef<AbortController | null>(null);
	const connectRequest = useRef<AbortController | null>(null);
	const installationStarting = useRef(false);
	const syncAttempted = useRef<Set<string>>(new Set());
	const connectionsRequest = useRef(0);
	const manualDraftStarted = useRef(false);
	const selectedRepository = repositories.find((repository) => repository.githubRepositoryId === selectedRepositoryId);
	const activeInstallations = installations.filter((installation) => installation.status === "active");
	const readyInstallations = activeInstallations.filter((installation) => installation.syncStatus === "ready");
	const waitingForGitHub = connecting && (openingGitHub || connectionBaseline.current !== null);
	const githubReadyToImport = readyInstallations.length > 0 && !waitingForGitHub;
	const githubStatusText = openingGitHub ? "Opening GitHub…"
		: activeInstallations.some((installation) => installation.syncStatus === "pending") ? `Syncing repositories from ${activeInstallations.map((item) => item.accountLogin).join(", ")}`
		: waitingForGitHub ? "Waiting for GitHub approval…"
		: readyInstallations.length > 0 ? `Connected to ${readyInstallations.map((item) => item.accountLogin).join(", ")}`
		: connecting ? `Syncing repositories from ${activeInstallations.map((item) => item.accountLogin).join(", ")}`
		: `Repository sync needs attention for ${activeInstallations.map((item) => item.accountLogin).join(", ")}`;
	const visibleRepositories = useMemo(() => repositorySearch.trim() ? repositories.filter((repository) => repository.fullName.toLowerCase().includes(repositorySearch.trim().toLowerCase())) : [], [repositories, repositorySearch]);
	const shownRepositories = visibleRepositories.slice(0, 30);
	const displayedStep = step;
	const workspaceLoading = signedIn === null || orgLoading;
	const scopedReserved = reserved?.operation.scopeKey === scopeKey ? reserved : null;
	const selectionRef = useRef({ agentPicker, agents, busy, frozen: Boolean(scopedReserved), workerAgent, orchestratorAgent });
	selectionRef.current = { agentPicker, agents, busy, frozen: Boolean(scopedReserved), workerAgent, orchestratorAgent };
	// New projects inherit the Settings default. A retry keeps its recorded
	// provider, including legacy Coder config, even if the offering has changed.
	const reservedConfig = scopedReserved?.operation.payload.input.config as ProjectConfig | undefined;
	const recordedProvider = typeof reservedConfig?.sandboxProvider === "string" ? reservedConfig.sandboxProvider.trim().toLowerCase() : "";
	const effectiveProvider = recordedProvider || (reservedConfig?.coder !== null && typeof reservedConfig?.coder === "object" ? "coder" : offering?.defaultProvider ?? "");
	const usesCoder = effectiveProvider === "coder";
	const valueForParameter = (parameter: CoderTemplateParameter) => parameterValues[parameter.name] ??
		(parameter.name === "size" ? configDefaults.coder?.size : parameter.name === "startup_script" ? configDefaults.coder?.startupScript : undefined) ??
		coderConfig?.coderConfig?.parameters?.[parameter.name] ?? parameter.defaultValue;
	useEffect(() => {
		if (!agentPicker && !connectingAgents) return;
		const listener = BackHandler.addEventListener("hardwareBackPress", () => { setAgentPicker(null); setConnectingAgents(false); return true; });
		return () => listener.remove();
	}, [agentPicker, connectingAgents]);
	useEffect(() => {
		if (busy || scopedReserved || !agents.length) setAgentPicker(null);
	}, [busy, scopedReserved, agents.length]);
	const openAgentPicker = (role: "worker" | "orchestrator") => {
		const latest = selectionRef.current;
		if (!mounted.current || !authRef.current.isCurrentSession() || latest.busy || latest.frozen || !latest.agents.length) return;
		Keyboard.dismiss(); setAgentPicker(role);
	};
	const chooseAgent = useCallback((role: "worker" | "orchestrator", agent: string) => {
		const latest = selectionRef.current;
		if (!mounted.current || !authRef.current.isCurrentSession() || latest.agentPicker !== role || latest.busy || latest.frozen || !latest.agents.includes(agent)) return;
		if (agent !== (latest.agentPicker === "worker" ? latest.workerAgent : latest.orchestratorAgent)) {
			haptics.select();
			if (latest.agentPicker === "worker") setWorkerAgent(agent); else setOrchestratorAgent(agent);
		}
		setAgentPicker(null);
	}, []);

	useEffect(() => {
		mounted.current = true;
		return () => {
			mounted.current = false;
			createAbort.current?.abort();
			manualAbort.current?.abort();
			const completed = navigationAck.current;
			if (completed) void clearCloudOperation(completed.operation.scopeKey, completed.slot, completed.operation.key).catch(() => {});
		};
	}, []);
	useEffect(() => {
		setCoderConfig(null); setCoderTemplates([]); setComputeError(null);
		if (!usesCoder || !orgId) { setComputeLoading(false); return; }
		const controller = new AbortController();
		setComputeLoading(true);
		void Promise.all([client.getOrgCoderConfig(orgId, { signal: controller.signal }), client.listCoderTemplates(orgId, { signal: controller.signal })])
			.then(([config, result]) => {
				if (controller.signal.aborted) return;
				setCoderConfig(config); setCoderTemplates(result.templates);
			}).catch((cause) => { if (!controller.signal.aborted) setComputeError(messageFor(cause, "Could not load Coder settings.")); })
			.finally(() => { if (!controller.signal.aborted) setComputeLoading(false); });
		return () => controller.abort();
	}, [usesCoder, orgId, client, computeAttempt]);
	useEffect(() => {
		setParameters([]); setParametersError(null);
		if (!usesCoder || !templateId || !orgId) { setParametersLoading(false); return; }
		const controller = new AbortController();
		setParametersLoading(true);
		void client.getCoderTemplateParameters(orgId, templateId, { signal: controller.signal }).then((result) => {
			if (!controller.signal.aborted) setParameters(result.parameters.slice().sort((a, b) => a.order - b.order));
		}).catch((cause) => { if (!controller.signal.aborted) setParametersError(messageFor(cause, "Could not load template options.")); })
			.finally(() => { if (!controller.signal.aborted) setParametersLoading(false); });
		return () => controller.abort();
	}, [usesCoder, templateId, orgId, client, computeAttempt]);
	const parameterErrors = Object.fromEntries(parameters.map((parameter) => [parameter.name, coderParameterProblem(parameter, valueForParameter(parameter))]).filter(([, problem]) => problem));
	const computeProblem = usesCoder ? computeError ?? parametersError ?? (computeLoading || parametersLoading ? "Loading Coder settings…" : coderConfig ? coderSetupProblem(coderConfig, coderTemplates, templateId) : "Could not load Coder settings.") : null;
	const prefixProblem = usesCoder && coderConfig?.configured && workspacePrefix && (workspacePrefix.length > 20 || !/^[a-z]([a-z0-9]|-[a-z0-9])*$/.test(workspacePrefix)) ? "Use up to 20 lowercase letters, digits, and single hyphens, starting with a letter." : null;

	const loadAgents = useCallback(async (signal?: AbortSignal) => {
		const captured = authRef.current;
		const request = ++connectionsRequest.current;
		const current = () => mounted.current && !signal?.aborted && captured.isCurrentSession() && request === connectionsRequest.current;
		setAgentsLoading(true); setAgentsError(null);
		try {
			const connections = await client.listUserProviderConnections({ signal });
			if (!current()) return;
			if (cacheKey) cloudProjectSetupCache.setConnections(cacheKey, connections);
			const harnesses = readyHarnesses(connections);
			setAgents(harnesses);
			// Keep valid choices and frozen retry payloads, not disconnected cached defaults.
			if (!reservedRef.current) {
				setWorkerAgent((value) => harnesses.includes(value) ? value : harnesses[0] ?? "");
				setOrchestratorAgent((value) => harnesses.includes(value) ? value : harnesses[0] ?? "");
			}
			const savedToken = initialProjectCreationStep(connections) === "repository";
			setHasSavedToken(savedToken);
			if (manualDeployment && !savedToken && !reservedRef.current && !manualDraftStarted.current) setStep((value) => value === "repository" ? "github-token" : value);
		} catch (cause) {
			if (current()) setAgentsError(messageFor(cause, "Could not load Cloud agent settings."));
		} finally { if (current()) setAgentsLoading(false); }
	}, [client, cacheKey, manualDeployment]);

	const refreshGitHub = useCallback(async (signal: AbortSignal) => {
		if (!orgId || manualDeployment) return false;
		const captured = authRef.current;
		const current = () => mounted.current && !signal.aborted && captured.isCurrentSession();
		setGithubLoading(true);
		try {
			let nextInstallations = await client.listGitHubInstallations(orgId, { signal });
			if (!current()) return false;
			setInstallations(nextInstallations);
			let active = nextInstallations.filter((installation) => installation.status === "active");
			let ready = active.some((installation) => installation.syncStatus === "ready");
			const needsSync = active.filter((installation) => installation.syncStatus === "pending" && !syncAttempted.current.has(installation.id));
			if (needsSync.length) {
				for (const installation of needsSync) {
					syncAttempted.current.add(installation.id);
					await client.syncGitHubInstallation(orgId, installation.id, { signal });
					if (!current()) return false;
				}
				nextInstallations = await client.listGitHubInstallations(orgId, { signal });
				if (!current()) return false;
				setInstallations(nextInstallations);
				active = nextInstallations.filter((installation) => installation.status === "active");
				ready = active.some((installation) => installation.syncStatus === "ready");
			}
			const connectionReady = !installationStarting.current && ready && (connectionBaseline.current === null || active.some((installation) =>
				installation.syncStatus === "ready" && connectionBaseline.current?.get(installation.id) !== installation.updatedAt,
			));
			if (!installationStarting.current && active.length > 0) {
				if (connectionReady) {
					connectionBaseline.current = null;
					setConnecting(false);
				} else if (active.some((installation) => installation.syncStatus === "failed")) {
					connectionBaseline.current = null;
					setConnecting(false);
					setGithubError(active.find((installation) => installation.syncStatus === "failed")?.lastError ?? "GitHub repository sync failed. Try again.");
				} else setConnecting(true);
			}
			let granted: GitHubRepository[] = [];
			if (ready) {
				setLoadingRepositories(true);
				granted = await loadGrantedRepositories(client, orgId, signal);
				if (!current()) return false;
				setGithubError(null);
			}
			setRepositories(granted);
			setSelectedRepositoryId((previous) => granted.some((repository) => repository.githubRepositoryId === previous) ? previous : "");
			if (cacheKey && !installationStarting.current && connectionBaseline.current === null && !active.some((installation) => installation.syncStatus === "pending")) {
				cloudProjectSetupCache.setGitHub(cacheKey, { installations: nextInstallations, repositories: granted });
			}
			return connectionReady;
		} finally {
			if (current()) { setGithubLoading(false); setLoadingRepositories(false); }
		}
	}, [client, orgId, manualDeployment, cacheKey]);

	const githubAttempt = useMemo(() => {
		const attempt = createGitHubConnectionAttempt({
			refresh: async (signal) => { if (await refreshGitHub(signal)) attempt.stop(); },
			isCurrent: () => Boolean(orgId) && requestAbort.current?.signal.aborted === false,
			onError: (cause) => {
				connectAttempt.current += 1;
				connectRequest.current?.abort();
				installationStarting.current = false;
				connectionBaseline.current = null;
				setOpeningGitHub(false); setConnecting(false);
				setGithubError(messageFor(cause, "Could not finish connecting GitHub."));
			},
		});
		return attempt;
	}, [orgId, refreshGitHub]);
	useEffect(() => () => { githubAttempt.stop(); connectRequest.current?.abort(); }, [githubAttempt]);
	useEffect(() => {
		const listener = AppState.addEventListener("change", (state) => {
			if (state === "active" && orgId && step === "github") void githubAttempt.refresh();
		});
		return () => listener.remove();
	}, [githubAttempt, orgId, step]);

	useEffect(() => {
		if (signedIn === null || orgLoading) {
			return;
		}
		if (!signedIn) {
			setError("Sign in to Orchestrator Cloud before adding a project.");
			setStep("unavailable");
			return;
		}
		if (!orgId) {
			setError(orgError ?? "Could not load your Orchestrator Cloud workspace.");
			setStep("unavailable");
			return;
		}
		const abort = new AbortController();
		requestAbort.current?.abort();
		requestAbort.current = abort;
		setError(null);
		setProjectSource((value) => manualDeployment ? "manual" : value);
		// Each section settles independently: GitHub must not wait for agents,
		// and neither may hide the form or delay an accepted request's recovery.
		if (!cachedSetup.connections?.fresh) {
			void loadAgents(abort.signal);
		} else if (manualDeployment && !hasSavedToken && !manualDraftStarted.current) setStep((value) => value === "repository" ? "github-token" : value);
		if (!manualDeployment && !cachedSetup.github?.fresh) {
			// Foreground, explicit, and initial reads share one sheet-owned flight.
			void githubAttempt.refresh();
		}
		return () => { abort.abort(); githubAttempt.stop(); };
	}, [signedIn, orgId, orgLoading, orgError, githubAttempt, loadAgents, cachedSetup, manualDeployment]);

	useEffect(() => {
		if (signedIn !== true || orgLoading || !scopeKey) return;
		let active = true;
		const captured = authRef.current;
		const current = () => active && captured.isCurrentSession();
		setRequestRestored(false); setRestoreError(null);
		void readCloudOperationsForScope<ProjectRequest>(scopeKey).then((records) => {
			if (!current()) return;
			const previous = records.find((record) => record.slot.startsWith("project-create/"));
			if (previous) {
				const request = previous.operation.payload;
				if (!request || !["app", "manual"].includes(request.kind) || !request.input || typeof request.input.displayName !== "string" || !validGitHubRepositoryURL(request.repositoryUrl)) {
					throw new Error("The saved project request could not be understood. Check existing projects before starting another request.");
				}
				const config = request.input.config as ProjectConfig ?? {};
				setReserved(previous); setConfigDefaults(config);
				setProjectSource(request.kind);
				setDisplayName(request.input.displayName); setRepositoryUrl(request.repositoryUrl);
				if (request.kind === "manual") setDefaultBranch(request.input.defaultBranch);
				else setSelectedRepositoryId(request.input.githubRepositoryId);
				setWorkerAgent(config.worker?.agent ?? ""); setOrchestratorAgent(config.orchestrator?.agent ?? "");
				setTemplateId(config.coder?.templateId ?? ""); setParameterValues(config.coder?.parameters ?? {});
				setWorkspacePrefix(config.coder?.workspaceNamePrefix ?? "");
				setStep("agents");
			}
			setRequestRestored(true);
		}).catch((cause) => {
			if (current()) setRestoreError(messageFor(cause, "Could not restore the previous Cloud request."));
		});
		return () => { active = false; };
	}, [signedIn, orgLoading, scopeKey, setupAttempt, setReserved]);

	useEffect(() => {
		if (connecting && orgId && step === "github") githubAttempt.start();
	}, [connecting, orgId, step, githubAttempt]);

	const connectGitHub = async () => {
		if (!orgId || connecting || manualDeployment) return;
		if (cacheKey) cloudProjectSetupCache.invalidate(cacheKey, "github");
		const attempt = ++connectAttempt.current;
		setGithubError(null);
		setConnecting(true);
		setOpeningGitHub(true);
		installationStarting.current = true;
		syncAttempted.current.clear();
		connectRequest.current?.abort();
		const controller = new AbortController();
		connectRequest.current = controller;
		githubAttempt.start();
		try {
			const signal = controller.signal;
			const before = await client.listGitHubInstallations(orgId, { signal });
			if (signal?.aborted || connectAttempt.current !== attempt) return;
			connectionBaseline.current = new Map(before.map((installation) => [installation.id, installation.updatedAt]));
			const { installationUrl } = await client.startGitHubInstallation(orgId, { signal });
			if (signal?.aborted || connectAttempt.current !== attempt) return;
			await openGitHub(installationUrl, { fromSheet: true, throwOnFailure: true });
			if (signal?.aborted || connectAttempt.current !== attempt) return;
			installationStarting.current = false;
			setOpeningGitHub(false);
			await githubAttempt.refresh();
		} catch (cause) {
			if (controller.signal.aborted || connectAttempt.current !== attempt) return;
			githubAttempt.stop();
			installationStarting.current = false;
			setGithubError(messageFor(cause, "Could not connect GitHub."));
			connectionBaseline.current = null;
			setConnecting(false);
			setOpeningGitHub(false);
		}
	};

	const stopWaiting = () => {
		connectAttempt.current += 1;
		connectRequest.current?.abort();
		githubAttempt.stop();
		installationStarting.current = false;
		connectionBaseline.current = null;
		setConnecting(false);
		setOpeningGitHub(false);
		setGithubLoading(false);
		setLoadingRepositories(false);
	};

	const syncRepositories = async () => {
		if (!orgId || busy) return;
		// A mutation must be followed by a new read, not join a pre-sync snapshot.
		githubAttempt.stop();
		setGithubLoading(false); setLoadingRepositories(false);
		if (cacheKey) cloudProjectSetupCache.invalidate(cacheKey, "github");
		setBusy(true);
		setGithubError(null);
		try {
			const signal = requestAbort.current?.signal ?? new AbortController().signal;
			for (const installation of activeInstallations.filter((item) => item.syncStatus !== "ready")) {
				await client.syncGitHubInstallation(orgId, installation.id, { signal });
			}
			// Foreground reads may have started during the POSTs; retire those too.
			githubAttempt.stop();
			if (cacheKey) cloudProjectSetupCache.invalidate(cacheKey, "github");
			await githubAttempt.refresh();
		} catch (cause) {
			setGithubError(messageFor(cause, "Could not refresh GitHub repositories."));
		} finally { setBusy(false); }
	};

	const saveToken = async () => {
		if (busy || token.trim().length < 8) {
			setError("Enter a GitHub personal access token.");
			return;
		}
		const captured = auth;
		const controller = new AbortController();
		const current = () => mounted.current && !controller.signal.aborted && captured.isCurrentSession() && authRef.current.accountId === captured.accountId && authRef.current.orgId === captured.orgId;
		if (!current() || manualAbort.current) return;
		manualAbort.current = controller; setBusy(true);
		setError(null);
		try {
			await client.putGitHubPAT({ secret: token.trim() }, { signal: controller.signal });
			if (!current()) return;
			setToken("");
			setHasSavedToken(true);
			if (cacheKey) cloudProjectSetupCache.invalidate(cacheKey, "connections");
			// Supersede pre-save metadata reads before they can undo token readiness.
			void loadAgents(requestAbort.current?.signal);
			setStep("repository");
		} catch (cause) {
			if (current()) setError(messageFor(cause, "Could not save the GitHub token."));
		} finally {
			if (manualAbort.current === controller) manualAbort.current = null;
			if (current()) setBusy(false);
		}
	};

	const checkRepository = async () => {
		if (busy) return;
		if (!validGitHubRepositoryURL(repositoryUrl)) {
			setError("Enter a GitHub repository URL like https://github.com/owner/repo.");
			return;
		}
		if (!displayName.trim() || !defaultBranch.trim()) {
			setError("Project name and default branch are required.");
			return;
		}
		const captured = auth;
		const controller = new AbortController();
		const current = () => mounted.current && !controller.signal.aborted && captured.isCurrentSession() && authRef.current.accountId === captured.accountId && authRef.current.orgId === captured.orgId;
		if (!current() || manualAbort.current) return;
		manualAbort.current = controller; setBusy(true);
		setError(null);
		setReadOnly(false);
		try {
			const access = await client.validateSavedRepositoryAccess({ repositoryUrl: repositoryUrl.trim() }, { signal: controller.signal });
			if (!current()) return;
			if (access.writeAccess) setStep("agents");
			else setReadOnly(true);
		} catch (cause) {
			if (!current()) return;
			if (cause instanceof CloudApiError && cause.code === "token_missing") {
				setStep("github-token");
				setError("Add a GitHub token before importing this repository.");
			} else {
				setError(messageFor(cause, "Could not verify repository access."));
			}
		} finally {
			if (manualAbort.current === controller) manualAbort.current = null;
			if (current()) setBusy(false);
		}
	};

	async function openProject(projectId: string, record: ReservedProject, current: () => boolean) {
		try { await refreshSource({ kind: "cloud", id: orgId! }); }
		catch (cause) { if (mounted.current && current()) setRefreshError("Project created. " + messageFor(cause, "Could not refresh the project list.")); }
		if (!mounted.current || !current()) return;
		navigationAck.current = record;
		try { router.replace({ pathname: "/project/[id]", params: { id: projectId, source: "cloud", sourceId: orgId! } }); }
		catch (cause) {
			navigationAck.current = null;
			setError("Project created. " + messageFor(cause, "Could not open it. Use Open project to try again."));
		}
	}

	async function submitOperation(record: ReservedProject, lookupOnly = false) {
		if (!scopeKey || !orgId || createAbort.current || !requestRestored) return;
		const captured = auth;
		const current = () => captured.isCurrentSession() && record.operation.scopeKey === scopeKey;
		if (!current()) return;
		const controller = new AbortController();
		createAbort.current = controller;
		setBusy(true); setError(null); setRefreshError(null);
		let accepted = record.operation.acceptedId;
		try {
			if (!accepted) {
				if (lookupOnly) {
					accepted = await findCloudProjectForRepository(client, orgId, record.operation.payload.repositoryUrl, controller.signal) ?? undefined;
					if (!accepted) throw new Error("No matching project was found. Retry the original request, or explicitly discard its retry record.");
				} else {
					if (computeProblem || prefixProblem || Object.keys(parameterErrors).length) throw new Error(computeProblem ?? prefixProblem ?? Object.values(parameterErrors)[0] as string);
					const config = record.operation.payload.input.config as ProjectConfig;
					const connected = readyHarnesses(await client.listUserProviderConnections({ signal: controller.signal }));
					if (!current() || controller.signal.aborted) return;
					if (!connected.includes(config.worker?.agent ?? "") || !connected.includes(config.orchestrator?.agent ?? "")) {
						if (mounted.current) setAgents(connected);
						throw new Error("Connect the project's personal coding agents below, then retry this request.");
					}
					await reserveCloudOperation(record.slot, record.operation);
					if (!current() || controller.signal.aborted) return;
					if (mounted.current) setReserved(record);
					const options = { idempotencyKey: record.operation.key, signal: controller.signal };
					try {
						const request = record.operation.payload;
						const response = request.kind === "app" ? await client.createProjectFromGitHub(orgId, request.input, options) : await client.createProject(orgId, request.input, options);
						accepted = response.project.id;
					} catch (cause) {
						if (!(cause instanceof CloudApiError && cause.code === "project_repository_exists")) throw cause;
						accepted = await findCloudProjectForRepository(client, orgId, record.operation.payload.repositoryUrl, controller.signal) ?? undefined;
						if (!accepted) throw cause;
					}
				}
				if (!current()) return;
				const completed = { ...record, operation: { ...record.operation, acceptedId: accepted } };
				if (mounted.current) setReserved(completed);
				// The server has accepted the write. A journal or board-refresh
				// failure must never turn it back into a new create request.
				try { await acceptCloudOperation(record.operation.scopeKey, record.slot, record.operation.key, accepted!); }
				catch (cause) { if (mounted.current && current()) setRefreshError("Project created. " + messageFor(cause, "Could not save its retry record.")); }
				if (mounted.current && current()) await openProject(accepted!, completed, current);
			} else await openProject(accepted, record, current);
		} catch (cause) {
			if (!mounted.current || !current() || controller.signal.aborted) return;
			if (record.operation.payload.kind === "manual" && cause instanceof CloudApiError && ["repository_unreachable", "read_only_token", "token_missing"].includes(cause.code)) {
				setStep(cause.code === "token_missing" ? "github-token" : "repository");
			}
			setError(messageFor(cause, "Could not finish the project request. Its original retry key is retained."));
		} finally {
			if (createAbort.current === controller) createAbort.current = null;
			if (mounted.current && current()) setBusy(false);
		}
	}

	const createProject = async () => {
		if (busy || !scopeKey || !orgId || !requestRestored) return;
		if (reserved) { await submitOperation(reserved); return; }
		if (projectSource === "app" && !selectedRepository) {
			setError("This repository is no longer available. Choose another repository.");
			setStep("github"); return;
		}
		if (!workerAgent || !orchestratorAgent) { setError("Connect a Cloud coding agent below first."); return; }
		const overrides = Object.fromEntries(parameters.filter((parameter) => parameterValues[parameter.name] !== undefined &&
			parameterValues[parameter.name] !== (coderConfig?.coderConfig?.parameters?.[parameter.name] ?? parameter.defaultValue))
			.map((parameter) => [parameter.name, parameterValues[parameter.name]!]));
		const coder = {
			...configDefaults.coder,
			...(templateId ? { templateId, ...(Object.keys(overrides).length ? { parameters: overrides } : {}) } : {}),
			...(coderConfig?.configured && workspacePrefix.trim() ? { workspaceNamePrefix: workspacePrefix.trim() } : {}),
		};
		const config: ProjectConfig = {
			...configDefaults,
			worker: { ...configDefaults.worker, agent: workerAgent as NonNullable<ProjectConfig["worker"]>["agent"] },
			orchestrator: { ...configDefaults.orchestrator, agent: orchestratorAgent as NonNullable<ProjectConfig["orchestrator"]>["agent"] },
			...(offersProviderChoice(offering ?? undefined) && effectiveProvider ? { sandboxProvider: effectiveProvider } : {}),
			...(usesCoder && Object.keys(coder).length ? { coder } : {}),
		};
		const payload: ProjectRequest = projectSource === "app" && selectedRepository
			? { kind: "app", repositoryUrl: selectedRepository.htmlUrl, input: { githubRepositoryId: selectedRepository.githubRepositoryId, displayName: displayName.trim(), config } }
			: { kind: "manual", repositoryUrl: repositoryUrl.trim(), input: cloudProjectInput({ displayName, repositoryUrl, defaultBranch, config }) };
		const slot = "project-create/" + normalizedGitHubRepository(payload.repositoryUrl);
		await submitOperation({ slot, operation: prepareCloudOperation(undefined, scopeKey, payload, Crypto.randomUUID) });
	};

	const discardReserved = () => {
		if (!reserved) return;
		Alert.alert("Discard the retry record?", "This does not delete any Cloud project. Check existing projects first if the previous request might have succeeded.", [
			{ text: "Cancel", style: "cancel" },
			{ text: "Discard retry record", style: "destructive", onPress: () => {
				const previous = reserved;
				void clearCloudOperation(previous.operation.scopeKey, previous.slot, previous.operation.key).then(() => {
					if (mounted.current && authRef.current.isCurrentSession()) { setReserved(null); setConfigDefaults({}); setError(null); }
				}).catch((cause) => { if (mounted.current) setError(messageFor(cause, "Could not discard the retry record.")); });
			} },
		]);
	};

	const refreshAgents = () => loadAgents(requestAbort.current?.signal);

	const back = () => {
		haptics.tap();
		if (connectingAgents) { setConnectingAgents(false); return; }
		if (agentPicker) { setAgentPicker(null); return; }
		if (step === "github" && repositoryPickerOpen) { setRepositoryPickerOpen(false); setRepositorySearch(""); Keyboard.dismiss(); return; }
		if (step === "agents") { setError(null); setStep(projectSource === "app" ? "github" : "repository"); }
		else if (step === "github-token" || step === "repository") { setError(null); setStep("github"); }
		else backOr(router);
	};

	if (connectingAgents) return <View style={styles.screen} collapsable={false}>
		<View style={styles.header}><Text maxFontSizeMultiplier={fontScaleCap.title} style={styles.headerTitle}>Add Cloud project</Text><HeaderIconButton icon="close" label="Close" onPress={() => backOr(router)} /></View>
		<View style={{ flex: 1, padding: space.lg }}><CloudAgentConnections inSheet client={client} orgId={orgId} ownerKey={JSON.stringify([baseUrl.replace(/\/+$/, ""), accountId, auth.sessionEpoch])} isCurrent={auth.isCurrentSession} onBack={() => setConnectingAgents(false)} onChanged={refreshAgents} /></View>
	</View>;

	return (
		<View style={styles.screen} collapsable={false}>
			<View style={styles.header}>
				<HeaderIconButton icon="back" label="Back" onPress={back} />
				<Text maxFontSizeMultiplier={fontScaleCap.title} style={styles.headerTitle}>Add Cloud project</Text>
				<HeaderIconButton icon="close" label="Close" onPress={() => backOr(router)} />
			</View>
			<ScrollView contentInsetAdjustmentBehavior="automatic" automaticallyAdjustKeyboardInsets={Platform.OS === "ios"} keyboardShouldPersistTaps="handled" contentContainerStyle={{ paddingBottom: insets.bottom + sheetInset.bottom }}>
				<View style={styles.content}>
				{agentPicker ? <PickerOptions
					title={agentPicker === "worker" ? "Worker agent" : "Orchestrator agent"}
					options={agents.map((agent) => ({ id: agent, label: AGENT_LABELS[agent] ?? agent }))}
					selectedValue={agentPicker === "worker" ? workerAgent : orchestratorAgent}
					showAgentLogos onSelect={(agent) => chooseAgent(agentPicker, agent)} onBack={() => setAgentPicker(null)}
				/> : <>
				{workspaceLoading ? <View style={styles.connectedRow}><ActivityIndicator color={t.accent} accessibilityLabel="Loading Cloud workspace" /><Text style={styles.hint}>Loading Cloud workspace…</Text></View> : null}
				{restoreError ? <View style={styles.group}><Text accessibilityRole="alert" style={styles.error}>{restoreError}</Text><Button title="Retry request recovery" variant="text" onPress={() => setSetupAttempt((value) => value + 1)} /></View> : null}
				{scopedReserved ? <View style={styles.group}>
					<Text style={styles.title}>{scopedReserved.operation.acceptedId ? "Project created" : "Finish your previous request"}</Text>
					<Text selectable style={styles.description}>{scopedReserved.operation.payload.input.displayName}{scopedReserved.operation.acceptedId ? " is ready to open." : " has a saved request. Retry keeps the same project settings and request key."}</Text>
					<Button title={scopedReserved.operation.acceptedId ? "Open project" : "Retry creation"} loading={busy} disabled={busy || (!scopedReserved.operation.acceptedId && Boolean(computeProblem || prefixProblem || Object.keys(parameterErrors).length))} onPress={() => submitOperation(scopedReserved)} />
					{!scopedReserved.operation.acceptedId ? <Button title="Check existing projects" variant="text" disabled={busy} onPress={() => submitOperation(scopedReserved, true)} /> : null}
					<Button title="Discard retry record" variant="text" disabled={busy} onPress={discardReserved} />
				</View> : null}
				{displayedStep === "unavailable" ? (
					<View style={styles.group}>
						<Text style={styles.title}>Can't add a project yet</Text>
						<Text style={styles.description}>{error}</Text>
						{signedIn ? <Button title="Retry" onPress={() => orgError || !orgId ? void retryOrgResolution() : setSetupAttempt((value) => value + 1)} /> : null}
						<Button title="Close" variant="text" onPress={() => backOr(router)} />
					</View>
				) : null}
				{displayedStep === "github" ? (
					<View style={styles.group}>
						<Text style={styles.title}>Choose a repository</Text>
						<Text style={styles.description}>Import a repository you’ve granted AO access to on GitHub.</Text>
						{githubLoading && !workspaceLoading ? <View style={styles.connectedRow}><ActivityIndicator color={t.accent} accessibilityLabel="Loading GitHub connection" /><Text style={styles.hint}>{loadingRepositories ? "Loading repositories…" : "Checking GitHub connection…"}</Text></View> : null}
						{activeInstallations.length === 0 && !githubLoading && !workspaceLoading ? (
							<View style={styles.connectCard}>
								<View style={styles.connectHeading}><BrandFeather name="github" size={iconSize.lg} color={t.textPrimary} /><Text style={styles.connectTitle}>Connect GitHub</Text></View>
								<Text style={styles.hint}>GitHub will open in a browser so you can choose which repositories AO may access. No token to copy or paste.</Text>
								<Button title={openingGitHub ? "Opening GitHub…" : connecting ? "Waiting for GitHub approval…" : "Connect GitHub"} loading={connecting} disabled={connecting} onPress={() => void connectGitHub()} />
								{connecting ? <Button title="Stop waiting" variant="text" onPress={stopWaiting} /> : null}
							</View>
						) : activeInstallations.length > 0 ? (
							<>
								<View style={styles.connectedRow}>
									<Feather name={githubReadyToImport ? "check-circle" : connecting ? "loader" : "alert-circle"} size={iconSize.md} color={githubReadyToImport ? t.green : t.amber} />
									<Text style={styles.connectedText}>{githubStatusText}</Text>
								</View>
								{activeInstallations.some((item) => item.syncStatus !== "ready") ? <Button title={connecting ? "Syncing repositories…" : "Retry repository sync"} variant="text" loading={busy} disabled={connecting} onPress={() => void syncRepositories()} /> : null}
								{connecting ? <Button title="Stop waiting" variant="text" onPress={stopWaiting} /> : null}
								{repositories.length > 0 ? (
									<>
										<View style={styles.field}>
											<Text style={styles.label}>Repository</Text>
											<Pressable accessibilityRole="combobox" accessibilityLabel="Select a repository" accessibilityValue={{ text: selectedRepository?.fullName ?? "None selected" }} accessibilityState={{ expanded: repositoryPickerOpen, disabled: busy || !githubReadyToImport || Boolean(scopedReserved) }} disabled={busy || !githubReadyToImport || Boolean(scopedReserved)}
												onPress={() => { haptics.tap(); setRepositoryPickerOpen(!repositoryPickerOpen); setRepositorySearch(""); if (repositoryPickerOpen) Keyboard.dismiss(); }}
												style={styles.repositoryTrigger}>
												<Feather name={selectedRepository?.isPrivate ? "lock" : "book"} size={iconSize.md} color={t.textSecondary} />
												<Text style={[styles.repositoryTriggerText, !selectedRepository && { color: t.textSecondary }]} numberOfLines={1}>{selectedRepository?.fullName ?? "Select a repository"}</Text>
												<Feather name={repositoryPickerOpen ? "chevron-up" : "chevron-down"} size={iconSize.sm} color={t.textSecondary} />
											</Pressable>
										</View>
										{repositoryPickerOpen ? <>
											<View style={styles.repositorySearch}>
												<Feather name="search" size={iconSize.md} color={t.textSecondary} />
												<TextInput {...textSelectionColors(t)} style={styles.repositorySearchInput} value={repositorySearch} onChangeText={setRepositorySearch} placeholder="Search repositories" placeholderTextColor={t.textSecondary} autoFocus autoCapitalize="none" autoCorrect={false} accessibilityLabel="Search repositories" editable={!busy && githubReadyToImport && !scopedReserved} />
											</View>
											{!repositorySearch.trim() ? <Text style={styles.hint}>Search by repository name.</Text> : <View style={styles.repositoryList}>
												{visibleRepositories.length === 0 ? <Text style={styles.emptyText}>No matching repositories</Text> : shownRepositories.map((repository) => (
													<Pressable key={repository.githubRepositoryId} accessibilityRole="radio" accessibilityState={{ selected: selectedRepositoryId === repository.githubRepositoryId }} accessibilityLabel={`Select ${repository.fullName}`} disabled={busy || !githubReadyToImport || Boolean(scopedReserved)} onPress={() => { if (busy || !githubReadyToImport || scopedReserved) return; haptics.select(); setProjectSource("app"); setSelectedRepositoryId(repository.githubRepositoryId); setDisplayName(repository.name); setRepositoryUrl(repository.htmlUrl); setDefaultBranch(repository.defaultBranch); setRepositoryPickerOpen(false); setRepositorySearch(""); Keyboard.dismiss(); }} style={[styles.repositoryOption, selectedRepositoryId === repository.githubRepositoryId && styles.repositorySelected]}>
														<View style={styles.repositoryIdentity}><Feather name={repository.isPrivate ? "lock" : "book"} size={iconSize.md} color={t.textSecondary} /><View style={styles.repositoryCopy}><Text style={styles.repositoryName} numberOfLines={1}>{repository.fullName}</Text><Text style={styles.repositoryMeta}>{repository.defaultBranch}</Text></View></View>
														{selectedRepositoryId === repository.githubRepositoryId ? <Feather name="check" size={iconSize.md} color={t.accent} /> : null}
													</Pressable>
												))}
											</View>}
											{visibleRepositories.length > shownRepositories.length ? <Text style={styles.hint}>Showing the first 30 matches. Refine your search to see fewer.</Text> : null}
										</> : null}
										<Button title="Continue" disabled={!selectedRepository} onPress={() => { setError(null); setStep("agents"); }} />
									</>
								) : !loadingRepositories && readyInstallations.length > 0 ? (
									<View style={styles.group}>
										<Text style={styles.hint}>No repositories are available to import. Grant AO access to a repository in GitHub, then refresh this list.</Text>
										<Button title="Manage GitHub access" variant="text" icon="external-link" disabled={connecting} onPress={() => void connectGitHub()} />
									</View>
								) : null}
								<Button title="Refresh repositories" variant="text" onPress={() => { void githubAttempt.refresh(); }} />
							</>
						) : null}
						{githubError ? <Text accessibilityRole="alert" style={styles.error}>{githubError}</Text> : null}
						{githubError && activeInstallations.length === 0 ? <Button title="Retry GitHub connection" variant="text" disabled={githubLoading} onPress={() => void githubAttempt.refresh()} /> : null}
						<Button title="Set up manually with a token" variant="text" disabled={workspaceLoading} onPress={() => { manualDraftStarted.current = true; setError(null); if (connecting || openingGitHub) stopWaiting(); setProjectSource("manual"); setSelectedRepositoryId(""); setRepositoryUrl(""); setDisplayName(""); setStep(hasSavedToken || agentsLoading || agentsError ? "repository" : "github-token"); }} />
					</View>
				) : null}
				{displayedStep === "github-token" ? (
					<View style={styles.group}>
						<Text style={styles.title}>Connect GitHub</Text>
						<Text style={styles.description}>For manual setup, add a fine-grained token with access to the repository. Orchestrator Cloud stores it securely and never shows it again.</Text>
						<View style={styles.field}>
							<Text style={styles.label}>GitHub access token</Text>
							<TextInput {...textSelectionColors(t)} style={styles.input} value={token} onChangeText={(value) => { manualDraftStarted.current = true; setToken(value); }} placeholder="github_pat_…" placeholderTextColor={t.textTertiary} secureTextEntry autoCapitalize="none" autoCorrect={false} textContentType="password" accessibilityLabel="GitHub access token" />
						</View>
						<Text style={styles.hint}>Use a fine-grained token with Contents read and write access to the repository.</Text>
						<Button title="Create a GitHub token" variant="text" icon="external-link" onPress={() => void openGitHub("https://github.com/settings/personal-access-tokens/new", { fromSheet: true, throwOnFailure: true }).catch((cause) => setError(messageFor(cause, "Could not open GitHub.")))} />
						<Button title="Save token and continue" loading={busy} disabled={token.trim().length < 8} onPress={() => void saveToken()} />
					</View>
				) : null}
				{displayedStep === "repository" ? (
					<View style={styles.group}>
						<Text style={styles.title}>Import a repository</Text>
						<Text style={styles.description}>Enter the repository URL and branch for this project.</Text>
						<View style={styles.field}>
							<Text style={styles.label}>Repository URL</Text>
							<TextInput {...textSelectionColors(t)} style={styles.input} value={repositoryUrl} onChangeText={(value) => { manualDraftStarted.current = true; setRepositoryUrl(value); setReadOnly(false); }} placeholder="https://github.com/owner/repo" placeholderTextColor={t.textTertiary} autoCapitalize="none" autoCorrect={false} keyboardType="url" accessibilityLabel="Repository URL" editable={!busy && !scopedReserved} />
						</View>
						<View style={styles.field}>
							<Text style={styles.label}>Project name</Text>
							<TextInput {...textSelectionColors(t)} style={styles.input} value={displayName} onChangeText={(value) => { manualDraftStarted.current = true; setDisplayName(value); }} placeholder="My project" placeholderTextColor={t.textTertiary} accessibilityLabel="Project name" editable={!busy && !scopedReserved} />
						</View>
						<View style={styles.field}>
							<Text style={styles.label}>Default branch</Text>
							<TextInput {...textSelectionColors(t)} style={styles.input} value={defaultBranch} onChangeText={(value) => { manualDraftStarted.current = true; setDefaultBranch(value); }} placeholder="main" placeholderTextColor={t.textTertiary} autoCapitalize="none" autoCorrect={false} accessibilityLabel="Default branch" editable={!busy && !scopedReserved} />
						</View>
						{readOnly ? <Text style={styles.warning}>Your GitHub token can read this repository but cannot push changes. Workers may be unable to publish their work.</Text> : null}
						<Button title={readOnly ? "Continue anyway" : "Check repository access"} loading={busy} disabled={workspaceLoading} onPress={() => readOnly ? (setReadOnly(false), setStep("agents")) : void checkRepository()} />
						<Button title="Change GitHub token" variant="text" onPress={() => { manualDraftStarted.current = true; setError(null); setStep("github-token"); }} />
					</View>
				) : null}
				{displayedStep === "agents" && !scopedReserved?.operation.acceptedId ? (
					<View style={styles.group}>
						<Text style={styles.title}>Review project</Text>
						{selectedRepository ? <View style={styles.selectedSummary}><Feather name="book" size={iconSize.md} color={t.textSecondary} /><View style={styles.repositoryCopy}><Text style={styles.repositoryName}>{selectedRepository.fullName}</Text><Text style={styles.repositoryMeta}>Default branch · {selectedRepository.defaultBranch}</Text></View></View> : null}
						{selectedRepository ? <View style={styles.field}><Text style={styles.label}>Project name</Text><TextInput {...textSelectionColors(t)} style={styles.input} value={displayName} onChangeText={(value) => { setDisplayName(value); }} placeholder="Project name" placeholderTextColor={t.textTertiary} accessibilityLabel="Project name" editable={!busy && !scopedReserved} /></View> : null}
						<Text style={styles.description}>Uses your connected coding agents. Creating a project doesn’t start any sessions.</Text>
						{agentsLoading ? <View style={styles.connectedRow}><ActivityIndicator color={t.accent} accessibilityLabel="Loading coding agents" /><Text style={styles.hint}>{agents.length ? "Refreshing coding agents…" : "Loading coding agents…"}</Text></View> : null}
						{agentsError ? <Text accessibilityRole="alert" style={styles.error}>{agentsError}</Text> : null}
						{agents.length === 0 && !agentsLoading && !agentsError ? <Text style={styles.warning}>Connect a personal coding agent to continue. Your project draft will stay in place.</Text> : null}
						<Button title="Connect coding agents" variant="text" disabled={busy} onPress={() => { Keyboard.dismiss(); setConnectingAgents(true); }} />
						{agentsError || agents.length === 0 && !agentsLoading ? <Button title="Refresh connected agents" variant="text" disabled={busy || agentsLoading} onPress={refreshAgents} /> : null}
						{agents.length > 0 ? (
							<View style={styles.agentGroup}>
								<CloudAgentSelect label="Worker agent" value={workerAgent} options={agents.map((agent) => ({ value: agent, label: AGENT_LABELS[agent] ?? agent }))} onChange={setWorkerAgent} onOpen={() => openAgentPicker("worker")} enabled={!busy && !scopedReserved} />
								<CloudAgentSelect label="Orchestrator agent" value={orchestratorAgent} options={agents.map((agent) => ({ value: agent, label: AGENT_LABELS[agent] ?? agent }))} onChange={setOrchestratorAgent} onOpen={() => openAgentPicker("orchestrator")} enabled={!busy && !scopedReserved} separator />
							</View>
						) : null}
						{usesCoder ? <>
							<Text style={styles.label}>Workspace</Text>
							{computeLoading ? <ActivityIndicator color={t.accent} /> : null}
							{coderConfig ? <CloudChoice label="Coder template" value={templateId} options={[{ value: "", label: coderConfig.configured ? "Choose a template" : "Deployment default" }, ...coderTemplates.map((template) => ({ value: template.id, label: template.displayName || template.name }))]} onChange={(value) => { setTemplateId(value); setParameterValues({}); }} enabled={!busy && !scopedReserved} styles={styles} scheme={scheme} /> : null}
							{coderConfig?.configured ? <View style={styles.field}><Text style={styles.label}>Workspace name prefix</Text><TextInput {...textSelectionColors(t)} value={workspacePrefix} onChangeText={setWorkspacePrefix} editable={!busy && !scopedReserved} style={styles.input} autoCapitalize="none" autoCorrect={false} placeholder="ao" placeholderTextColor={t.textTertiary} accessibilityLabel="Workspace name prefix" /></View> : null}
							{parametersLoading ? <ActivityIndicator color={t.accent} /> : null}
							{parameters.map((parameter) => {
								const value = valueForParameter(parameter);
								const label = parameter.displayName || parameter.name;
								const update = (next: string) => setParameterValues((previous) => ({ ...previous, [parameter.name]: next }));
								if (/^(secret|token|accessToken|refreshToken|password|authorization|apiKey|privateKey)$/i.test(parameter.name)) return <Text key={parameter.name} style={styles.hint}>Configure {label} in desktop Settings; credentials are not collected here.</Text>;
								let selected: string[] = [];
								if (parameter.type === "list(string)") { try { const decoded = JSON.parse(value); if (Array.isArray(decoded)) selected = decoded; } catch {} }
								return <View key={parameter.name} style={styles.group}>
									{parameter.type === "list(string)" && parameter.options.length ? <><Text style={styles.label}>{label}</Text><Host matchContents={{ vertical: true }} colorScheme={scheme}><Column>{parameter.options.map((option) => <Checkbox key={option.value} label={option.name} value={selected.includes(option.value)} disabled={busy || Boolean(scopedReserved)} onValueChange={(checked) => update(JSON.stringify(checked ? [...new Set([...selected, option.value])] : selected.filter((item) => item !== option.value)))} />)}</Column></Host></>
									: parameter.type === "bool" || parameter.options.length ? <CloudChoice label={label} value={value} options={[...(value ? [] : [{ label: "Choose a value", value: "" }]), ...(parameter.type === "bool" ? [{ label: "True", value: "true" }, { label: "False", value: "false" }] : parameter.options.map((option) => ({ label: option.name, value: option.value })))]} onChange={update} enabled={!busy && !scopedReserved} styles={styles} scheme={scheme} />
									: <View style={styles.field}><Text style={styles.label}>{label}</Text><TextInput {...textSelectionColors(t)} style={styles.input} value={value} onChangeText={update} editable={!busy && !scopedReserved} autoCapitalize="none" autoCorrect={false} keyboardType={parameter.type === "number" ? "numbers-and-punctuation" : "default"} multiline={parameter.name === "startup_script"} accessibilityLabel={label} /></View>}
									{parameter.description ? <Text style={styles.hint}>{parameter.description}</Text> : null}
									{parameterErrors[parameter.name] ? <Text selectable accessibilityRole="alert" style={styles.error}>{parameterErrors[parameter.name]}</Text> : null}
								</View>;
							})}
							{computeProblem || prefixProblem ? <Text selectable accessibilityRole="alert" style={styles.warning}>{computeProblem ?? prefixProblem}</Text> : null}
							{computeError || parametersError ? <Button title="Retry Coder settings" variant="text" onPress={() => setComputeAttempt((value) => value + 1)} /> : null}
						</> : null}
						{!requestRestored && !restoreError ? <View style={styles.connectedRow}><ActivityIndicator color={t.accent} accessibilityLabel="Checking previous project requests" /><Text style={styles.hint}>Checking previous project requests…</Text></View> : null}
						{!scopedReserved ? <Button title="Create project" loading={busy} disabled={busy || !requestRestored || agents.length === 0 || !displayName.trim() || Boolean(computeProblem || prefixProblem || Object.keys(parameterErrors).length)} onPress={createProject} /> : null}
					</View>
				) : null}
				{error && displayedStep !== "unavailable" ? <Text selectable accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
				{refreshError ? <Text selectable accessibilityRole="alert" style={styles.warning}>{refreshError}</Text> : null}
				</>}
				</View>
			</ScrollView>
		</View>
	);
}

function CloudChoice({ label, value, options, onChange, enabled, styles, scheme }: {
	label: string; value: string; options: Array<{ value: string; label: string }>; onChange: (value: string) => void;
	enabled: boolean; styles: ReturnType<typeof makeStyles>; scheme: "dark" | "light";
}) {
	return <View style={styles.field}>
		<Text style={styles.label}>{label}</Text>
		<View style={styles.agentOption}>
			<Host style={{ flex: 1, minHeight: touchTarget }} matchContents={{ vertical: true }} colorScheme={scheme}>
				<Picker testID={label} selectedValue={value} onValueChange={onChange} enabled={enabled} appearance="menu">
					{options.map((option) => <Picker.Item key={option.value} value={option.value} label={option.label} />)}
				</Picker>
			</Host>
		</View>
	</View>;
}

function messageFor(cause: unknown, fallback: string): string {
	return cause instanceof Error && cause.message ? cause.message : fallback;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	header: { minHeight: touchTarget + space.xl, flexDirection: "row", alignItems: "center", paddingHorizontal: space.sm },
	headerTitle: { ...typography.title3, flex: 1, textAlign: "center", color: t.textPrimary },
	content: { paddingHorizontal: space.lg, paddingTop: space.xs, gap: space.lg },
	group: { gap: space.md },
	field: { gap: space.xs },
	connectCard: { gap: space.md, paddingTop: space.sm },
	connectHeading: { flexDirection: "row", alignItems: "center", gap: space.sm },
	connectTitle: { ...typography.subheadline, color: t.textPrimary },
	connectedRow: { flexDirection: "row", alignItems: "center", gap: space.sm, paddingVertical: space.xxs },
	connectedText: { ...typography.footnote, color: t.textSecondary, flexShrink: 1 },
	repositoryList: { borderRadius: radius.md, borderCurve: "continuous", overflow: "hidden", backgroundColor: t.bgElevated },
	repositoryTrigger: { minHeight: touchTarget + space.sm, paddingHorizontal: space.md, flexDirection: "row", alignItems: "center", gap: space.sm, borderRadius: radius.md, borderCurve: "continuous", backgroundColor: t.bgElevated },
	repositoryTriggerText: { ...typography.subheadline, flex: 1, color: t.textPrimary },
	repositorySearch: { minHeight: touchTarget + space.sm, flexDirection: "row", alignItems: "center", gap: space.sm, paddingHorizontal: space.md, borderRadius: radius.md, borderCurve: "continuous", backgroundColor: t.bgElevated },
	repositorySearchInput: { ...typography.subheadline, minHeight: touchTarget, flex: 1, minWidth: 0, color: t.textPrimary, paddingVertical: space.sm },
	repositoryOption: { minHeight: touchTarget + space.md, paddingHorizontal: space.md, paddingVertical: space.sm, flexDirection: "row", alignItems: "center", justifyContent: "space-between", borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: t.borderSubtle },
	repositorySelected: { backgroundColor: t.accentTint },
	repositoryIdentity: { flexDirection: "row", alignItems: "center", gap: space.md, flex: 1 },
	repositoryCopy: { flex: 1, gap: space.hair },
	repositoryName: { ...typography.subheadline, color: t.textPrimary },
	repositoryMeta: { ...typography.caption1, color: t.textTertiary },
	emptyText: { ...typography.footnote, color: t.textSecondary, padding: space.lg },
	selectedSummary: { flexDirection: "row", alignItems: "center", gap: space.md, padding: space.md, borderRadius: radius.md, borderCurve: "continuous", backgroundColor: t.bgElevated },
	title: { ...typography.headline, color: t.textPrimary },
	description: { ...typography.footnote, color: t.textSecondary },
	label: { ...typography.caption1, color: t.textSecondary },
	input: { ...typography.subheadline, minHeight: touchTarget + space.sm, paddingHorizontal: space.md, paddingVertical: space.sm, borderRadius: radius.md, borderCurve: "continuous", backgroundColor: t.bgElevated, color: t.textPrimary },
	hint: { ...typography.footnote, color: t.textSecondary },
	warning: { ...typography.footnote, color: t.amber, marginVertical: space.xs },
	error: { ...typography.footnote, color: t.red },
	agentGroup: { borderRadius: radius.md, borderCurve: "continuous", backgroundColor: t.bgElevated, overflow: "hidden" },
	agentOption: { minHeight: touchTarget + space.sm, paddingHorizontal: space.md, borderRadius: radius.md, borderCurve: "continuous", backgroundColor: t.bgElevated, flexDirection: "row", alignItems: "center", justifyContent: "space-between" },
});
