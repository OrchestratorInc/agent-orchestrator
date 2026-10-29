import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
	TaskComposerView,
	type TaskComposerAgentControl,
	type TaskComposerEffortControl,
	type TaskComposerModelControl,
	type TaskComposerModelCatalog,
} from "@aoagents/product-ui";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { RequiredAgentField } from "./CreateProjectAgentSheet";
import type { ApprovalMode, ChatModel, TurnSettings } from "../types/conversation";
import { AGENT_OPTIONS, agentLabel } from "../lib/agent-options";
import type { components } from "../../api/schema";
import { apiClient, apiErrorCode, apiErrorMessage } from "../lib/api-client";
import { clientForHost } from "../lib/host-clients";
import { useConnectedHosts } from "../hooks/useHostConnection";
import { captureRendererEvent } from "../lib/telemetry";
import { CloudCpError, type CloudCpAgentProvider, type CloudCpChatModelsResponse } from "../lib/cloud-cp";
import {
	cacheAgentReadiness,
	ensureAgentReadiness,
	useAgentReadinessQuery,
} from "../hooks/useAgentReadinessQuery";
import { type FileAttachmentPayload, useFileAttachments } from "../hooks/useFileAttachments";
import { useSettings } from "../hooks/useSettings";
import { useOfferedModels } from "../hooks/useOfferedModels";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { useCloudOrg } from "../hooks/useCloudOrg";
import { useProviderConnections } from "../hooks/useProviderConnections";
import { CLOUD_AGENT_PROVIDERS, cloudAgentInfos, connectedCredentialType, credentialModelScope } from "../lib/cloud-agents";
import { createCloudSession } from "../lib/cloud-session-create";
import { isConcreteModelID, modelChoiceLabel, splitClaudeModels } from "../lib/agent-model-choices";
import { collapseModelVariants, findModelVariant, listsModelVariants, pickModelVariant } from "../lib/model-variants";
import { ToggleRow } from "./chat/AgentSwitchPicker";
import { fallbackEffort } from "../lib/effort";
import {
	buildRankedAgentOptions,
	DEFAULT_AGENT_PRIORITY_RANK,
	isLaunchableAgent,
	isReadyAgent,
	unknownAgentReadiness,
	type AgentInfo,
} from "../lib/agent-select-options";
import { cloudSessionsQueryKey, seedCreatedFreestyleSession, useCloudProjectsQuery } from "../hooks/useWorkspaceQuery";
import {
	agentModelsQueryKey,
	agentModelsQueryOptions,
	modelCatalogAuthIssue,
	refreshAgentModels,
	revalidateAgentModels,
} from "../hooks/useAgentModelsQuery";
import { useModelCatalogAuthRecovery } from "../hooks/useModelCatalogAuthRecovery";
import { useUiStore } from "../stores/ui-store";
import { ModelCatalogNotice } from "./ModelCatalogNotice";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { type EffortAvailability } from "./settings/EffortPicker";
import { COMPOSER_MENU_WIDTH, TaskEffortPicker, TaskModelPicker } from "./TaskModelControls";
import { useModelTuning } from "./settings/ModelTuningControls";
import {
	readTaskComposerPreferences,
	rememberTaskComposerPreference,
	type TaskComposerAgentPreference,
} from "../lib/task-composer-preferences";

type Project = components["schemas"]["Project"];
type DelegateAgent = components["schemas"]["DelegateTaskRequest"]["agent"];
type AgentModelOption = components["schemas"]["AgentModelInfo"];

const NO_MODELS: AgentModelOption[] = [];

type CreateTaskInput = {
	clientRequestId?: string;
	projectId: string;
	brief: string;
	agent?: DelegateAgent;
	model?: string;
	effort?: string;
	mode?: "chat" | "tui";
	approvalMode?: ApprovalMode;
	attachments?: FileAttachmentPayload[];
	taskPreparation?: string;
};

const CHAT_PREFLIGHT_CODES = new Set([
	"SESSION_MODE_UNSUPPORTED",
	"CHAT_DRIVER_UNAVAILABLE",
	"CHAT_DRIVER_INCOMPATIBLE",
	"CHAT_AUTH_REQUIRED",
]);

const READINESS_RECONCILE_CODES = new Set(["AGENT_BINARY_NOT_FOUND", "AGENT_AUTH_REQUIRED", "CHAT_AUTH_REQUIRED"]);

function cancelTaskPreparation(token: string, hostId?: string): void {
	if (!token) return;
	try {
		void (hostId ? clientForHost(hostId) : apiClient).DELETE("/api/v1/task-preparations/{token}", {
			params: { path: { token } },
		}).catch(() => { /* The host reclaims abandoned preparations after their TTL. */ });
	} catch {
		// A disconnected host will reclaim this preparation on its own TTL.
	}
}

class TaskCreateError extends Error {
	constructor(
		message: string,
		readonly code?: string,
		readonly details?: components["schemas"]["APIError"]["details"],
	) {
		super(message);
		this.name = "TaskCreateError";
	}
}

type FallbackAction = "tui" | "bypass-permissions";

function hasErrorDetail(details: components["schemas"]["APIError"]["details"] | undefined, key: string, value: string) {
	const values = details?.[key];
	return Array.isArray(values) && values.includes(value);
}

export type TaskComposerProps = {
	projectId?: string;
	hostId?: string;
	onCreated: (sessionId: string) => void;
	onDirtyChange?: (dirty: boolean) => void;
	onSubmittingChange?: (submitting: boolean) => void;
	autoFocusTitle?: boolean;
	createLabel?: string;
};

export type TaskLaunchOptions = {
	interfaceMode?: "chat" | "tui";
	approvalMode?: ApprovalMode;
	/** Idempotency key; a retry with the same key cannot create a second session. */
	clientRequestId?: string;
	/** Send exactly this request instead of the current picks, e.g. a retry of a send whose response was lost. */
	replay?: LaunchReplay;
	/** Files to upload with the task, in place of the ones this hook holds. */
	attachments?: FileAttachmentPayload[];
};

/** The agent, model and permission parts of a create request, saved so a retry can repeat them exactly. */
export type LaunchReplay = {
	agent?: string;
	model?: string;
	effort?: string;
	approvalMode?: ApprovalMode;
	mode?: "chat" | "tui";
};

/**
 * The New Task launch logic: which project, agent, model, and effort a task runs
 * with, and the create call for local, remote, cloud, and standalone targets.
 * The new chat page and TaskComposer render it.
 */
