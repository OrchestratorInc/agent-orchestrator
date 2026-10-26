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
import { CloudCpError } from "../lib/cloud-cp";
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
import { cloudAgentInfos, connectedCredentialType, credentialModelScope, isCloudHarnessConnected } from "../lib/cloud-agents";
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
import { cloudSessionsQueryKey, useCloudProjectsQuery } from "../hooks/useWorkspaceQuery";
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
	const { client: cloudClient } = useCloudCp();
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
		[cloudClient, cloudOrg, queryClient, t],
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
	// Shares the picker's query key, so this is the same fetch, not a second one.
	const modelCatalogQuery = useQuery(agentModelsQueryOptions(selectedAgent, modelScope, hostId));
	const revalidationQuery = useQuery({
		queryKey: [
			"agent-model-revalidation",
			hostId ?? "",
			selectedAgent,
			modelScope,
			modelCatalogQuery.data?.validatedAt ?? "",
		],
		queryFn: () => revalidateAgentModels(selectedAgent, modelScope, hostId),
		enabled: selectedAgent !== "" && modelCatalogQuery.data?.refreshRecommended === true,
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
		modelCatalogQuery.data?.warning ??
		(modelCatalogQuery.isError
			? modelCatalogQuery.error instanceof Error
				? modelCatalogQuery.error.message
				: t("settings.models.loadFailed")
			: undefined);
	// A login problem behind the catalog warning gets a plain-language notice
	// with the fix in place. Discovery runs against this computer's login even
	// for a cloud project, whose task runs with the cloud connection instead,
	// so there the notice is informational and never reads as blocking.
	const modelAuthIssue = revalidationWarning === undefined ? modelCatalogAuthIssue(modelCatalogQuery.data) : undefined;
	useModelCatalogAuthRecovery(selectedAgent, hostId, modelAuthIssue);
	// Settings → Harness → Models picks the models this machine offers; cloud VMs keep their own catalog.
	const { models: offeredModels } = useOfferedModels(selectedAgent, modelCatalogQuery.data?.models, {
		hostId,
		keep: [model, mode, projectModelForSelectedAgent, projectModeForSelectedAgent],
		skip: isCloudProject,
	});
	const modelCatalog: TaskComposerModelCatalog | undefined = modelCatalogQuery.data
		? {
				allowCustom: modelCatalogQuery.data.allowCustom,
				customModelEntry: modelCatalogQuery.data.customModelEntry,
				models: offeredModels,
				refreshError: modelCatalogQuery.data.refreshError,
				refreshState: modelCatalogQuery.data.refreshState,
				retryAt: modelCatalogQuery.data.retryAt,
				selectionMode: modelCatalogQuery.data.selectionMode,
			}
		: undefined;
	// An unmarked first row is not evidence of what the provider will run.
	const catalogModels = offeredModels.filter((item) => isConcreteModelID(item.id));
	const catalogDefaultOption =
		catalogModels.find((item) => item.isDefault)?.id ?? "";
	const catalogUsesModes = modelCatalogQuery.data?.selectionMode === "mode";
	// The model this user ran most recently.
	const lastUsedOption =
		catalogModels
			.filter((item) => item.lastUsedAt)
			.sort((a, b) => Date.parse(b.lastUsedAt ?? "") - Date.parse(a.lastUsedAt ?? ""))[0]?.id ?? "";
	// With nothing remembered, run, or configured, Claude Code opens on the newest Opus.
	const claudeFallbackOption =
		selectedAgent === "claude-code" && !catalogUsesModes
			? (splitClaudeModels(catalogModels).current.find((item) => /opus/i.test(modelChoiceLabel(item)))?.id ?? "")
			: "";
	const rememberedConfigForSelectedAgent = agentDrafts[selectedAgent];
	const rememberedModel = rememberedConfigForSelectedAgent?.model ?? "";
	const rememberedMode = rememberedConfigForSelectedAgent?.mode ?? "";
	const rememberedModelIsValid =
		isConcreteModelID(rememberedModel) &&
		Boolean(
			modelCatalogQuery.data &&
				(modelCatalogQuery.data.allowCustom || catalogModels.some((item) => item.id === rememberedModel)),
		);
	const rememberedModeIsValid =
		isConcreteModelID(rememberedMode) && catalogModels.some((item) => item.id === rememberedMode);
	// A model is always selected: the provider's default, else the first one listed.
	// Cloud tasks read this device's catalog, whose first model the cloud VM may not
	// run, so there an unreported default is left to the VM, as before.
	const catalogDefaultHidden = Boolean(modelCatalogQuery.data?.models?.some(
		(item) => item.isDefault && isConcreteModelID(item.id) && !catalogModels.some((offered) => offered.id === item.id),
	));
	const catalogPick = isCloudProject ? catalogDefaultOption : catalogDefaultOption || catalogModels[0]?.id || "";
	const defaultModelForSelectedAgent =
		(rememberedModelIsValid ? rememberedModel : "") ||
		(isConcreteModelID(projectModelForSelectedAgent) ? projectModelForSelectedAgent : "") ||
		(!catalogUsesModes && isConcreteModelID(lastUsedOption) ? lastUsedOption : "") ||
		(catalogUsesModes ? "" : catalogDefaultOption) ||
		claudeFallbackOption ||
		(catalogUsesModes ? "" : catalogPick) ||
		// Only replace a default the catalog identified and Settings hid.
		(isCloudProject && !catalogUsesModes && catalogDefaultHidden ? catalogModels[0]?.id ?? "" : "");
	const defaultModeForSelectedAgent =
		(rememberedModeIsValid ? rememberedMode : "") ||
		(isConcreteModelID(projectModeForSelectedAgent) ? projectModeForSelectedAgent : "") ||
		(catalogUsesModes && isConcreteModelID(lastUsedOption) ? lastUsedOption : "") ||
		(catalogUsesModes ? catalogPick : "");
	const selectedModel = model || (modelTouched ? (catalogUsesModes ? "" : catalogPick) : defaultModelForSelectedAgent);
	const selectedMode = mode || (modelTouched ? (catalogUsesModes ? catalogPick : "") : defaultModeForSelectedAgent);
	const selectedModelOrMode = (selectedModel || selectedMode).trim();
	const projectModelOrMode = projectModelForSelectedAgent || projectModeForSelectedAgent;
	const requestedModel = isStandalone
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
	const { selected: effortModel } = useModelTuning({
		models: catalogModels,
		model: selectedModel,
		effort,
		onEffortChange: setEffort,
		onEffortReset: setEffort,
	});
	const effortOptions = effortModel?.efforts?.filter((option) => option && option.toLowerCase() !== "default") ?? [];
	const cloudDefaultEffort = isCloudProject && effortOptions.includes("medium") ? "medium" : "";
	const inheritedEffort = selectedAgent === configuredProjectAgent ? defaultWorkerEffort : "";
	const implicitEffort = inheritedEffort || effortModel?.defaultEffort || "";
	const selectedAgentLabel = agentCatalog?.agents.find((item) => item.id === selectedAgent)?.label || selectedAgent;
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
	const effectiveEffort = effort && effort !== "default"
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
		? effort || cloudDefaultEffort || undefined
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
	const canSubmit =
		hostConnected &&
		Boolean(projectId) &&
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
	const remoteLoadError = !hostId ? (!isCloudProject && projectQuery.error instanceof Error ? projectQuery.error.message : standaloneAgentError) : !hostConnected ? t("remote.hostOffline") :
		[projectQuery.error, agentsQuery.error].find((cause): cause is Error => cause instanceof Error)?.message ?? settingsError ??
			(agentsQuery.isSuccess && !agentCatalog?.agents.some(isLaunchableAgent) ? t("remote.noReadyAgent") : undefined);
	const refreshSelectedModels = useCallback(async () => {
		const refreshed = await refreshAgentModels(selectedAgent, modelScope, hostId);
		queryClient.setQueryData(agentModelsQueryKey(selectedAgent, modelScope, hostId), refreshed);
	}, [hostId, modelScope, queryClient, selectedAgent]);
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
	// The catalog is read through this computer's login, but a cloud task runs on
	// the cloud connection: once that is connected, this computer's login state is
	// irrelevant to the task and the notice only confuses.
	const cloudHarnessReady = isCloudProject && isCloudHarnessConnected(cloudConnectionsQuery.data, selectedAgent);
	const modelWarning = modelAuthIssue && cloudHarnessReady ? undefined : modelAuthIssue ? (
		<ModelCatalogNotice
			agentLabel={selectedAgentLabel}
			issue={modelAuthIssue}
			detail={modelWarningText}
			cloud={isCloudProject}
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
	const launch = async (brief: string, { interfaceMode, approvalMode: requestedApproval, clientRequestId, replay }: TaskLaunchOptions = {}): Promise<string> => {
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
			const attachmentPayloads = await toSettledPayload();
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
	const launchFallback = (brief: string, clientRequestId?: string) =>
		fallbackAction === "bypass-permissions"
			? launch(brief, { interfaceMode: selectedAgent === "unreal-agent" ? "chat" : undefined, approvalMode: "bypass-permissions", clientRequestId })
			: launch(brief, { interfaceMode: "tui", clientRequestId });

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

	const showEffort = cursorChoice ? cursorChoice.family.efforts.length > 1 : !requiresTuiFallback && (effortOptions.length > 0 || Boolean(effort && effort !== "default"));
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
			fetching: modelCatalogQuery.isFetching,
			loading:
				selectedAgent !== "" &&
				modelCatalogQuery.isFetching &&
				modelCatalogQuery.data === undefined,
			onModelChange: (value: string) => {
				setModel(value);
				setMode("");
				setModelTouched(true);
				// Effort levels are per-model, so a level the newly chosen model
				// does not advertise has to be dropped rather than carried over.
				const nextEfforts =
					modelCatalog?.models?.find((item) => item.id === value)?.efforts ?? [];
				setEffort((current) => (current !== "" && !nextEfforts.includes(current) ? "" : current));
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
		renderModelControl: (control: TaskComposerModelControl) => <TaskModelPicker {...control} onRefresh={refreshSelectedModels}
			showFollowAgentAction={Boolean(catalogDefaultOption || !isConcreteModelID(projectModelOrMode))} />,
	};
}

export type TaskLaunch = ReturnType<typeof useTaskLaunch>;

/** Installed, with a sign-in that works, is not needed, or has credentials not yet verified. */
function startsLocally(agent: AgentInfo): boolean {
	return agent.installation.state === "installed" && (isReadyAgent(agent) || agent.authentication.state === "configured");
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
