import { Feather as BrandFeather } from "@expo/vector-icons";
import { Checkbox, Column, Host, Picker } from "@expo/ui";
import { CloudApiError, type CoderTemplate, type CoderTemplateParameter, type CreateGitHubProjectInput, type CreateProjectInput, type GitHubInstallation, type GitHubRepository, type OrgCoderConfigResponse, type ProjectConfig } from "@aoagents/cloud-client";
import * as Crypto from "expo-crypto";
import { useRouter } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, Alert, AppState, Keyboard, Platform, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { backOr } from "../lib/backNavigation";
import { readyHarnesses } from "../lib/cloud/agentReadiness";
import { useCloudAuth } from "../lib/cloud/authStore";
import { CloudAgentSelect } from "../lib/cloud/cloud-agent-select";
import { loadGrantedRepositories } from "../lib/cloud/githubProjectRepositories";
import { createGitHubConnectionAttempt } from "../lib/cloud/githubConnection";
import { cloudAccountScopeKey } from "../lib/cloud/deployment";
import { offersProviderChoice } from "../lib/cloud/offering";
import { acceptCloudOperation, clearCloudOperation, prepareCloudOperation, readCloudOperationsForScope, reserveCloudOperation, type CloudOperation } from "../lib/cloud/operation";
import { cloudProjectInput, coderParameterProblem, coderSetupProblem, findCloudProjectForRepository, initialProjectCreationStep, normalizedGitHubRepository, validGitHubRepositoryURL } from "../lib/cloud/projectCreation";
import { haptics } from "../lib/haptics";
import { Feather } from "../lib/icons";
import { openGitHub } from "../lib/openGitHub";
import { useApp } from "../lib/store";
import type { Theme } from "../lib/theme";
import { useTheme, useThemeState, useThemedStyles } from "../lib/ThemeProvider";
import { textSelectionColors } from "../lib/textSelection";
import { Button, HeaderIconButton } from "../lib/ui";
import { fontScaleCap, iconSize, radius, sheetInset, space, touchTarget, type as typography } from "../lib/tokens";

export { SheetErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";

type Step = "loading" | "unavailable" | "github" | "github-token" | "repository" | "agents";
type ProjectRequest = { kind: "app"; repositoryUrl: string; input: CreateGitHubProjectInput } | { kind: "manual"; repositoryUrl: string; input: CreateProjectInput };
type ReservedProject = { slot: string; operation: CloudOperation<ProjectRequest> };

const AGENT_LABELS: Record<string, string> = {
	"claude-code": "Claude Code",
	codex: "Codex",
	cursor: "Cursor",
	opencode: "OpenCode",
};

export default function CreateCloudProject() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const insets = useSafeAreaInsets();
	const { refreshSource } = useApp();
	const auth = useCloudAuth();
	const { client, signedIn, orgId, orgLoading, orgError, retryOrgResolution, accountId, baseUrl, sessionEpoch, offering } = auth;
	const { scheme } = useThemeState();
	const scopeKey = accountId && orgId ? cloudAccountScopeKey({ baseUrl, userId: accountId, orgId }) : null;
	const manualDeployment = baseUrl.replace(/\/+$/, "") === "https://staging-api.aoagents.dev";
	const authRef = useRef(auth); authRef.current = auth;
	const mounted = useRef(true);
	const createAbort = useRef<AbortController | null>(null);
	const manualAbort = useRef<AbortController | null>(null);
	const navigationAck = useRef<ReservedProject | null>(null);
	const [reserved, setReserved] = useState<ReservedProject | null>(null);
	const [refreshError, setRefreshError] = useState<string | null>(null);
	const [configDefaults, setConfigDefaults] = useState<ProjectConfig>({});
	const [providerChoice, setProviderChoice] = useState("");
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
	const effectiveProvider = providerChoice && offering?.providers.includes(providerChoice) ? providerChoice : offering?.defaultProvider ?? "";
	const usesCoder = effectiveProvider === "coder";
	const [step, setStep] = useState<Step>("loading");
	const [setupScope, setSetupScope] = useState<string | null>(null);
	const [setupAttempt, setSetupAttempt] = useState(0);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [token, setToken] = useState("");
	const [repositoryUrl, setRepositoryUrl] = useState("");
	const [displayName, setDisplayName] = useState("");
	const [defaultBranch, setDefaultBranch] = useState("main");
	const [readOnly, setReadOnly] = useState(false);
	const [agents, setAgents] = useState<string[]>([]);
	const [workerAgent, setWorkerAgent] = useState("");
	const [orchestratorAgent, setOrchestratorAgent] = useState("");
	const [installations, setInstallations] = useState<GitHubInstallation[]>([]);
	const [repositories, setRepositories] = useState<GitHubRepository[]>([]);
	const [repositorySearch, setRepositorySearch] = useState("");
	const [repositoryPickerOpen, setRepositoryPickerOpen] = useState(false);
	const [selectedRepositoryId, setSelectedRepositoryId] = useState("");
	const [projectSource, setProjectSource] = useState<"app" | "manual">("app");
	const [githubError, setGithubError] = useState<string | null>(null);
	const [connecting, setConnecting] = useState(false);
	const [openingGitHub, setOpeningGitHub] = useState(false);
	const [loadingRepositories, setLoadingRepositories] = useState(false);
	const [hasSavedToken, setHasSavedToken] = useState(false);
	const connectAttempt = useRef(0);
	const connectionBaseline = useRef<Map<string, string> | null>(null);
	const requestAbort = useRef<AbortController | null>(null);
	const connectRequest = useRef<AbortController | null>(null);
	const installationStarting = useRef(false);
	const syncAttempted = useRef<Set<string>>(new Set());
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
	const displayedStep = step === "loading" || step === "unavailable" || setupScope === scopeKey ? step : "loading";
	const scopedReserved = reserved?.operation.scopeKey === scopeKey ? reserved : null;
	const valueForParameter = (parameter: CoderTemplateParameter) => parameterValues[parameter.name] ??
		(parameter.name === "size" ? configDefaults.coder?.size : parameter.name === "startup_script" ? configDefaults.coder?.startupScript : undefined) ??
		coderConfig?.coderConfig?.parameters?.[parameter.name] ?? parameter.defaultValue;

	useEffect(() => {
		mounted.current = true;
		return () => {
			mounted.current = false;
			createAbort.current?.abort();
			const completed = navigationAck.current;
			if (completed) void clearCloudOperation(completed.operation.scopeKey, completed.slot, completed.operation.key).catch(() => {});
		};
	}, []);
	useEffect(() => {
		manualAbort.current?.abort(); manualAbort.current = null;
		setRepositoryPickerOpen(false); setRepositorySearch("");
		setToken(""); setRepositoryUrl(""); setDisplayName(""); setDefaultBranch("main"); setReadOnly(false); setHasSavedToken(false); setBusy(false);
		return () => { createAbort.current?.abort(); createAbort.current = null; manualAbort.current?.abort(); manualAbort.current = null; };
	}, [scopeKey, sessionEpoch]);
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

	const refreshGitHub = useCallback(async (signal: AbortSignal) => {
		if (!orgId) return false;
		if (manualDeployment) return false;
		const nextInstallations = await client.listGitHubInstallations(orgId, { signal });
		if (signal.aborted) return false;
		setInstallations(nextInstallations);
		let active = nextInstallations.filter((installation) => installation.status === "active");
		let ready = active.some((installation) => installation.syncStatus === "ready");
		const needsSync = active.filter((installation) => installation.syncStatus === "pending" && !syncAttempted.current.has(installation.id));
		if (needsSync.length) {
			for (const installation of needsSync) {
				syncAttempted.current.add(installation.id);
				await client.syncGitHubInstallation(orgId, installation.id, { signal });
				if (signal.aborted) return false;
			}
				const updated = await client.listGitHubInstallations(orgId, { signal });
				if (signal.aborted) return false;
				setInstallations(updated);
				active = updated.filter((installation) => installation.status === "active");
				ready = active.some((installation) => installation.syncStatus === "ready");
		}
		const connectionReady = !installationStarting.current && ready && (connectionBaseline.current === null || active.some((installation) =>
			installation.syncStatus === "ready" && connectionBaseline.current?.get(installation.id) !== installation.updatedAt,
		));
		if (!signal.aborted && !installationStarting.current && active.length > 0) {
			if (connectionReady) {
				connectionBaseline.current = null;
				setConnecting(false);
			}
			else if (active.some((installation) => installation.syncStatus === "failed")) {
				connectionBaseline.current = null;
				setConnecting(false);
				setGithubError(active.find((installation) => installation.syncStatus === "failed")?.lastError ?? "GitHub repository sync failed. Try again.");
			} else setConnecting(true);
		}
		if (ready) {
			setLoadingRepositories(true);
			try {
				const granted = await loadGrantedRepositories(client, orgId, signal);
				if (!signal.aborted) {
					setRepositories(granted);
					setSelectedRepositoryId((previous) => granted.some((repository) => repository.githubRepositoryId === previous) ? previous : "");
					setGithubError(null);
				}
			} finally {
				if (!signal.aborted) setLoadingRepositories(false);
			}
		} else if (!signal.aborted) {
			setRepositories([]);
			setSelectedRepositoryId("");
		}
		return connectionReady;
	}, [client, orgId, manualDeployment]);

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
			setError(null);
			setStep("loading");
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
		let active = true;
		const abort = new AbortController();
		requestAbort.current?.abort();
		requestAbort.current = abort;
		syncAttempted.current.clear();
		setError(null);
		setGithubError(null);
		setProjectSource(manualDeployment ? "manual" : "app");
		setSelectedRepositoryId("");
		setRepositories([]);
		setInstallations([]);
		setConnecting(false);
		setOpeningGitHub(false);
		connectAttempt.current += 1;
		connectionBaseline.current = null;
		setStep("loading");
		setReserved(null);
		if (setupScope !== scopeKey) {
			setConfigDefaults({}); setProviderChoice(""); setTemplateId(""); setParameterValues({}); setWorkspacePrefix("");
		}
		Promise.allSettled([client.listUserProviderConnections({ signal: abort.signal }), refreshGitHub(abort.signal), scopeKey ? readCloudOperationsForScope<ProjectRequest>(scopeKey) : Promise.resolve([])]).then((results) => {
			if (!active) return;
			if (results[0].status === "rejected") {
				setError(messageFor(results[0].reason, "Could not load Cloud agent credentials."));
				setStep("unavailable");
				return;
			}
			const userConnections = results[0].value;
			setSetupScope(scopeKey);
			const harnesses = readyHarnesses(userConnections);
			setAgents(harnesses);
			setWorkerAgent(harnesses[0] ?? "");
			setOrchestratorAgent(harnesses[0] ?? "");
			setHasSavedToken(initialProjectCreationStep(userConnections) === "repository");
			if (results[2].status === "rejected") {
				setError(messageFor(results[2].reason, "Could not restore the previous Cloud request."));
				setStep("unavailable"); return;
			}
			const previous = results[2].value.find((record) => record.slot.startsWith("project-create/"));
			if (previous) {
				const request = previous.operation.payload;
				if (!request || !["app", "manual"].includes(request.kind) || !request.input || typeof request.input.displayName !== "string" || !validGitHubRepositoryURL(request.repositoryUrl)) {
					setError("The saved project request could not be understood. Check existing projects before starting another request.");
					setStep("unavailable"); return;
				}
				const config = request.input.config as ProjectConfig ?? {};
				setReserved(previous); setConfigDefaults(config);
				setProjectSource(request.kind);
				setDisplayName(request.input.displayName); setRepositoryUrl(request.repositoryUrl);
				if (request.kind === "manual") setDefaultBranch(request.input.defaultBranch);
				else setSelectedRepositoryId(request.input.githubRepositoryId);
				setWorkerAgent(config.worker?.agent ?? ""); setOrchestratorAgent(config.orchestrator?.agent ?? "");
				setProviderChoice(typeof config.sandboxProvider === "string" ? config.sandboxProvider : "");
				setTemplateId(config.coder?.templateId ?? ""); setParameterValues(config.coder?.parameters ?? {});
				setWorkspacePrefix(config.coder?.workspaceNamePrefix ?? "");
				setStep("agents");
			} else { setConfigDefaults({}); setStep(manualDeployment ? initialProjectCreationStep(userConnections) : "github"); }
			if (results[1].status === "rejected") {
				setGithubError(messageFor(results[1].reason, "Could not load GitHub repositories."));
			}
		}).catch((cause) => {
			if (!active) return;
			setError(messageFor(cause, "Could not load Cloud project setup."));
			setStep("github");
		});
		return () => { active = false; abort.abort(); };
	}, [client, signedIn, orgId, orgLoading, orgError, refreshGitHub, setupAttempt, scopeKey, sessionEpoch, manualDeployment]);

	useEffect(() => {
		if (connecting && orgId && step === "github") githubAttempt.start();
		else githubAttempt.stop();
	}, [connecting, orgId, step, githubAttempt]);

	const connectGitHub = async () => {
		if (!orgId || connecting || manualDeployment) return;
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
	};

	const syncRepositories = async () => {
		if (!orgId || busy) return;
		setBusy(true);
		setGithubError(null);
		try {
			const signal = requestAbort.current?.signal ?? new AbortController().signal;
			for (const installation of activeInstallations.filter((item) => item.syncStatus !== "ready")) {
				await client.syncGitHubInstallation(orgId, installation.id, { signal });
			}
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
		if (!scopeKey || !orgId || createAbort.current) return;
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
						throw new Error("Connect the project's personal default coding agents in desktop Settings, then retry this request.");
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
		if (busy || !scopeKey || !orgId) return;
		if (reserved) { await submitOperation(reserved); return; }
		if (projectSource === "app" && !selectedRepository) {
			setError("This repository is no longer available. Choose another repository.");
			setStep("github"); return;
		}
		if (!workerAgent || !orchestratorAgent) { setError("Connect a Cloud coding agent in desktop Settings first."); return; }
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
			...(offering?.source === "server" && effectiveProvider ? { sandboxProvider: effectiveProvider } : {}),
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

	const refreshAgents = async () => {
		setBusy(true); setError(null);
		const captured = auth;
		try {
			const available = readyHarnesses(await client.listUserProviderConnections({ signal: requestAbort.current?.signal }));
			if (!mounted.current || !captured.isCurrentSession()) return;
			setAgents(available);
			if (!reserved) {
				setWorkerAgent((value) => available.includes(value) ? value : available[0] ?? "");
				setOrchestratorAgent((value) => available.includes(value) ? value : available[0] ?? "");
			}
		} catch (cause) { if (mounted.current && captured.isCurrentSession()) setError(messageFor(cause, "Could not refresh connected agents.")); }
		finally { if (mounted.current && captured.isCurrentSession()) setBusy(false); }
	};

	const back = () => {
		haptics.tap();
		if (step === "github" && repositoryPickerOpen) { setRepositoryPickerOpen(false); setRepositorySearch(""); Keyboard.dismiss(); return; }
		if (step === "agents") { setError(null); setStep(projectSource === "app" ? "github" : "repository"); }
		else if (step === "github-token" || step === "repository") { setError(null); setStep("github"); }
		else backOr(router);
	};

	return (
		<View style={styles.screen} collapsable={false}>
			<View style={styles.header}>
				<HeaderIconButton icon="back" label="Back" onPress={back} />
				<Text maxFontSizeMultiplier={fontScaleCap.title} style={styles.headerTitle}>Add Cloud project</Text>
				<HeaderIconButton icon="close" label="Close" onPress={() => backOr(router)} />
			</View>
			<ScrollView contentInsetAdjustmentBehavior="automatic" automaticallyAdjustKeyboardInsets={Platform.OS === "ios"} keyboardShouldPersistTaps="handled" contentContainerStyle={{ paddingBottom: insets.bottom + sheetInset.bottom }}>
				<View style={styles.content}>
				{scopedReserved ? <View style={styles.group}>
					<Text style={styles.title}>{scopedReserved.operation.acceptedId ? "Project created" : "Finish your previous request"}</Text>
					<Text selectable style={styles.description}>{scopedReserved.operation.payload.input.displayName}{scopedReserved.operation.acceptedId ? " is ready to open." : " has a saved request. Retry keeps the same project settings and request key."}</Text>
					<Button title={scopedReserved.operation.acceptedId ? "Open project" : "Retry creation"} loading={busy} disabled={busy || (!scopedReserved.operation.acceptedId && Boolean(computeProblem || prefixProblem || Object.keys(parameterErrors).length))} onPress={() => submitOperation(scopedReserved)} />
					{!scopedReserved.operation.acceptedId ? <Button title="Check existing projects" variant="text" disabled={busy} onPress={() => submitOperation(scopedReserved, true)} /> : null}
					<Button title="Discard retry record" variant="text" disabled={busy} onPress={discardReserved} />
				</View> : null}
				{displayedStep === "loading" ? <ActivityIndicator color={t.accent} style={styles.loading} /> : null}
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
						{activeInstallations.length === 0 ? (
							<View style={styles.connectCard}>
								<View style={styles.connectHeading}><BrandFeather name="github" size={iconSize.lg} color={t.textPrimary} /><Text style={styles.connectTitle}>Connect GitHub</Text></View>
								<Text style={styles.hint}>GitHub will open in a browser so you can choose which repositories AO may access. No token to copy or paste.</Text>
								<Button title={openingGitHub ? "Opening GitHub…" : connecting ? "Waiting for GitHub approval…" : "Connect GitHub"} loading={connecting} disabled={connecting} onPress={() => void connectGitHub()} />
								{connecting ? <Button title="Stop waiting" variant="text" onPress={stopWaiting} /> : null}
							</View>
						) : (
							<>
								<View style={styles.connectedRow}>
									<Feather name={githubReadyToImport ? "check-circle" : connecting ? "loader" : "alert-circle"} size={iconSize.md} color={githubReadyToImport ? t.green : t.amber} />
									<Text style={styles.connectedText}>{githubStatusText}</Text>
								</View>
								{activeInstallations.some((item) => item.syncStatus !== "ready") ? <Button title={connecting ? "Syncing repositories…" : "Retry repository sync"} variant="text" loading={busy} disabled={connecting} onPress={() => void syncRepositories()} /> : null}
								{connecting ? <Button title="Stop waiting" variant="text" onPress={stopWaiting} /> : null}
								{loadingRepositories ? <ActivityIndicator color={t.accent} /> : null}
								{repositories.length > 0 ? (
									<>
										<View style={styles.field}>
											<Text style={styles.label}>Repository</Text>
											<Pressable accessibilityRole="combobox" accessibilityLabel="Select a repository" accessibilityValue={{ text: selectedRepository?.fullName ?? "None selected" }} accessibilityState={{ expanded: repositoryPickerOpen, disabled: busy || !githubReadyToImport || loadingRepositories || Boolean(scopedReserved) }} disabled={busy || !githubReadyToImport || loadingRepositories || Boolean(scopedReserved)}
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
												<TextInput {...textSelectionColors(t)} style={styles.repositorySearchInput} value={repositorySearch} onChangeText={setRepositorySearch} placeholder="Search repositories" placeholderTextColor={t.textSecondary} autoFocus autoCapitalize="none" autoCorrect={false} accessibilityLabel="Search repositories" editable={!busy && githubReadyToImport && !loadingRepositories && !scopedReserved} />
											</View>
											{!repositorySearch.trim() ? <Text style={styles.hint}>Search by repository name.</Text> : <View style={styles.repositoryList}>
												{visibleRepositories.length === 0 ? <Text style={styles.emptyText}>No matching repositories</Text> : shownRepositories.map((repository) => (
													<Pressable key={repository.githubRepositoryId} accessibilityRole="radio" accessibilityState={{ selected: selectedRepositoryId === repository.githubRepositoryId }} accessibilityLabel={`Select ${repository.fullName}`} disabled={busy || !githubReadyToImport || loadingRepositories || Boolean(scopedReserved)} onPress={() => { if (busy || !githubReadyToImport || loadingRepositories || scopedReserved) return; haptics.select(); setProjectSource("app"); setSelectedRepositoryId(repository.githubRepositoryId); setDisplayName(repository.name); setRepositoryUrl(repository.htmlUrl); setDefaultBranch(repository.defaultBranch); setRepositoryPickerOpen(false); setRepositorySearch(""); Keyboard.dismiss(); }} style={[styles.repositoryOption, selectedRepositoryId === repository.githubRepositoryId && styles.repositorySelected]}>
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
						)}
						{githubError ? <Text accessibilityRole="alert" style={styles.error}>{githubError}</Text> : null}
						<Button title="Set up manually with a token" variant="text" onPress={() => { setError(null); stopWaiting(); setProjectSource("manual"); setSelectedRepositoryId(""); setRepositoryUrl(""); setDisplayName(""); setStep(hasSavedToken ? "repository" : "github-token"); }} />
					</View>
				) : null}
				{displayedStep === "github-token" ? (
					<View style={styles.group}>
						<Text style={styles.title}>Connect GitHub</Text>
						<Text style={styles.description}>For manual setup, add a fine-grained token with access to the repository. Orchestrator Cloud stores it securely and never shows it again.</Text>
						<View style={styles.field}>
							<Text style={styles.label}>GitHub access token</Text>
							<TextInput {...textSelectionColors(t)} style={styles.input} value={token} onChangeText={setToken} placeholder="github_pat_…" placeholderTextColor={t.textTertiary} secureTextEntry autoCapitalize="none" autoCorrect={false} textContentType="password" accessibilityLabel="GitHub access token" />
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
							<TextInput {...textSelectionColors(t)} style={styles.input} value={repositoryUrl} onChangeText={(value) => { setRepositoryUrl(value); setReadOnly(false); }} placeholder="https://github.com/owner/repo" placeholderTextColor={t.textTertiary} autoCapitalize="none" autoCorrect={false} keyboardType="url" accessibilityLabel="Repository URL" editable={!busy && !scopedReserved} />
						</View>
						<View style={styles.field}>
							<Text style={styles.label}>Project name</Text>
							<TextInput {...textSelectionColors(t)} style={styles.input} value={displayName} onChangeText={(value) => { setDisplayName(value); }} placeholder="My project" placeholderTextColor={t.textTertiary} accessibilityLabel="Project name" editable={!busy && !scopedReserved} />
						</View>
						<View style={styles.field}>
							<Text style={styles.label}>Default branch</Text>
							<TextInput {...textSelectionColors(t)} style={styles.input} value={defaultBranch} onChangeText={(value) => { setDefaultBranch(value); }} placeholder="main" placeholderTextColor={t.textTertiary} autoCapitalize="none" autoCorrect={false} accessibilityLabel="Default branch" editable={!busy && !scopedReserved} />
						</View>
						{readOnly ? <Text style={styles.warning}>Your GitHub token can read this repository but cannot push changes. Workers may be unable to publish their work.</Text> : null}
						<Button title={readOnly ? "Continue anyway" : "Check repository access"} loading={busy} onPress={() => readOnly ? (setReadOnly(false), setStep("agents")) : void checkRepository()} />
						<Button title="Change GitHub token" variant="text" onPress={() => { setError(null); setStep("github-token"); }} />
					</View>
				) : null}
				{displayedStep === "agents" && !scopedReserved?.operation.acceptedId ? (
					<View style={styles.group}>
						<Text style={styles.title}>Review project</Text>
						{selectedRepository ? <View style={styles.selectedSummary}><Feather name="book" size={iconSize.md} color={t.textSecondary} /><View style={styles.repositoryCopy}><Text style={styles.repositoryName}>{selectedRepository.fullName}</Text><Text style={styles.repositoryMeta}>Default branch · {selectedRepository.defaultBranch}</Text></View></View> : null}
						{selectedRepository ? <View style={styles.field}><Text style={styles.label}>Project name</Text><TextInput {...textSelectionColors(t)} style={styles.input} value={displayName} onChangeText={(value) => { setDisplayName(value); }} placeholder="Project name" placeholderTextColor={t.textTertiary} accessibilityLabel="Project name" editable={!busy && !scopedReserved} /></View> : null}
						<Text style={styles.description}>Uses your connected coding agents. Creating a project doesn’t start any sessions.</Text>
						{agents.length === 0 ? <><Text style={styles.warning}>Connect a personal default coding agent in desktop Settings, then refresh here. Your project draft will stay in place.</Text><Button title="Refresh connected agents" variant="text" disabled={busy} onPress={refreshAgents} /></> : (
							<View style={styles.agentGroup}>
								<CloudAgentSelect label="Worker agent" value={workerAgent} options={agents.map((agent) => ({ value: agent, label: AGENT_LABELS[agent] ?? agent }))} onChange={setWorkerAgent} enabled={!busy && !scopedReserved} />
								<CloudAgentSelect label="Orchestrator agent" value={orchestratorAgent} options={agents.map((agent) => ({ value: agent, label: AGENT_LABELS[agent] ?? agent }))} onChange={setOrchestratorAgent} enabled={!busy && !scopedReserved} separator />
							</View>
						)}
						{offersProviderChoice(offering ?? undefined) ? <CloudChoice label="Cloud provider" value={effectiveProvider} options={offering!.providers.map((provider) => ({ value: provider, label: provider === "coder" ? "Coder" : provider === "freestyle" ? "Freestyle" : provider }))} onChange={setProviderChoice} enabled={!busy && !scopedReserved} styles={styles} scheme={scheme} /> : null}
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
						{!scopedReserved ? <Button title="Create project" loading={busy} disabled={busy || agents.length === 0 || !displayName.trim() || Boolean(computeProblem || prefixProblem || Object.keys(parameterErrors).length)} onPress={createProject} /> : null}
					</View>
				) : null}
				{error && displayedStep !== "unavailable" ? <Text selectable accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
				{refreshError ? <Text selectable accessibilityRole="alert" style={styles.warning}>{refreshError}</Text> : null}
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
	loading: { marginTop: space.huge },
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