export function useTaskLaunch({ projectId, hostId }: { projectId?: string; hostId?: string }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const remoteConnections = useConnectedHosts();
	const hostConnected = !hostId || remoteConnections.includes(hostId);
	const [model, setModel] = useState("");
	const [mode, setMode] = useState("");
	const [effort, setEffort] = useState("");
	const [agent, setAgent] = useState("");
	const [agentTouched, setAgentTouched] = useState(false);
	const [modelTouched, setModelTouched] = useState(false);
	const [effortTouched, setEffortTouched] = useState(false);
	// Only a permission the user picks is sent; otherwise the project's own applies.
	const [approvalMode, setApprovalMode] = useState<ApprovalMode>();
	const [isSubmitting, setIsSubmitting] = useState(false);
	const [error, setError] = useState<string | undefined>();
	const [fallbackAction, setFallbackAction] = useState<FallbackAction>();
	const taskPreparationRef = useRef("");
	// The preparation a send in flight will claim. Leaving the page or project does not
	// cancel it; the send does, once answered.
	const launchingPreparationRef = useRef("");
	// The prepared worktree's session id. It becomes the task's session id on send,
	// so files uploaded into it before then are already where the agent reads them.
	const [preparedSessionId, setPreparedSessionId] = useState("");
	const requestRef = useRef<{ payload: string; id: string } | undefined>(undefined);
	const {
		attachments,
		error: attachmentError,
		addFiles,
		remove: removeAttachment,
		clear: clearAttachments,
		toSettledPayload,
	} = useFileAttachments();
	// Cloud vs local is decided here and nowhere else: a cloud project routes task
	// creation to the control plane (which provisions a sandbox), while a local
	// project keeps the existing daemon flow untouched.
	const { client: cloudClient, baseUrl: cloudBaseUrl, userId: cloudUserId } = useCloudCp();
	const { org: cloudOrg } = useCloudOrg();
	const cloudOffering = useCloudOffering();
	const cloudProjects = useCloudProjectsQuery();
	const cloudProject = (cloudProjects.data ?? []).find((project) => project.id === projectId);
	const isCloudProject = !hostId && Boolean(cloudProject);
	const isStandalone = projectId === STANDALONE_WORKSPACE_ID;
	const preferenceContext = hostId ? `${hostId}:${projectId ?? ""}` : projectId ?? "";
	const persistedPreferences = useMemo(
		() => readTaskComposerPreferences(preferenceContext),
		[preferenceContext],
	);
	const agentDrafts = useRef<Record<string, TaskComposerAgentPreference>>({
		...persistedPreferences?.agents,
	}).current;
	const createCloudTask = useCallback(
		async (input: CreateTaskInput): Promise<string> => {
			try {
				if (input.attachments?.length) throw new Error(t("newTask.cloudAttachmentsUnsupported", { defaultValue: "File attachments are not supported for cloud tasks yet." }));
				if (!cloudOrg?.id) throw new Error(t("newTask.unableToStart"));
				// No provider: the control plane runs the session on the project's
				// stored provider (config.sandboxProvider), else the org's default.
				// The task starts in the interface the composer resolved (its choice,
				// else the default session interface); an older control plane that
				// predates the field starts it in the terminal (createCloudSession).
				const { session } = await createCloudSession(cloudClient, cloudOrg.id, {
					projectId: input.projectId,
					kind: "worker",
					harness: input.agent ?? "claude-code",
					displayName: input.brief.trim().slice(0, 100) || (input.agent ?? "claude-code"),
					prompt: input.brief,
					...(input.model ? { model: input.model } : {}),
					...(input.effort ? { reasoningEffort: input.effort } : {}),
					...(input.mode ? { interfaceMode: input.mode } : {}),
				}, input.clientRequestId ? { idempotencyKey: input.clientRequestId } : undefined);
				// The control plane provisions the sandbox asynchronously; surface the
				// new session on the board immediately.
				seedCreatedFreestyleSession(queryClient, cloudBaseUrl, cloudOrg.id, session);
				void queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
				void captureRendererEvent("ao.renderer.task_create_succeeded", { project_id: input.projectId });
				return session.id;
			} catch (err) {
				void captureRendererEvent("ao.renderer.task_create_failed", { project_id: input.projectId });
				// The code drives the same fallbacks a local create error does ("Create as Terminal").
				if (err instanceof CloudCpError) throw new TaskCreateError(err.message, err.code);
				throw err instanceof Error ? err : new Error(t("newTask.unableToStart"));
			}
		},
		[cloudBaseUrl, cloudClient, cloudOrg, queryClient, t],
	);

	const createDaemonTask = useCallback(
		async (input: CreateTaskInput): Promise<string> => {
			try {
				const { data, error } = await (hostId ? clientForHost(hostId) : apiClient).POST("/api/v1/orchestrators/delegate", {
					headers: input.attachments?.length ? { "X-AO-Attachment-Upload": "1" } : undefined,
					body: {
						clientRequestId: input.clientRequestId,
						projectId: input.projectId,
						brief: input.brief,
						agent: input.agent,
						...(input.model ? { model: input.model } : {}),
						...(input.effort !== undefined ? { effort: input.effort } : {}),
						...(input.mode ? { mode: input.mode } : {}),
						...(input.approvalMode ? { approvalMode: input.approvalMode } : {}),
						...(input.attachments && input.attachments.length > 0 ? { attachments: input.attachments } : {}),
						...(input.taskPreparation ? { taskPreparation: input.taskPreparation } : {}),
					},
				});
				if (error) {
					throw new TaskCreateError(
						apiErrorMessage(error, t("newTask.unableToStart")),
						apiErrorCode(error),
						error.details,
					);
				}
				if (!data?.workerId) throw new Error(t("newTask.noSession"));
				void captureRendererEvent("ao.renderer.task_create_succeeded", { project_id: input.projectId });
				return data.workerId;
			} catch (err) {
				void captureRendererEvent("ao.renderer.task_create_failed", { project_id: input.projectId });
				if (
					err instanceof TaskCreateError &&
					err.code &&
					READINESS_RECONCILE_CODES.has(err.code) &&
					input.agent
				) {
					try {
						cacheAgentReadiness(queryClient, await ensureAgentReadiness([input.agent], "launch", hostId), hostId);
					} catch {
						// Preserve the launch error when opportunistic reconciliation fails.
					}
				}
				throw err instanceof Error ? err : new Error(t("newTask.unableToStart"));
			}
		},
		[hostId, queryClient, t],
	);

	const createStandaloneTask = useCallback(
		async (input: CreateTaskInput): Promise<string> => {
			try {
			const displayName = input.brief.trim().slice(0, 100) || input.agent || "Standalone agent";
			const { data, error } = await (hostId ? clientForHost(hostId) : apiClient).POST("/api/v1/sessions", {
				headers: input.attachments?.length ? { "X-AO-Attachment-Upload": "1" } : undefined,
				body: {
					clientRequestId: input.clientRequestId,
					kind: "worker",
					harness: input.agent as components["schemas"]["SpawnSessionRequest"]["harness"],
					prompt: input.brief,
					displayName,
					model: input.model,
					...(input.effort ? { effort: input.effort } : {}),
					...(input.mode ? { mode: input.mode } : {}),
					...(input.approvalMode ? { approvalMode: input.approvalMode } : {}),
					...(input.attachments && input.attachments.length > 0 ? { attachments: input.attachments } : {}),
				},
			});
			if (error) {
				throw new TaskCreateError(apiErrorMessage(error, t("newTask.unableToStart")), apiErrorCode(error), error.details);
			}
			if (!data?.session.id) throw new Error(t("newTask.noSession"));
			void captureRendererEvent("ao.renderer.task_create_succeeded", { scope: "standalone" });
			return data.session.id;
			} catch (err) {
				void captureRendererEvent("ao.renderer.task_create_failed", { scope: "standalone" });
				throw err instanceof Error ? err : new Error(t("newTask.unableToStart"));
			}
		},
		[hostId, t],
	);

	const createTask = useCallback(
		(input: CreateTaskInput): Promise<string> =>
			isStandalone ? createStandaloneTask(input) : isCloudProject ? createCloudTask(input) : createDaemonTask(input),
		[isStandalone, isCloudProject, createStandaloneTask, createCloudTask, createDaemonTask],
	);

	const projectQuery = useQuery({
		// A cloud project lives in the control plane, not the local daemon, so this
		// local lookup would 404 (PROJECT_NOT_FOUND); skip it for cloud projects.
		queryKey: hostId ? ["project", hostId, projectId] : ["project", projectId],
		enabled: Boolean(projectId) && !isCloudProject && !isStandalone,
		queryFn: async () => {
			const { data, error: apiError } = await (hostId ? clientForHost(hostId) : apiClient).GET("/api/v1/projects/{id}", {
				params: { path: { id: projectId ?? "" } },
			});
			if (apiError) throw new Error(apiErrorMessage(apiError));
			if (data?.status !== "ok") throw new Error(t("newTask.configUnavailable"));
			return data.project as Project;
		},
	});
	useEffect(() => {
		const id = projectQuery.data?.id;
		if (!id) return;
		let client;
		try {
			client = hostId ? clientForHost(hostId) : apiClient;
		} catch {
			return;
		}
		let disposed = false;
		taskPreparationRef.current = "";
		setPreparedSessionId("");
		void client.POST("/api/v1/projects/{id}/tasks/prepare", {
			params: { path: { id } },
		}).then(
			({ data }) => {
				const token = data?.taskPreparation ?? "";
				if (!token) return;
				if (disposed) {
					cancelTaskPreparation(token, hostId);
					return;
				}
				taskPreparationRef.current = token;
				setPreparedSessionId(token);
			},
			() => undefined,
		);
		return () => {
			disposed = true;
			const token = taskPreparationRef.current;
			taskPreparationRef.current = "";
			setPreparedSessionId("");
			if (token !== launchingPreparationRef.current) cancelTaskPreparation(token, hostId);
		};
	}, [hostId, projectQuery.data?.id]);
	const agentsQuery = useAgentReadinessQuery(true, hostId);
	const { settings, error: settingsError } = useSettings(hostId);
	// The composer preselects the agent and model a spawn would actually use
	// instead of parking the controls on a "default" label the user has to
	// remember. Both resolved values remain directly editable.
	// A cloud project is unknown to the local daemon, so the local projectQuery
	// above is disabled for it; its config lives in the control-plane projects
	// list (already fetched as cloudProjects). Reading the worker defaults from
	// the disabled local query is what made a cloud worker ignore
	// config.worker.agent and fall back to claude-code.
	const projectConfig = (isCloudProject ? cloudProject?.config : projectQuery.data?.config) as
		| {
				worker?: { agent?: string; agentConfig?: { model?: string; mode?: string; effort?: string; permissions?: string } };
				agentConfig?: { model?: string; mode?: string; effort?: string; permissions?: string };
		  }
		| undefined;
	const projectWorkerAgent = projectConfig?.worker?.agent ?? "";
	const globalDefaultAgent = projectQuery.data?.agent ?? "";
	const configuredProjectAgent = projectWorkerAgent || globalDefaultAgent;
	const agentCatalog = agentsQuery.data;
	// Cloud projects support the control-plane agents listed in CLOUD_AGENT_PROVIDERS
	// (the single source), with readiness derived from the user's provider connections.
	const cloudConnectionsQuery = useProviderConnections();
	const cloudAgents = useMemo(() => cloudAgentInfos(cloudConnectionsQuery.data), [cloudConnectionsQuery.data]);
	const standaloneDefaultAgent = useMemo(() => {
		if (!isStandalone || !agentCatalog) return "";
		return (
			buildRankedAgentOptions({
				agents: agentCatalog.agents,
				priorityRank: DEFAULT_AGENT_PRIORITY_RANK,
				fallbackAgents: [],
			}).find(isReadyAgent)?.id ?? ""
		);
	}, [agentCatalog, isStandalone]);
	const configuredDefaultAgent = isStandalone
		? standaloneDefaultAgent
		: configuredProjectAgent;
	const rememberedAgent = persistedPreferences?.lastAgent ?? "";
	const availableAgents = isCloudProject ? cloudAgents : agentCatalog?.agents;
	const rememberedAgentIsAvailable = Boolean(
		availableAgents?.some((candidate) => candidate.id === rememberedAgent && (hostId ? isLaunchableAgent(candidate) : isReadyAgent(candidate))),
	);
	const defaultWorkerAgent = rememberedAgentIsAvailable ? rememberedAgent : configuredDefaultAgent;
	const selectedAgent = agent || defaultWorkerAgent;
	// The connection a cloud task with this agent runs on; its catalog follows it.
	const selectedCloudConnection = isCloudProject
		? cloudConnectionsQuery.data?.find(
				(connection) => connection.provider === selectedAgent && connection.label === "default" && connection.validationState === "valid",
			)
		: undefined;
	// Model catalogs are device-wide, so every local project reads the one catalog
	// per agent. opencode in a cloud project is the exception: its catalog depends
	// on the connected provider, so scope the query by that credential type
	// (credentialModelScope) to show the models the cloud VM will actually run.
	const modelScope = useMemo(() => {
		if (isCloudProject && selectedAgent === "opencode") {
			const credentialType = connectedCredentialType(cloudConnectionsQuery.data, "opencode");
			if (credentialType !== "") return credentialModelScope(credentialType);
		}
		return "";
	}, [isCloudProject, selectedAgent, cloudConnectionsQuery.data]);
	const defaultWorkerModel =
		projectConfig?.worker?.agentConfig?.model ?? projectConfig?.agentConfig?.model ?? "";
	const defaultWorkerMode = projectConfig?.worker?.agentConfig?.mode ?? projectConfig?.agentConfig?.mode ?? "";
	const defaultWorkerEffort =
		projectConfig?.worker?.agentConfig?.effort ?? projectConfig?.agentConfig?.effort ?? "";
	const projectModelForSelectedAgent = selectedAgent === configuredProjectAgent
		? defaultWorkerModel
		: "";
	const projectModeForSelectedAgent = selectedAgent === configuredProjectAgent
		? defaultWorkerMode
		: "";
	const rememberedConfigForSelectedAgent = agentDrafts[selectedAgent];
	const rememberedModel = rememberedConfigForSelectedAgent?.model ?? "";
	const rememberedMode = rememberedConfigForSelectedAgent?.mode ?? "";
	// A cloud task runs the control plane's catalog for its agent: the models a
	// Cloud worker on the same CLI version and credential reported, or the ones
	// its pinned CLI release supports. Neither depends on this computer's login.
	const cloudCatalogAgent = isCloudProject && (CLOUD_AGENT_PROVIDERS as readonly string[]).includes(selectedAgent);
	const cloudModelCatalogQuery = useQuery({
		queryKey: [
			"cloud-agent-models",
			cloudBaseUrl,
			cloudUserId,
			cloudOrg?.id ?? "",
			selectedAgent,
			selectedCloudConnection?.id ?? "",
			selectedCloudConnection?.updatedAt ?? "",
		],
		queryFn: ({ signal }): Promise<CloudCpChatModelsResponse> =>
			cloudClient.listUserAgentModels(cloudOrg?.id ?? "", selectedAgent as CloudCpAgentProvider, { signal }),
		// Asked once the connections are known, so their arrival does not ask twice.
		enabled: cloudCatalogAgent && Boolean(cloudOrg?.id) && (cloudConnectionsQuery.data !== undefined || cloudConnectionsQuery.isError),
		staleTime: 5 * 60_000,
		refetchOnWindowFocus: false,
		refetchOnReconnect: false,
		retry: false,
	});
	const cloudCatalog = cloudCatalogAgent ? cloudModelCatalogQuery.data : undefined;
	// Until the catalog arrives no listed model vouches for the effort, so a task
	// does not start yet with a level its model refuses.
	const cloudCatalogLoading = cloudCatalogAgent && Boolean(cloudOrg?.id) && cloudCatalog === undefined && !cloudModelCatalogQuery.isError;
	const cloudCatalogGap = cloudCatalogAgent && !cloudCatalog && cloudModelCatalogQuery.isError
		? cloudModelCatalogGap(cloudModelCatalogQuery.error)
		: undefined;
	const localCatalogForCloud = listsLocalModelsForCloud(cloudCatalogGap, selectedAgent, modelScope);
	const localCatalogInUse = !isCloudProject || localCatalogForCloud;
	// Shares the picker's query key, so this is the same fetch, not a second one.
	const localModelCatalogQuery = useQuery({
		...agentModelsQueryOptions(selectedAgent, modelScope, hostId),
		enabled: selectedAgent !== "" && localCatalogInUse,
	});
	// A read that is off still holds this computer's saved catalog, which a cloud task does not run.
	const localCatalog = localCatalogInUse ? localModelCatalogQuery.data : undefined;
	// A model chosen for one Cloud server, account or organization may not exist on
	// another, so a change of any of them starts over; values that arrive while the
	// composer loads are not a change. A new credential for an agent drops only
	// that agent's choices. Leaving Cloud for a local project or Scratchpad is not
	// a change: their choices stay, and the next Cloud project is compared with
	// the last one.
	const previousCloudIdentity = useRef<readonly string[] | undefined>(undefined);
	const cloudCredentialScopes = useRef<Record<string, string>>({});
	useEffect(() => {
		const identity = [projectId ?? "", cloudBaseUrl, cloudUserId, cloudOrg?.id ?? ""];
		const previous = previousCloudIdentity.current;
		const identityChanged = isCloudProject && previous !== undefined &&
			previous.some((value, index) => (index === 0 || value !== "") && value !== identity[index]);
		if (isCloudProject) previousCloudIdentity.current = identity;
		if (identityChanged) {
			for (const key of Object.keys(agentDrafts)) delete agentDrafts[key];
			cloudCredentialScopes.current = {};
		}
		let credentialChanged = false;
		if (isCloudProject && selectedAgent && cloudConnectionsQuery.data !== undefined) {
			const scope = JSON.stringify([selectedCloudConnection?.id, selectedCloudConnection?.updatedAt]);
			const previousScope = cloudCredentialScopes.current[selectedAgent];
			credentialChanged = previousScope !== undefined && previousScope !== scope;
			cloudCredentialScopes.current[selectedAgent] = scope;
			if (credentialChanged) delete agentDrafts[selectedAgent];
		}
		if (!identityChanged && !credentialChanged) return;
		setModel("");
		setMode("");
		setEffort("");
		setModelTouched(false);
		setEffortTouched(false);
	}, [agentDrafts, cloudBaseUrl, cloudConnectionsQuery.data, cloudOrg?.id, cloudUserId, isCloudProject, projectId, selectedAgent, selectedCloudConnection?.id, selectedCloudConnection?.updatedAt]);
	const revalidationQuery = useQuery({
		queryKey: [
			"agent-model-revalidation",
			hostId ?? "",
			selectedAgent,
			modelScope,
			localCatalog?.validatedAt ?? "",
		],
		queryFn: () => revalidateAgentModels(selectedAgent, modelScope, hostId),
		enabled: selectedAgent !== "" && localCatalog?.refreshRecommended === true,
		staleTime: Number.POSITIVE_INFINITY,
		retry: false,
	});
	useEffect(() => {
		if (revalidationQuery.data) {
			queryClient.setQueryData(
				agentModelsQueryKey(selectedAgent, modelScope, hostId),
				revalidationQuery.data,
			);
		}
	}, [hostId, modelScope, queryClient, revalidationQuery.data, selectedAgent]);
	const revalidationWarning = revalidationQuery.isError
		? revalidationQuery.error instanceof Error
			? revalidationQuery.error.message
			: t("settings.models.validateFailed")
		: undefined;
	const modelWarningText =
		revalidationWarning ??
		localCatalog?.warning ??
		(localCatalogInUse && localModelCatalogQuery.isError
			? localModelCatalogQuery.error instanceof Error
				? localModelCatalogQuery.error.message
				: t("settings.models.loadFailed")
			: undefined);
	// A login problem behind the catalog warning gets a plain-language notice
	// with the fix in place. A cloud task never runs on this computer's login,
	// so its composer never asks for it.
	const modelAuthIssue = !isCloudProject && revalidationWarning === undefined ? modelCatalogAuthIssue(localCatalog) : undefined;
	useModelCatalogAuthRecovery(selectedAgent, hostId, modelAuthIssue);
	// Settings → Harness → Models picks the models this machine offers; cloud VMs keep their own catalog.
	const { models: offeredModels } = useOfferedModels(selectedAgent, localCatalog?.models, {
		hostId,
		keep: [model, mode, projectModelForSelectedAgent, projectModeForSelectedAgent],
		skip: isCloudProject,
	});
	const localModelCatalog: TaskComposerModelCatalog | undefined = localCatalog
		? {
				allowCustom: localCatalog.allowCustom,
				customModelEntry: localCatalog.customModelEntry,
				models: offeredModels,
				refreshError: localCatalog.refreshError,
				refreshState: localCatalog.refreshState,
				retryAt: localCatalog.retryAt,
				selectionMode: localCatalog.selectionMode,
			}
		: undefined;
	// No catalog default is a cloud task's: Default sends no model, so the
	// project's or the agent's own applies. A configured or remembered model the
	// catalog does not list stays selectable, with a warning that it may not run.
	const keptCloudModels = [projectModelForSelectedAgent, rememberedModel].filter(isConcreteModelID).join("\n");
	const cloudModels = useMemo((): AgentModelOption[] => {
		if (cloudCatalog) {
			const listed: AgentModelOption[] = cloudCatalog.models.map((item) => ({
				id: item.id,
				label: item.displayName,
				isDefault: false,
				efforts: item.efforts,
				defaultEffort: item.defaultEffort,
			}));
			const kept = keptCloudModels === "" ? [] : keptCloudModels.split("\n");
			const unlisted = kept.filter((id, index) => kept.indexOf(id) === index && !listed.some((item) => item.id === id));
			return [...listed, ...unlisted.map((id) => ({ id, label: id, isDefault: false }))];
		}
		return localCatalogForCloud ? offeredModels.map((item) => ({ ...item, isDefault: false })) : NO_MODELS;
	}, [cloudCatalog, keptCloudModels, localCatalogForCloud, offeredModels]);
	const modelCatalog: TaskComposerModelCatalog | undefined = isCloudProject
		? {
				...(localCatalogForCloud ? localModelCatalog : undefined),
				allowCustom: true,
				customModelEntry: "direct",
				selectionMode: "catalog",
				models: cloudModels,
			}
		: localModelCatalog;
	// An unmarked first row is not evidence of what the provider will run.
	const catalogModels = (isCloudProject ? cloudModels : offeredModels).filter((item) => isConcreteModelID(item.id));
	const catalogDefaultOption =
		isCloudProject ? "" : catalogModels.find((item) => item.isDefault)?.id ?? "";
	const catalogUsesModes = modelCatalog?.selectionMode === "mode";
	// The model this user ran most recently.
	const lastUsedOption = isCloudProject
		? ""
		: catalogModels
			.filter((item) => item.lastUsedAt)
			.sort((a, b) => Date.parse(b.lastUsedAt ?? "") - Date.parse(a.lastUsedAt ?? ""))[0]?.id ?? "";
	// With nothing remembered, run, or configured, Claude Code opens on the newest Opus.
	const claudeFallbackOption =
		!isCloudProject && selectedAgent === "claude-code" && !catalogUsesModes
			? (splitClaudeModels(catalogModels).current.find((item) => /opus/i.test(modelChoiceLabel(item)))?.id ?? "")
			: "";
	const rememberedModelIsValid =
		isConcreteModelID(rememberedModel) &&
		Boolean(
			modelCatalog &&
				(modelCatalog.allowCustom || catalogModels.some((item) => item.id === rememberedModel)),
		);
	const rememberedModeIsValid =
		isConcreteModelID(rememberedMode) && catalogModels.some((item) => item.id === rememberedMode);
	// A model is always selected: the provider's default, else the first one listed.
	// A cloud task starts on the project's or the agent's default unless one is chosen.
	const catalogPick = isCloudProject ? "" : catalogDefaultOption || catalogModels[0]?.id || "";
	const defaultModelForSelectedAgent =
		(rememberedModelIsValid ? rememberedModel : "") ||
		(isConcreteModelID(projectModelForSelectedAgent) ? projectModelForSelectedAgent : "") ||
		(!catalogUsesModes && isConcreteModelID(lastUsedOption) ? lastUsedOption : "") ||
		(catalogUsesModes ? "" : catalogDefaultOption) ||
		claudeFallbackOption ||
		(catalogUsesModes ? "" : catalogPick);
	const defaultModeForSelectedAgent =
		(rememberedModeIsValid ? rememberedMode : "") ||
		(isConcreteModelID(projectModeForSelectedAgent) && !isCloudProject ? projectModeForSelectedAgent : "") ||
		(catalogUsesModes && isConcreteModelID(lastUsedOption) ? lastUsedOption : "") ||
		(catalogUsesModes ? catalogPick : "");
	const selectedModel = model || (modelTouched ? (catalogUsesModes ? "" : catalogPick) : defaultModelForSelectedAgent);
	const selectedMode = mode || (modelTouched ? (catalogUsesModes ? catalogPick : "") : defaultModeForSelectedAgent);
	const selectedModelOrMode = (selectedModel || selectedMode).trim();
	const projectModelOrMode = projectModelForSelectedAgent || projectModeForSelectedAgent;
	const requestedModel = isCloudProject
		// A cloud task sends the model shown; Default sends none.
		? (isConcreteModelID(selectedModelOrMode) ? selectedModelOrMode : undefined)
		: isStandalone
		// Standalone sessions have no project config to inherit. If the UI shows a
		// concrete catalog default, send it explicitly; otherwise the native agent
		// may launch with a different harness-owned default than the one displayed.
		? (isConcreteModelID(selectedModelOrMode) ? selectedModelOrMode : undefined)
		: selectedModelOrMode && selectedModelOrMode !== projectModelOrMode && (
				selectedModelOrMode !== catalogDefaultOption || isConcreteModelID(projectModelOrMode) ||
				(!catalogUsesModes && !projectModelOrMode)
			) ? selectedModelOrMode : undefined;
	const rememberedEffortIsExplicit = Boolean(
		rememberedConfigForSelectedAgent &&
			Object.prototype.hasOwnProperty.call(rememberedConfigForSelectedAgent, "effort"),
	);
	const defaultEffortForSelectedAgent = rememberedEffortIsExplicit
		? (rememberedConfigForSelectedAgent?.effort ?? "")
		: selectedAgent === configuredProjectAgent
			? defaultWorkerEffort
			: "";
	// A model Cloud's catalog lists takes only the levels it reports: Claude
	// refuses a turn whose effort its model does not offer (Haiku offers none).
	// Any other model that reports no levels, such as one the catalog does not
	// list, may still take the chosen effort, so that effort is kept.
	const cloudEffortsUnknown = isCloudProject &&
		!cloudCatalog?.models.some((item) => item.id === selectedModel) &&
		catalogModels.find((item) => item.id === selectedModel)?.efforts === undefined;
	const { selected: effortModel } = useModelTuning({
		models: cloudEffortsUnknown ? undefined : catalogModels,
		model: selectedModel,
		effort,
		onEffortChange: setEffort,
		onEffortReset: setEffort,
	});
	const effortOptions = isCloudProject && !selectedModelOrMode
		? []
		: effortModel?.efforts?.filter((option) => option && option.toLowerCase() !== "default") ?? [];
	const cloudDefaultEffort = isCloudProject && effortOptions.includes("medium") ? "medium" : "";
	const inheritedEffort = selectedAgent === configuredProjectAgent ? defaultWorkerEffort : "";
	const implicitEffort = inheritedEffort || effortModel?.defaultEffort || "";
	const selectedAgentLabel = availableAgents?.find((item) => item.id === selectedAgent)?.label || selectedAgent;
	// A cloud project's Chat-capable harnesses come from the control plane, as a
	// local project's come from the daemon.
	const chatHarnesses = isCloudProject ? cloudOffering.chatHarnesses : settings?.chatHarnesses;
	const requiresTuiFallback =
		selectedAgent !== "" &&
		settings?.defaultSessionMode === "chat" &&
		!(chatHarnesses ?? []).includes(selectedAgent);
	// With no provider default for the reported levels AO picks one, shows it as
	// selected, and sends it, so the picker matches what the task runs with.
	const aoDefaultEffort = requiresTuiFallback ? undefined : fallbackEffort(effortOptions, implicitEffort);
	const effectiveEffort = isCloudProject
		? effort && effort !== "default" && (cloudEffortsUnknown || effortOptions.includes(effort)) ? effort : cloudDefaultEffort
		: effort && effort !== "default"
		? effort
		: aoDefaultEffort ?? (isStandalone && effortOptions.includes(implicitEffort) ? implicitEffort : "");
	// Cursor's effort and thinking are part of its model id; its effort button swaps the id.
	const cursorFamilies = listsModelVariants(selectedAgent)
		? collapseModelVariants((modelCatalog?.models ?? []).filter((item) => isConcreteModelID(item.id)))
		: undefined;
	const cursorChoice = cursorFamilies ? findModelVariant(cursorFamilies, selectedModel) : undefined;
	const pickCursorVariant = (want: { effort?: string; thinking?: boolean }) => {
		if (!cursorChoice) return;
		setModel(pickModelVariant(cursorChoice.family, { ...cursorChoice.variant, ...want }).id);
		setModelTouched(true);
	};
	const requestedEffort = isCloudProject
		? effectiveEffort || undefined
		: isStandalone
		// Standalone sessions have no project role to inherit from. Send the
		// concrete effort shown by the picker even when the UI is retrying in TUI
		// mode, otherwise the native harness may use a different default.
		? (effectiveEffort || undefined)
		: effortTouched || rememberedEffortIsExplicit
			? !effectiveEffort || effectiveEffort === implicitEffort ? undefined : effectiveEffort
			: aoDefaultEffort;
	const effortAvailability: EffortAvailability = requiresTuiFallback
		? "launch-unavailable"
		: !effortModel || effortModel.efforts === undefined
			? "unknown"
			: effortOptions.length > 0
				? "supported"
				: "unsupported";
	// What the picker says about the cloud catalog, and what it warns of: a model
	// the catalog does not list, or no catalog to go on.
	const cloudModelSource = !isCloudProject
		? undefined
		: cloudCatalog
			? cloudCatalog.source === "worker"
				? t("newTask.cloudModelsVerified")
				: cloudCatalog.cliVersion
					? t("newTask.cloudModelsRelease", { agent: selectedAgentLabel, version: cloudCatalog.cliVersion })
					: t("newTask.cloudModelsCatalog", { agent: selectedAgentLabel })
			: localCatalogForCloud && cloudCatalogGap === "unavailable"
				? t("newTask.cloudCredentialModels")
				: cloudModelCatalogQuery.isFetching
					? t("settings.models.loading")
					: undefined;
	const cloudModelWarning = !isCloudProject
		? undefined
		: cloudCatalog
			? isConcreteModelID(selectedModelOrMode) && !cloudCatalog.models.some((item) => item.id === selectedModelOrMode)
				? t("newTask.cloudModelUnlisted", { model: selectedModelOrMode, agent: selectedAgentLabel })
				: undefined
			: cloudCatalogGap === "legacy"
				? t("newTask.cloudLegacyModels")
				: cloudCatalogGap === "unavailable" && !localCatalogForCloud
					? t("newTask.cloudModelsUnavailable", { agent: selectedAgentLabel })
					: cloudCatalogGap === "failed"
						? t("newTask.cloudModelsFailed", { agent: selectedAgentLabel })
						: undefined;
	const canSubmit =
		hostConnected &&
		Boolean(projectId) &&
		!cloudCatalogLoading &&
		(!isStandalone || selectedAgent !== "") &&
		(!hostId || Boolean(agentCatalog?.agents.some((candidate) => candidate.id === selectedAgent && isLaunchableAgent(candidate)))) &&
		(isCloudProject || isStandalone || projectQuery.data !== undefined) &&
		(!hostId || (agentsQuery.isSuccess && settings !== undefined));
	// Named for its remote origins; a local project that failed to load reports here too.
	// A cloud project is not read locally, so a lookup made before it was known to be
	// a cloud project must not linger as its error.
	// Scratchpad has no project agent to fall back to: with no ready agent, or no agent
	// list at all, a send could never start, so it says why at once.
	const standaloneAgentError = isStandalone && !isCloudProject && selectedAgent === ""
		? agentsQuery.error instanceof Error ? agentsQuery.error.message : agentsQuery.isSuccess ? t("newTask.noReadyAgent") : undefined
		: undefined;
	// Scratchpad never reads a project, but its disabled query still sees an entry
	// another view cached under the same key, so that entry's error is not its own.
	const projectError = isCloudProject || isStandalone ? undefined : projectQuery.error;
	const remoteLoadError = !hostId ? (projectError instanceof Error ? projectError.message : standaloneAgentError) : !hostConnected ? t("remote.hostOffline") :
		[projectError, agentsQuery.error].find((cause): cause is Error => cause instanceof Error)?.message ?? settingsError ??
			(agentsQuery.isSuccess && !agentCatalog?.agents.some(isLaunchableAgent) ? t("remote.noReadyAgent") : undefined);
	const refreshSelectedModels = useCallback(async () => {
		if (isCloudProject) {
			// A cloud task's catalog is the control plane's: ask it again, and this
			// computer only when the answer still points here.
			if (!cloudCatalogAgent || !cloudOrg?.id) return;
			const result = await cloudModelCatalogQuery.refetch();
			if (result.data || !listsLocalModelsForCloud(cloudModelCatalogGap(result.error), selectedAgent, modelScope)) return;
		}
		const refreshed = await refreshAgentModels(selectedAgent, modelScope, hostId);
		queryClient.setQueryData(agentModelsQueryKey(selectedAgent, modelScope, hostId), refreshed);
	}, [cloudCatalogAgent, cloudModelCatalogQuery.refetch, cloudOrg?.id, hostId, isCloudProject, modelScope, queryClient, selectedAgent]);
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	const startAgentLogin = useCallback(() => {
		openGlobalSettings("harness", {
			focusAgentId: selectedAgent,
			...(hostId ? { hostId } : {}),
			// A cloud task runs on the cloud connection, so that is the login to fix.
			harnessView: isCloudProject ? "cloud" : "local",
			startLogin: true,
			preserveProject: true,
		});
	}, [hostId, isCloudProject, openGlobalSettings, selectedAgent]);
	const modelWarning = isCloudProject
		? cloudModelWarning ?? (localCatalogForCloud ? modelWarningText : undefined)
		: modelAuthIssue ? (
		<ModelCatalogNotice
			agentLabel={selectedAgentLabel}
			issue={modelAuthIssue}
			detail={modelWarningText}
			onLogin={startAgentLogin}
			onRetry={refreshSelectedModels}
		/>
	) : modelWarningText;
	useEffect(() => {
		if (!agentTouched) setAgent(defaultWorkerAgent);
	}, [agentTouched, defaultWorkerAgent]);
	useEffect(() => {
		if (!modelTouched) {
			setModel(defaultModelForSelectedAgent);
			setMode(defaultModeForSelectedAgent);
		}
	}, [defaultModelForSelectedAgent, defaultModeForSelectedAgent, modelTouched]);
	useEffect(() => {
		if (!effortTouched) setEffort(defaultEffortForSelectedAgent || cloudDefaultEffort);
	}, [cloudDefaultEffort, defaultEffortForSelectedAgent, effortTouched]);

	useEffect(() => () => clearAttachments(), [clearAttachments]);

	// Rejects with the create error after recording it (and any fallback) here.
	const launch = async (brief: string, { interfaceMode, approvalMode: requestedApproval, clientRequestId, replay, attachments: files }: TaskLaunchOptions = {}): Promise<string> => {
		if (!projectId || !canSubmit || isSubmitting) throw new Error(remoteLoadError ?? t("newTask.unableToStart"));

		setIsSubmitting(true);
		setError(undefined);
		setFallbackAction(undefined);
		const heldPreparation = taskPreparationRef.current;
		launchingPreparationRef.current = heldPreparation;
		let submittedPreparation = heldPreparation;
		try {
			if (!isCloudProject && selectedAgent) {
				// This check lacks the selected project's cwd and environment, so it
				// is advisory: the project-aware launch path remains authoritative.
				// It refreshes the readiness snapshot in the background rather than
				// holding the launch for a CLI probe that can take seconds.
				void Promise.resolve()
					.then(() => ensureAgentReadiness([selectedAgent], "launch", hostId))
					.then((readiness) => cacheAgentReadiness(queryClient, readiness, hostId))
					.catch(() => {});
			}
			const attachmentPayloads = files ?? await toSettledPayload();
			// One that resolved while the checks above ran is claimed too.
			submittedPreparation = heldPreparation || taskPreparationRef.current;
			launchingPreparationRef.current = submittedPreparation;
			const request: CreateTaskInput = {
				projectId,
				brief,
				...(replay ? {
					agent: replay.agent as CreateTaskInput["agent"],
					model: replay.model,
					effort: replay.effort,
					mode: replay.mode ?? (isCloudProject ? settings?.defaultSessionMode : undefined),
					approvalMode: replay.approvalMode,
				} : {
					agent: selectedAgent ? (selectedAgent as CreateTaskInput["agent"]) : undefined,
					model: requestedModel,
					// Only explicit Codex picks set this; agent changes reset it, and TUI retries preserve it.
					effort: requestedEffort,
					// The local daemon applies the default session mode itself; the control
					// plane has none, so a cloud task carries it.
					mode: interfaceMode ?? (isCloudProject ? settings?.defaultSessionMode : undefined),
					approvalMode: requestedApproval ?? approvalMode,
				}),
				attachments: attachmentPayloads.length > 0 ? attachmentPayloads : undefined,
				taskPreparation: submittedPreparation || undefined,
			};
			const { taskPreparation: _, attachments: _attachments, ...requestPayload } = request;
			const payload = JSON.stringify({ ...requestPayload, attachmentIds: attachments.map((attachment) => attachment.id) });
			if (requestRef.current?.payload !== payload) {
				requestRef.current = { payload, id: globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random()}` };
			}
			const createRequest = { ...request, clientRequestId: clientRequestId ?? requestRef.current.id };
			let sessionId: string;
			try {
				sessionId = await createTask(createRequest);
			} catch (err) {
				// A model list that went stale cannot vouch for the effort. Refresh it and
				// retry once; the same request id cannot create a second session.
				if (!(err instanceof TaskCreateError) || err.code !== "MODEL_CAPABILITIES_UNAVAILABLE") throw err;
				await refreshSelectedModels().catch(() => undefined);
				try {
					sessionId = await createTask(createRequest);
				} catch (retryErr) {
					if (!(retryErr instanceof TaskCreateError) || retryErr.code !== "MODEL_CAPABILITIES_UNAVAILABLE") throw retryErr;
					throw new TaskCreateError(
						t("newTask.modelCheckUnavailable", {
							agent: selectedAgentLabel,
							defaultValue: "Couldn't confirm {{agent}}'s models right now. Try again in a moment.",
						}),
						retryErr.code,
					);
				}
			}
			const preparationAfterSubmit = taskPreparationRef.current;
			taskPreparationRef.current = "";
			// DELETE is intentionally idempotent after a successful claim. It also
			// reclaims a preparation that resolved after this submission captured its
			// token, instead of leaving that unused worktree until TTL expiry.
			cancelTaskPreparation(submittedPreparation, hostId);
			cancelTaskPreparation(preparationAfterSubmit, hostId);
			if (selectedAgent) {
				// Remember a model only when the user chose it (now or before). The one
				// shown because the agent marks no default is not their preference.
				const modelChosen = modelTouched || rememberedModelIsValid || rememberedModeIsValid;
				const preference: TaskComposerAgentPreference = {
					model: selectedModel && modelChosen ? requestedModel ?? "" : "",
					mode: selectedMode && modelChosen ? requestedModel ?? "" : "",
					...(requestedEffort !== undefined ? { effort: requestedEffort } : {}),
				};
				agentDrafts[selectedAgent] = preference;
				rememberTaskComposerPreference(preferenceContext, selectedAgent, preference);
			}
			return sessionId;
		} catch (err) {
			const canBypassApprovals =
				err instanceof TaskCreateError &&
				err.code === "SESSION_MODE_UNSUPPORTED" &&
				hasErrorDetail(err.details, "missingCapabilities", "approvals") &&
				hasErrorDetail(err.details, "allowedApprovalModes", "bypass-permissions");
			setFallbackAction(
				canBypassApprovals
					? "bypass-permissions"
					: selectedAgent !== "unreal-agent" && interfaceMode !== "tui" &&
							err instanceof TaskCreateError &&
							Boolean(err.code && CHAT_PREFLIGHT_CODES.has(err.code))
						? "tui"
						: undefined,
			);
			// A page that has since left this preparation no longer offers it for a retry.
			if (submittedPreparation && taskPreparationRef.current !== submittedPreparation) cancelTaskPreparation(submittedPreparation, hostId);
			const message = err instanceof Error ? err.message : t("newTask.unableToStart");
			setError(message);
			throw err instanceof Error ? err : new Error(message);
		} finally {
			launchingPreparationRef.current = "";
			setIsSubmitting(false);
		}
	};
	// The interface a plain submit asks for; a TUI-only agent cannot open Chat.
	const submitMode: TaskLaunchOptions["interfaceMode"] = selectedAgent === "unreal-agent" ? "chat" : requiresTuiFallback ? "tui" : undefined;
	const launchFallback = (brief: string, clientRequestId?: string, files?: FileAttachmentPayload[]) =>
		fallbackAction === "bypass-permissions"
			? launch(brief, { interfaceMode: selectedAgent === "unreal-agent" ? "chat" : undefined, approvalMode: "bypass-permissions", clientRequestId, attachments: files })
			: launch(brief, { interfaceMode: "tui", clientRequestId, attachments: files });

	const changeAgent = (value: string) => {
		if (selectedAgent) {
			agentDrafts[selectedAgent] = {
				model: selectedModel ? requestedModel ?? "" : "",
				mode: selectedMode ? requestedModel ?? "" : "",
				...(requestedEffort !== undefined ? { effort: requestedEffort } : {}),
			};
		}
		setAgent(value);
		setAgentTouched(true);
		setModel("");
		setMode("");
		setEffort("");
		setModelTouched(false);
		setEffortTouched(false);
	};

	// The agents the new chat page's picker offers, ranked as in New Task. Locally only
	// installed agents whose sign-in is known to be usable: an "Auth unknown" agent may
	// not start, so it stays in Harness settings. A remote host's readiness can be
	// unknown without anything being wrong, so there definite failures alone are left out.
	const agentChoices = useMemo(() => {
		const agents = isCloudProject ? cloudAgents : agentCatalog?.agents;
		const ranked = buildRankedAgentOptions({
			agents,
			priorityRank: DEFAULT_AGENT_PRIORITY_RANK,
			fallbackAgents: AGENT_OPTIONS.map((agent) => unknownAgentReadiness(agent, agentLabel(agent))),
		});
		return (agents ? ranked.filter(hostId ? isLaunchableAgent : startsLocally) : ranked).map(({ id, label }) => ({ id, label }));
	}, [agentCatalog?.agents, cloudAgents, hostId, isCloudProject]);
	const catalogProjectId = isCloudProject || isStandalone ? "" : projectId ?? "";
	// A pick from the picker's other agents: that agent, with the model and effort chosen
	// alongside it, if any. Its catalog is cached, since the picker lists its models.
	const switchAgent = (harness: string, pick: { model?: string; effort?: string } = {}) => {
		if (harness !== selectedAgent) changeAgent(harness);
		if (pick.model) {
			const catalog = queryClient.getQueryData(agentModelsQueryOptions(harness, catalogProjectId, hostId).queryKey);
			if (catalog?.selectionMode === "mode") {
				setMode(pick.model);
				setModel("");
			} else {
				setModel(pick.model);
				setMode("");
			}
			setModelTouched(true);
		}
		if (pick.effort) {
			setEffort(pick.effort);
			setEffortTouched(true);
		}
	};

	// A cloud task shows the effort it sends.
	const shownEffort = isCloudProject ? effectiveEffort : effort;
	const showEffort = cursorChoice ? cursorChoice.family.efforts.length > 1 : !requiresTuiFallback && (effortOptions.length > 0 || Boolean(shownEffort && shownEffort !== "default"));
	// The same choices in the chat composer's shape, so the new chat page can use its settings bar.
	const chatModels = useMemo<ChatModel[]>(() => catalogModels.map((item) => ({
		id: item.id,
		displayName: item.id === "auto" ? t("settings.models.autoRouteLabel") : catalogUsesModes ? modelChoiceLabel(item) : item.label,
		default: Boolean(item.isDefault),
		efforts: item.efforts,
		defaultEffort: item.defaultEffort,
	})), [catalogModels, catalogUsesModes, t]);
	const projectPermissions = projectConfig?.worker?.agentConfig?.permissions ?? projectConfig?.agentConfig?.permissions;
	const turnSettings: TurnSettings = {
		model: (selectedModel || selectedMode) || undefined,
		reasoningEffort: showEffort ? effectiveEffort || undefined : undefined,
		// A session started without a permission choice runs in auto (applySpawnAgentConfig).
		approvalMode: approvalMode ?? (APPROVAL_MODES.includes(projectPermissions as ApprovalMode) ? projectPermissions as ApprovalMode : "auto"),
	};
	const chooseTurnSettings = (next: TurnSettings) => {
		if ((next.model ?? "") !== (turnSettings.model ?? "")) {
			if (catalogUsesModes) {
				setMode(next.model ?? "");
				setModel("");
				setEffort("");
			} else {
				setModel(next.model ?? "");
				setMode("");
			}
			setModelTouched(true);
		}
		if ((next.reasoningEffort ?? "") !== (turnSettings.reasoningEffort ?? "")) {
			setEffort(next.reasoningEffort ?? "");
			setEffortTouched(true);
		}
		if (next.approvalMode && next.approvalMode !== turnSettings.approvalMode) setApprovalMode(next.approvalMode);
	};

	return {
		launch,
		launchFallback,
		// What a plain send would request right now, to save alongside a send.
		launchSnapshot: (): LaunchReplay => ({
			agent: selectedAgent || undefined,
			model: requestedModel,
			effort: requestedEffort,
			approvalMode,
			mode: submitMode,
		}),
		// The interface a plain send starts in, including the Settings default.
		effectiveInterface: (submitMode ?? settings?.defaultSessionMode ?? "tui") as "chat" | "tui",
		refreshModels: refreshSelectedModels,
		preparedSessionId,
		submitMode,
		isCloudProject,
		canSubmit,
		isSubmitting,
		isDirty: modelTouched || effortTouched || attachments.length > 0,
		error,
		remoteLoadError,
		modelWarning,
		fallbackAction,
		fallbackLabel: fallbackAction === "bypass-permissions"
			? t("newTask.startWithoutApprovals", { defaultValue: "Start without approvals" })
			: t("newTask.createAsTui"),
		agent: {
			label: t("newTask.agent"),
			placeholder: t("newTask.selectAgent"),
			value: selectedAgent,
			agents: isCloudProject ? cloudAgents : agentCatalog?.agents,
			disabled:
				isSubmitting || (!isCloudProject && agentsQuery.isFetching && agentCatalog === undefined),
			onChange: changeAgent,
		},
		model: {
			agentId: selectedAgent,
			agentLabel: selectedAgentLabel,
			projectId: isStandalone ? "" : (projectId ?? ""),
			disabled: isSubmitting,
			value: selectedModel,
			mode: selectedMode,
			catalog: modelCatalog,
			fetching: localCatalogInUse ? localModelCatalogQuery.isFetching : cloudModelCatalogQuery.isFetching,
			loading:
				selectedAgent !== "" &&
				(localCatalogInUse
					? localModelCatalogQuery.isFetching && localModelCatalogQuery.data === undefined
					: cloudModelCatalogQuery.isFetching && cloudCatalog === undefined),
			onModelChange: (value: string) => {
				setModel(value);
				setMode("");
				setModelTouched(true);
				// Effort levels are per-model, so a level the newly chosen model
				// does not advertise has to be dropped rather than carried over. A
				// cloud model Cloud's catalog does not list may still take it. The
				// cloud default shows no effort, so it starts from the project's.
				if (isCloudProject && value === "") {
					setEffort(inheritedEffort);
					return;
				}
				const listed = cloudCatalog?.models.find((item) => item.id === value);
				const nextEfforts = listed
					? listed.efforts
					: modelCatalog?.models?.find((item) => item.id === value)?.efforts;
				const keepUnknown = isCloudProject && !listed && nextEfforts === undefined;
				setEffort((current) => (current !== "" && !keepUnknown && !nextEfforts?.includes(current) ? "" : current));
			},
			onModeChange: (value: string) => {
				setMode(value);
				setModel("");
				setModelTouched(true);
				// A mode replaces the model entirely, so no model vouches for a
				// previously chosen level any more.
				setEffort("");
			},
		},
		effort: cursorChoice ? {
			disabled: isSubmitting,
			options: cursorChoice.family.efforts,
			value: cursorChoice.variant.effort ?? "",
			onChange: (value: string) => pickCursorVariant({ effort: value }),
		} : {
			disabled: isSubmitting,
			options: effortOptions,
			value: effectiveEffort,
			onChange: (value: string) => {
				setEffort(value);
				setEffortTouched(true);
			},
		},
		attachments: {
			items: attachments.map(({ id, name, dataUrl }) => ({ id, name, previewUrl: dataUrl })),
			error: attachmentError,
			onAddFiles: (files: File[]) => void addFiles(files),
			onRemove: removeAttachment,
		},
		showEffort,
		chatModels,
		turnSettings,
		chooseTurnSettings,
		agentChoices,
		catalogProjectId,
		switchAgent,
		renderAgentControl: (control: TaskComposerAgentControl) => <DesktopAgentControl {...control} hostId={hostId} manageView={isCloudProject ? "cloud" : "local"} />,
		renderEffortControl: (control: TaskComposerEffortControl) => <TaskEffortPicker {...control} value={control.value || cloudDefaultEffort} defaultEffort={cursorChoice ? undefined : inheritedEffort || effortModel?.defaultEffort} availability={cursorChoice ? "supported" : effortAvailability}
			footer={cursorChoice?.family.hasThinkingChoice ? <ToggleRow label={t("agentSwitch.thinking")} checked={cursorChoice.variant.thinking} onChange={(thinking) => pickCursorVariant({ thinking })} /> : undefined} />,
		renderModelControl: (control: TaskComposerModelControl) => <TaskModelPicker {...control}
			onRefresh={isCloudProject && !cloudCatalogAgent ? undefined : refreshSelectedModels}
			allowNativeSelection={isCloudProject}
			footer={cloudModelSource}
			showFollowAgentAction={Boolean(catalogDefaultOption || !isConcreteModelID(projectModelOrMode))} />,
	};
}

export type TaskLaunch = ReturnType<typeof useTaskLaunch>;

/** Installed, with a sign-in that works, is not needed, or has credentials not yet verified. */
function startsLocally(agent: AgentInfo): boolean {
	return agent.installation.state === "installed" && (isReadyAgent(agent) || agent.authentication.state === "configured");
}

type CloudModelCatalogGap = "legacy" | "unavailable" | "failed";

/** Why the control plane gave a cloud task no model catalog. */
function cloudModelCatalogGap(error: unknown): CloudModelCatalogGap {
	if (!(error instanceof CloudCpError)) return "failed";
	// An older control plane has no such route and answers 404 or 501 without an
	// error envelope. A handler's own 404 carries a code and is a real failure.
	if (!error.code && (error.status === 404 || error.status === 501)) return "legacy";
	if (error.status === 409 && error.code === "MODEL_CATALOG_UNAVAILABLE") return "unavailable";
	return "failed";
}

/**
 * Whether a cloud task lists this computer's models in place of the control
 * plane's: an older control plane's suggestions, or opencode's own discovery,
 * scoped to the connected provider's credential type.
 */
function listsLocalModelsForCloud(gap: CloudModelCatalogGap | undefined, agent: string, modelScope: string): boolean {
	return gap === "legacy" || (gap === "unavailable" && agent === "opencode" && modelScope !== "");
}

// The launch form as a standalone composer. The app starts tasks from the new
// chat page; this view keeps the launch logic exercised in isolation.
export function TaskComposer({
	projectId,
	hostId,
	onCreated,
	onDirtyChange,
	onSubmittingChange,
	autoFocusTitle,
	createLabel,
}: TaskComposerProps) {
	const { t } = useTranslation();
	const taskPlaceholder = useMemo(() => {
		const placeholders = t("newTask.taskPlaceholders" as never, { returnObjects: true }) as string[];
		return Array.isArray(placeholders)
			? (placeholders[Math.floor(Math.random() * placeholders.length)] ?? "")
			: "";
	}, [t]);
	const task = useTaskLaunch({ projectId, hostId });
	const [isPromptDirty, setIsPromptDirty] = useState(false);
	const isDirty = isPromptDirty || task.isDirty;
	const handlePromptChange = useCallback((value: string) => {
		const nextDirty = value.trim() !== "";
		setIsPromptDirty((wasDirty) => (wasDirty === nextDirty ? wasDirty : nextDirty));
	}, []);
	useEffect(() => {
		onDirtyChange?.(isDirty);
	}, [isDirty, onDirtyChange]);
	useEffect(() => () => onDirtyChange?.(false), [onDirtyChange]);
	useEffect(() => {
		onSubmittingChange?.(task.isSubmitting);
	}, [task.isSubmitting, onSubmittingChange]);
	useEffect(() => () => onSubmittingChange?.(false), [onSubmittingChange]);
	// The hook records a failure for display, so a rejected create just stays put.
	const settle = (created: Promise<string>) => void created.then(onCreated, () => undefined);

	return (
		<TaskComposerView
			autoFocusPrompt={autoFocusTitle}
			canSubmit={task.canSubmit}
			onPromptChange={handlePromptChange}
			labels={{
				addFile: t("newTask.addFile"),
				effort: t("settings.models.effort"),
				fallbackAction: task.fallbackLabel,
				removeFile: (name) => t("newTask.removeFile", { name }),
				runsWith: t("newTask.runsWith"),
				start: createLabel ?? t("newTask.start"),
				starting: t("newTask.starting"),
				task: t("newTask.task"),
				taskPlaceholder,
			}}
			agent={task.agent}
			model={task.model}
			effort={task.effort}
			attachments={task.attachments}
			submission={{
				showFallbackAction: task.fallbackAction !== undefined,
				error: task.error ?? task.remoteLoadError ?? undefined,
				isSubmitting: task.isSubmitting,
				modelWarning: task.modelWarning,
				onFallbackAction: (brief) => settle(task.launchFallback(brief)),
				onSubmit: (brief) => settle(task.launch(brief, { interfaceMode: task.submitMode })),
			}}
			renderAgentControl={task.renderAgentControl}
			renderEffortControl={task.renderEffortControl}
			renderModelControl={task.renderModelControl}
			showEffort={task.showEffort}
		/>
	);
}

const APPROVAL_MODES: ApprovalMode[] = ["default", "accept-edits", "auto", "bypass-permissions"];

// Both local and cloud list only harnesses that can run, plus a way to manage
// them: local logins, or cloud connections for cloud projects.
function DesktopAgentControl({ hostId, manageView, ...control }: TaskComposerAgentControl & { hostId?: string; manageView: "local" | "cloud" }) {
	const { t } = useTranslation();
	return (
		<RequiredAgentField
			{...control}
			hostId={hostId}
			manageView={manageView}
			managementLabel={(manageView === "cloud" ? t("agentSelector.manageCloud") : t("agentSelector.manage")).replace(/[.…]+$/u, "")}
			variant="chip"
			triggerClassName="composer-toolbar-option w-fit"
			contentClassName={COMPOSER_MENU_WIDTH}
		/>
	);
}
