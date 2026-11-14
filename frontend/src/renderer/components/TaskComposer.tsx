import { apiErrorException } from "../lib/api-error-exception";
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
import { agentAccountProviderFor, type AgentAccountProvider } from "../hooks/useAgentAccounts";
import { useConnectedHosts } from "../hooks/useHostConnection";
import { useResetStaleProjectTuning } from "../hooks/useResetStaleProjectTuning";
import { captureRendererEvent } from "../lib/telemetry";
import { CloudCpError, type CloudCpSession } from "../lib/cloud-cp";
import { seedCloudOpening } from "../lib/cloud-opening";
import {
	cacheAgentReadiness,
	ensureAgentReadiness,
	useAgentReadinessQuery,
} from "../hooks/useAgentReadinessQuery";
import { type FileAttachmentPayload, useFileAttachments } from "../hooks/useFileAttachments";
import { useChatPermissionModesQuery } from "../hooks/useChatPermissionModes";
import { useSettings } from "../hooks/useSettings";
import { useOfferedModels } from "../hooks/useOfferedModels";
import { useCloudCp } from "../hooks/useCloudCp";
import { invalidateCloudModelCatalogs, useCloudModelCatalog } from "../hooks/useCloudModelCatalog";
import { cloudModelCatalogGap, cloudModelVerdict, listsLocalModelsForCloud } from "../lib/cloud-model-catalog";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { useCloudOrg } from "../hooks/useCloudOrg";
import { useCloudOrchestratorModel } from "../hooks/useCloudOrchestratorModel";
import { useProviderConnections } from "../hooks/useProviderConnections";
import { CLOUD_AGENT_PROVIDERS, cloudAgentInfos, connectedCredentialType, credentialModelScope } from "../lib/cloud-agents";
import { CloudTaskFilesNeedChatError, CloudTaskFilesUndeliveredError, createCloudChatSessionWithFiles, createCloudSession } from "../lib/cloud-session-create";
import { uploadCloudAttachment } from "../lib/cloud-attachments";
import { reportCloudSessionCreateRequested } from "../lib/cloud-telemetry";
import { rememberStandaloneAgent } from "../lib/cloud-standalone-agents";
import { cloudDefaultModel, cloudModelLabel, isConcreteModelID, isModelSelectable, modelChoiceLabel, splitClaudeModels, withoutLocalAvailability } from "../lib/agent-model-choices";
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
import { cloudSessionsQueryKey, seedCreatedFreestyleSession, seedCreatedStandaloneAgent, useCloudProjectsQuery } from "../hooks/useWorkspaceQuery";
import {
	agentModelsQueryKey,
	agentModelsQueryOptions,
	modelCatalogAuthIssue,
	refreshAgentModels,
	revalidateAgentModels,
} from "../hooks/useAgentModelsQuery";
import { useModelCatalogAuthRecovery } from "../hooks/useModelCatalogAuthRecovery";
import { sessionNameFromBrief } from "../lib/report-problem";
import { useUiStore } from "../stores/ui-store";
import { ModelCatalogNotice } from "./ModelCatalogNotice";
import { CLOUD_STANDALONE_WORKSPACE_ID, STANDALONE_WORKSPACE_ID, toAgentProvider } from "../types/workspace";
import { CODER_PROVIDER, projectSandboxProvider } from "../lib/cloud-offering";
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
	branch?: string;
	agent?: DelegateAgent;
	model?: string;
	effort?: string;
	mode?: "chat" | "tui";
	approvalMode?: ApprovalMode;
	accountId?: string;
	attachments?: FileAttachmentPayload[];
	taskPreparation?: string;
};

const CHAT_PREFLIGHT_CODES = new Set([
	"SESSION_MODE_UNSUPPORTED",
	"CHAT_DRIVER_UNAVAILABLE",
	"CHAT_DRIVER_INCOMPATIBLE",
	"CHAT_AUTH_REQUIRED",
]);

const CHAT_ONLY_AGENTS = new Set(["unreal-agent", "docker-agent"]);

function isChatOnlyAgent(agent: string | null | undefined): boolean {
	return Boolean(agent && CHAT_ONLY_AGENTS.has(agent));
}

const READINESS_RECONCILE_CODES = new Set(["AGENT_BINARY_NOT_FOUND", "AGENT_AUTH_REQUIRED", "CHAT_AUTH_REQUIRED"]);

function cancelTaskPreparation(token: string, hostId?: string): void {
	if (!token) return;
	try {
		void (hostId ? clientForHost(hostId) : apiClient).DELETE("/api/v1/task-preparations/{token}", {
			params: { path: { token } },
		}).catch(() => {   });
	} catch {
	}
}

class TaskCreateError extends Error {
	constructor(
		message: string,
		readonly code?: string,
		readonly details?: components["schemas"]["APIError"]["details"],
		readonly status?: number,
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
	clientRequestId?: string;
	replay?: LaunchReplay;
	attachments?: FileAttachmentPayload[];
};

export type LaunchReplay = {
	agent?: string;
	model?: string;
	effort?: string;
	approvalMode?: ApprovalMode;
	mode?: "chat" | "tui";
	accountId?: string;
};

export function useTaskLaunch({ projectId, hostId }: {
	projectId?: string;
	hostId?: string;
}) {
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
	const [approvalMode, setApprovalMode] = useState<ApprovalMode>();
	const [isSubmitting, setIsSubmitting] = useState(false);
	const [error, setError] = useState<string | undefined>();
	const [status, setStatus] = useState<string | undefined>();
	const [errorAction, setErrorAction] = useState<"install-agent" | "choose-branch" | undefined>();
	const [branchOverride, setBranchOverride] = useState("");
	const [accountPicks, setAccountPicks] = useState<Partial<Record<AgentAccountProvider, string>>>({});
	useEffect(() => setAccountPicks({}), [hostId]);
	const [branchDraft, setBranchDraft] = useState("");
	useEffect(() => {
		setBranchOverride("");
		setBranchDraft("");
		setErrorAction(undefined);
		setStatus(undefined);
	}, [projectId, hostId]);
	const [fallbackAction, setFallbackAction] = useState<FallbackAction>();
	const taskPreparationRef = useRef("");
	const launchingPreparationRef = useRef("");
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
	const { client: cloudClient, baseUrl: cloudBaseUrl, userId: cloudUserId } = useCloudCp();
	const { org: cloudOrg } = useCloudOrg();
	const cloudOffering = useCloudOffering();
	const cloudProjects = useCloudProjectsQuery();
	const cloudProject = (cloudProjects.data ?? []).find((project) => project.id === projectId);
	const isCloudStandalone = !hostId && projectId === CLOUD_STANDALONE_WORKSPACE_ID;
	const isCloudProject = !hostId && (Boolean(cloudProject) || isCloudStandalone);
	const isCoderCloudProject = isCloudProject && !isCloudStandalone && projectSandboxProvider(cloudProject?.config, cloudOffering) === CODER_PROVIDER;
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
				const files = input.attachments ?? [];
				if (files.length > 0 && (isCloudStandalone || cloudOffering.maxAttachmentBytes === undefined)) {
					throw new TaskCreateError(t("newTask.cloudAttachmentsUnsupported", { defaultValue: "File attachments are not supported for cloud tasks yet." }), "CLOUD_ATTACHMENTS_UNSUPPORTED");
				}
				if (files.length > 0 && input.mode !== "chat") {
					throw new TaskCreateError(input.agent && !cloudOffering.chatHarnesses.includes(input.agent)
						? t("newTask.cloudAttachmentsNeedChatAgent", { agent: agentLabel(input.agent), defaultValue: "Files can start a Cloud task only in Chat, which {{agent}} doesn't run in on Cloud. Choose another agent, or remove the files." })
						: t("newTask.cloudAttachmentsNeedChatDefault", { defaultValue: "Files can start a Cloud task only in Chat. Set the default session interface to Chat in Settings > General, or remove the files." }),
					"CLOUD_ATTACHMENTS_NEED_CHAT");
				}
				if (!cloudOrg?.id) throw new Error(t("newTask.unableToStart"));
				if (!input.agent) throw new Error(t("newTask.noReadyAgent"));
				const sentAt = new Date().toISOString();
				const body = {
					harness: input.agent,
					displayName: input.brief.trim().slice(0, 100) || input.agent,
					prompt: input.brief,
					...(input.model ? { model: input.model } : {}),
					...(input.effort ? { reasoningEffort: input.effort } : {}),
					...(input.mode ? { interfaceMode: input.mode } : {}),
					...(input.approvalMode ? { approvalMode: input.approvalMode } : {}),
				};
				const createOptions = input.clientRequestId ? { idempotencyKey: input.clientRequestId } : undefined;
				const orgId = cloudOrg.id;
				let session;
				if (isCloudStandalone) {
					reportCloudSessionCreateRequested({ kind: "worker", projectId: "" });
					const created = await cloudClient.createStandaloneAgent(orgId, body, createOptions);
					seedCreatedStandaloneAgent(queryClient, cloudBaseUrl, orgId, created.project);
					rememberStandaloneAgent(created.session.id, created.project.id);
					session = created.session;
				} else if (files.length > 0) {
					({ session } = await createCloudChatSessionWithFiles(cloudClient, orgId,
						{ projectId: input.projectId, kind: "worker", ...body, interfaceMode: "chat" }, files,
						(sessionId, file, signal) => uploadCloudAttachment(cloudBaseUrl, orgId, sessionId, { name: file.name || "attachment", data: file.data }, { signal }),
						{ idempotencyKey: input.clientRequestId }));
				} else {
					({ session } = await createCloudSession(cloudClient, orgId, { projectId: input.projectId, kind: "worker", ...body }, createOptions));
				}
				rememberCloudChatSettings(cloudOrg.id, session.id, session.harness, input);
				seedCloudOpening(queryClient, cloudOrg.id, session.id, {
					clientMessageId: input.clientRequestId ?? session.id,
					text: input.brief,
					createdAt: sentAt,
					...(session.interfaceMode ? { interfaceMode: session.interfaceMode } : {}),
				});
				seedCreatedFreestyleSession(queryClient, cloudBaseUrl, cloudOrg.id, session);
				void queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
				void captureRendererEvent("ao.tasks.created", { project_id: input.projectId });
				return session.id;
			} catch (err) {
				void captureRendererEvent("ao.tasks.create_failed", { project_id: input.projectId });
				if (err instanceof CloudCpError && err.code === "MODEL_UNAVAILABLE" && cloudOrg?.id && input.agent) {
					void invalidateCloudModelCatalogs(queryClient, { baseUrl: cloudBaseUrl, userId: cloudUserId, orgId: cloudOrg.id, agent: input.agent });
				}
				if (err instanceof CloudCpError) throw new TaskCreateError(err.message, err.code, undefined, err.status);
				if (err instanceof CloudTaskFilesNeedChatError) throw new TaskCreateError(t("newTask.cloudAttachmentsStartedInTerminal", { defaultValue: err.message }), err.code);
				if (err instanceof CloudTaskFilesUndeliveredError) throw new TaskCreateError(err.message, err.code);
				throw err instanceof Error ? err : new Error(t("newTask.unableToStart"));
			}
		},
		[cloudBaseUrl, cloudClient, cloudOffering.chatHarnesses, cloudOffering.maxAttachmentBytes, cloudOrg, cloudUserId, isCloudStandalone, queryClient, t],
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
						...(input.branch ? { branch: input.branch } : {}),
						agent: input.agent,
						...(input.model ? { model: input.model } : {}),
						...(input.effort !== undefined ? { effort: input.effort } : {}),
						...(input.mode ? { mode: input.mode } : {}),
						...(input.approvalMode ? { approvalMode: input.approvalMode } : {}),
						...(input.accountId ? { accountId: input.accountId } : {}),
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
				void captureRendererEvent("ao.tasks.created", { project_id: input.projectId });
				return data.workerId;
			} catch (err) {
				void captureRendererEvent("ao.tasks.create_failed", { project_id: input.projectId });
				if (
					err instanceof TaskCreateError &&
					err.code &&
					READINESS_RECONCILE_CODES.has(err.code) &&
					input.agent
				) {
					try {
						cacheAgentReadiness(queryClient, await ensureAgentReadiness([input.agent], "launch", hostId), hostId);
					} catch {
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
			const displayName = sessionNameFromBrief(input.brief) || input.agent || "Standalone agent";
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
					...(input.accountId ? { accountId: input.accountId } : {}),
					...(input.attachments && input.attachments.length > 0 ? { attachments: input.attachments } : {}),
				},
			});
			if (error) {
				throw new TaskCreateError(apiErrorMessage(error, t("newTask.unableToStart")), apiErrorCode(error), error.details);
			}
			if (!data?.session.id) throw new Error(t("newTask.noSession"));
			void captureRendererEvent("ao.tasks.created", { scope: "standalone" });
			return data.session.id;
			} catch (err) {
				void captureRendererEvent("ao.tasks.create_failed", { scope: "standalone" });
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
		queryKey: hostId ? ["project", hostId, projectId] : ["project", projectId],
		enabled: Boolean(projectId) && !isCloudProject && !isStandalone,
		queryFn: async () => {
			const { data, error: apiError } = await (hostId ? clientForHost(hostId) : apiClient).GET("/api/v1/projects/{id}", {
				params: { path: { id: projectId ?? "" } },
			});
			if (apiError) throw apiErrorException(apiError);
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
	const cloudConnectionsQuery = useProviderConnections();
	const cloudAgents = useMemo(() => cloudAgentInfos(cloudConnectionsQuery.data), [cloudConnectionsQuery.data]);
	const readyFallbackAgent = agentCatalog && (isStandalone || projectQuery.data) ? buildRankedAgentOptions({
		agents: agentCatalog.agents,
		priorityRank: DEFAULT_AGENT_PRIORITY_RANK,
		fallbackAgents: [],
	}).find(isReadyAgent)?.id ?? "" : "";
	const readyCloudAgent = isCloudProject && cloudConnectionsQuery.isSuccess ? buildRankedAgentOptions({
		agents: cloudAgents,
		priorityRank: DEFAULT_AGENT_PRIORITY_RANK,
		fallbackAgents: [],
	}).find(isReadyAgent)?.id ?? "" : "";
	const configuredDefaultAgent = isStandalone ? readyFallbackAgent
		: configuredProjectAgent || (isCloudProject ? readyCloudAgent : readyFallbackAgent);
	const rememberedAgent = persistedPreferences?.lastAgent ?? "";
	const availableAgents = isCloudProject ? cloudAgents : agentCatalog?.agents;
	const rememberedAgentIsAvailable = Boolean(
		availableAgents?.some((candidate) => candidate.id === rememberedAgent && (hostId ? isLaunchableAgent(candidate) : isReadyAgent(candidate))),
	);
	const defaultWorkerAgent = rememberedAgentIsAvailable ? rememberedAgent : configuredDefaultAgent;
	const selectedAgent = agent || defaultWorkerAgent;
	const accountProvider = agentAccountProviderFor(selectedAgent);
	const accountId = accountProvider && !isCloudProject ? accountPicks[accountProvider] : undefined;
	const selectedCloudConnection = isCloudProject
		? cloudConnectionsQuery.data?.find(
				(connection) => connection.provider === selectedAgent && connection.label === "default" && connection.validationState === "valid",
			)
		: undefined;
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
	const cloudCatalogAgent = isCloudProject && (CLOUD_AGENT_PROVIDERS as readonly string[]).includes(selectedAgent);
	const cloudCatalogState = useCloudModelCatalog({ orgId: cloudOrg?.id, agent: selectedAgent, enabled: cloudCatalogAgent });
	const cloudModelCatalogQuery = cloudCatalogState.query;
	const cloudCatalog = cloudCatalogState.catalog;
	const cloudCatalogLoading = cloudCatalogState.loading;
	const cloudCatalogGap = cloudCatalogState.gap;
	const cloudModelUnavailable = (id: string) => isCloudProject && cloudModelVerdict(cloudCatalog, id) === "unavailable";
	const cloudOrchestrator = useCloudOrchestratorModel({
		baseUrl: cloudBaseUrl,
		orgId: cloudOrg?.id,
		projectId,
		enabled: isCloudProject && !isCloudStandalone,
	});
	const orchestratorModelForSelectedAgent = isCloudProject && cloudOrchestrator && cloudOrchestrator.harness === selectedAgent &&
		isConcreteModelID(cloudOrchestrator.model) && !cloudModelUnavailable(cloudOrchestrator.model)
		? cloudOrchestrator.model
		: "";
	const localCatalogForCloud = listsLocalModelsForCloud(cloudCatalogGap, selectedAgent, modelScope);
	const localCatalogInUse = !isCloudProject || localCatalogForCloud;
	const localModelCatalogQuery = useQuery({
		...agentModelsQueryOptions(selectedAgent, modelScope, hostId),
		enabled: selectedAgent !== "" && localCatalogInUse,
	});
	const localCatalog = localCatalogInUse ? localModelCatalogQuery.data : undefined;
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
	const pickOrigin = useRef({ cloud: isCloudProject, context: preferenceContext, agent: selectedAgent });
	const parkedPicks = useRef<Record<string, ParkedComposerPicks>>({});
	const parkedDrafts = useRef<Record<string, Record<string, TaskComposerAgentPreference>>>({});
	const restoringModel = useRef(false);
	const restoringEffort = useRef(false);
	useEffect(() => {
		const origin = pickOrigin.current;
		if (origin.context === preferenceContext) return;
		if (origin.cloud === isCloudProject) {
			pickOrigin.current = { ...origin, context: preferenceContext };
			return;
		}
		parkedPicks.current[origin.context] = { agent: origin.agent, model, mode, effort, modelTouched, effortTouched };
		parkedDrafts.current[origin.context] = { ...agentDrafts };
		pickOrigin.current = { cloud: isCloudProject, context: preferenceContext, agent: selectedAgent };
		const entered = parkedPicks.current[preferenceContext];
		const ownAgent = entered !== undefined && entered.agent === selectedAgent;
		const keepModel = ownAgent && entered.modelTouched;
		const keepEffort = ownAgent && entered.effortTouched;
		restoringModel.current = keepModel;
		restoringEffort.current = keepEffort;
		for (const key of Object.keys(agentDrafts)) delete agentDrafts[key];
		Object.assign(agentDrafts, parkedDrafts.current[preferenceContext] ?? readTaskComposerPreferences(preferenceContext)?.agents);
		setModel(keepModel ? entered.model : "");
		setMode(keepModel ? entered.mode : "");
		setModelTouched(keepModel);
		setEffort(keepEffort ? entered.effort : "");
		setEffortTouched(keepEffort);
		// eslint-disable-next-line react-hooks/exhaustive-deps -- runs on a move only, with the picks as they were
	}, [isCloudProject, preferenceContext]);
	useEffect(() => {
		pickOrigin.current = { cloud: isCloudProject, context: preferenceContext, agent: selectedAgent };
		// eslint-disable-next-line react-hooks/exhaustive-deps -- records where each pick was made
	}, [model, mode, effort, agent, selectedAgent]);
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
	const modelAuthIssue = !isCloudProject && revalidationWarning === undefined ? modelCatalogAuthIssue(localCatalog) : undefined;
	useModelCatalogAuthRecovery(selectedAgent, hostId, modelAuthIssue);
	const { models: localOfferedModels } = useOfferedModels(selectedAgent, localCatalog?.models, {
		hostId,
		keep: [model, mode, projectModelForSelectedAgent, projectModeForSelectedAgent],
	});
	const listedCloudModels = useMemo((): AgentModelOption[] | undefined => cloudCatalog?.models.map((item) => ({
		id: item.id,
		label: cloudModelLabel(selectedAgent, item),
		isDefault: false,
		efforts: item.efforts,
		defaultEffort: item.defaultEffort,
	})), [cloudCatalog, selectedAgent]);
	const { models: offeredCloudModels } = useOfferedModels(selectedAgent, listedCloudModels, {
		keep: [model, mode, ...[orchestratorModelForSelectedAgent, projectModelForSelectedAgent, rememberedModel].filter((id) => !cloudModelUnavailable(id))],
		skip: !isCloudProject,
	});
	const offeredModels = useMemo(
		() => (isCloudProject ? withoutLocalAvailability(localOfferedModels) : localOfferedModels),
		[isCloudProject, localOfferedModels],
	);
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
	const keptCloudModels = [orchestratorModelForSelectedAgent, projectModelForSelectedAgent, rememberedModel]
		.filter((id) => isConcreteModelID(id) && !cloudModelUnavailable(id)).join("\n");
	const cloudModels = useMemo((): AgentModelOption[] => {
		if (cloudCatalog) {
			const listed = offeredCloudModels;
			const kept = keptCloudModels === "" ? [] : keptCloudModels.split("\n");
			const unlisted = kept.filter((id, index) => kept.indexOf(id) === index && !listed.some((item) => item.id === id));
			return [...listed, ...unlisted.map((id) => ({ id, label: id, isDefault: false }))];
		}
		return localCatalogForCloud ? offeredModels.map((item) => ({ ...item, isDefault: false })) : NO_MODELS;
	}, [cloudCatalog, keptCloudModels, localCatalogForCloud, offeredCloudModels, offeredModels]);
	const modelCatalog: TaskComposerModelCatalog | undefined = isCloudProject
		? {
				...(localCatalogForCloud ? localModelCatalog : undefined),
				allowCustom: true,
				customModelEntry: "direct",
				selectionMode: "catalog",
				models: cloudModels,
			}
		: localModelCatalog;
	const catalogModels = (isCloudProject ? cloudModels : offeredModels).filter((item) => isConcreteModelID(item.id));
	const selectableModels = catalogModels.filter(isModelSelectable);
	const catalogDefaultOption =
		isCloudProject ? "" : selectableModels.find((item) => item.isDefault)?.id ?? "";
	const catalogUsesModes = modelCatalog?.selectionMode === "mode";
	const cloudSessions = queryClient.getQueryData<CloudCpSession[]>([...cloudSessionsQueryKey, cloudBaseUrl, cloudOrg?.id ?? ""]);
	const cloudPicksAllowed = !isCloudProject || cloudCatalog !== undefined;
	const lastUsedOption = isCloudProject
		? cloudPicksAllowed ? lastCloudSessionModel(cloudSessions, selectedAgent, selectableModels) : ""
		: selectableModels
			.filter((item) => item.lastUsedAt)
			.sort((a, b) => Date.parse(b.lastUsedAt ?? "") - Date.parse(a.lastUsedAt ?? ""))[0]?.id ?? "";
	const claudeFallbackOption =
		cloudPicksAllowed && selectedAgent === "claude-code" && !catalogUsesModes
			? ((cloudCatalog ? cloudDefaultModel(cloudCatalog.models, selectableModels) : undefined)
				?? splitClaudeModels(selectableModels).current.find((item) => /opus/i.test(modelChoiceLabel(item))))?.id ?? ""
			: "";
	const rememberedModelIsValid =
		isConcreteModelID(rememberedModel) &&
		!cloudModelUnavailable(rememberedModel) &&
		!catalogModels.some((item) => item.id === rememberedModel && !isModelSelectable(item)) &&
		Boolean(
			modelCatalog &&
				(modelCatalog.allowCustom || catalogModels.some((item) => item.id === rememberedModel)),
		);
	const projectModelIsValid = isConcreteModelID(projectModelForSelectedAgent) && !cloudModelUnavailable(projectModelForSelectedAgent);
	const orchestratorModelIsValid = orchestratorModelForSelectedAgent !== "" &&
		!catalogModels.some((item) => item.id === orchestratorModelForSelectedAgent && !isModelSelectable(item));
	const rememberedModeIsValid =
		isConcreteModelID(rememberedMode) && catalogModels.some((item) => item.id === rememberedMode);
	const catalogPick = isCloudProject ? "" : catalogDefaultOption || selectableModels[0]?.id || "";
	const defaultModelForSelectedAgent =
		(rememberedModelIsValid ? rememberedModel : "") ||
		(projectModelIsValid ? projectModelForSelectedAgent : "") ||
		(orchestratorModelIsValid ? orchestratorModelForSelectedAgent : "") ||
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
		? (isConcreteModelID(selectedModelOrMode) ? selectedModelOrMode : undefined)
		: isStandalone
		? (isConcreteModelID(selectedModelOrMode) ? selectedModelOrMode : undefined)
		: selectedModelOrMode && selectedModelOrMode !== projectModelOrMode && (
				selectedModelOrMode !== catalogDefaultOption || isConcreteModelID(projectModelOrMode) ||
				(!catalogUsesModes && !projectModelOrMode)
			) ? selectedModelOrMode : undefined;
	const rememberedEffortIsExplicit = Boolean(
		rememberedConfigForSelectedAgent &&
			(rememberedConfigForSelectedAgent.effortChosen || !isCloudProject) &&
			Object.prototype.hasOwnProperty.call(rememberedConfigForSelectedAgent, "effort"),
	);
	const unmarkedRememberedEffort = isCloudProject && !rememberedConfigForSelectedAgent?.effortChosen
		? rememberedConfigForSelectedAgent?.effort ?? ""
		: "";
	const projectEffortForSelectedAgent = selectedAgent === configuredProjectAgent ? defaultWorkerEffort : "";
	const orchestratorEffort = orchestratorModelIsValid && selectedModel === orchestratorModelForSelectedAgent &&
		cloudOrchestrator?.reasoningEffort &&
		cloudCatalog?.models.find((item) => item.id === orchestratorModelForSelectedAgent)?.efforts?.includes(cloudOrchestrator.reasoningEffort)
		? cloudOrchestrator.reasoningEffort
		: "";
	const effortWithSelectedModel = projectModelIsValid && selectedModel === projectModelForSelectedAgent
		? projectEffortForSelectedAgent || orchestratorEffort
		: orchestratorEffort || projectEffortForSelectedAgent;
	const defaultEffortForSelectedAgent = rememberedEffortIsExplicit
		? (rememberedConfigForSelectedAgent?.effort ?? "")
		: effortWithSelectedModel || unmarkedRememberedEffort;
	const effortFromPreference = rememberedEffortIsExplicit || (!effortWithSelectedModel && unmarkedRememberedEffort !== "");
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
	useResetStaleProjectTuning({
		projectId: isCloudProject || isStandalone ? undefined : projectQuery.data?.id,
		hostId,
		agent: selectedAgent,
		agentLabel: selectedAgentLabel,
		projectAgent: configuredProjectAgent,
		config: isCloudProject ? undefined : projectQuery.data?.config,
		catalog: localCatalog,
	});
	const chatHarnesses = isCloudProject ? cloudOffering.chatHarnesses : settings?.chatHarnesses;
	const requiresTuiFallback =
		selectedAgent !== "" &&
		settings?.defaultSessionMode === "chat" &&
		!(chatHarnesses ?? []).includes(selectedAgent);
	const usesChatPermissions = !isCloudProject && !requiresTuiFallback && settings?.defaultSessionMode === "chat";
	const permissionDiscovery = useChatPermissionModesQuery(hostId, usesChatPermissions ? selectedAgent : undefined);
	const chatPermissionModes = permissionDiscovery.data?.get(selectedAgent);
	const projectPermissions = projectConfig?.worker?.agentConfig?.permissions ?? projectConfig?.agentConfig?.permissions;
	const wantedApproval = approvalMode ?? (isCloudProject ? "bypass-permissions" : APPROVAL_MODES.includes(projectPermissions as ApprovalMode) ? projectPermissions as ApprovalMode : "auto");
	const usesNativePermissions = usesChatPermissions && chatPermissionModes?.length === 1 && chatPermissionModes[0] === "default";
	const effectiveApproval = usesNativePermissions ? "default" : wantedApproval;
	const launchApproval = usesNativePermissions ? "default" : approvalMode;
	const aoDefaultEffort = requiresTuiFallback ? undefined : fallbackEffort(effortOptions, implicitEffort);
	const effectiveEffort = isCloudProject
		? effort && effort !== "default" && (cloudEffortsUnknown || effortOptions.includes(effort)) ? effort : cloudDefaultEffort
		: effort && effort !== "default"
		? effort
		: aoDefaultEffort ?? (isStandalone && effortOptions.includes(implicitEffort) ? implicitEffort : "");
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
		? (effectiveEffort || undefined)
		: effortTouched || effortFromPreference
			? !effectiveEffort || effectiveEffort === implicitEffort ? undefined : effectiveEffort
			: aoDefaultEffort;
	const effortAvailability: EffortAvailability = requiresTuiFallback
		? "launch-unavailable"
		: !effortModel || effortModel.efforts === undefined
			? "unknown"
			: effortOptions.length > 0
				? "supported"
				: "unsupported";
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
	const projectModelUnavailable = isConcreteModelID(projectModelForSelectedAgent) && cloudModelUnavailable(projectModelForSelectedAgent);
	const cloudModelWarning = !isCloudProject
		? undefined
		: cloudCatalog
			? cloudModelUnavailable(selectedModelOrMode)
				? t("newTask.cloudModelUnavailable", { model: selectedModelOrMode, agent: selectedAgentLabel })
				: projectModelUnavailable
					? t("newTask.cloudProjectModelUnavailable", { model: projectModelForSelectedAgent, agent: selectedAgentLabel })
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
		(!usesChatPermissions || !permissionDiscovery.isPending) &&
		(!isStandalone || selectedAgent !== "") &&
		selectedAgent !== "" &&
		(!hostId || Boolean(agentCatalog?.agents.some((candidate) => candidate.id === selectedAgent && isLaunchableAgent(candidate)))) &&
		(isCloudProject || isStandalone || projectQuery.data !== undefined) &&
		(!hostId || (agentsQuery.isSuccess && settings !== undefined)) &&
		!cloudModelUnavailable(selectedModelOrMode);
	const standaloneAgentError = !isCloudProject && selectedAgent === ""
		? agentsQuery.error instanceof Error ? agentsQuery.error.message : agentsQuery.isSuccess ? t("newTask.noReadyAgent") : undefined
		: isCloudProject && selectedAgent === "" && cloudConnectionsQuery.isSuccess ? t("newTask.noReadyAgent") : undefined;
	const projectError = isCloudProject || isStandalone ? undefined : projectQuery.error;
	const remoteLoadError = !hostId ? (projectError instanceof Error ? projectError.message : standaloneAgentError) : !hostConnected ? t("remote.hostOffline") :
		[projectError, agentsQuery.error].find((cause): cause is Error => cause instanceof Error)?.message ?? settingsError ??
			(agentsQuery.isSuccess && !agentCatalog?.agents.some(isLaunchableAgent) ? t("remote.noReadyAgent") : standaloneAgentError);
	const refreshSelectedModels = useCallback(async () => {
		if (isCloudProject) {
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
			harnessView: isCloudProject ? "cloud" : "local",
			startLogin: true,
			preserveProject: true,
		});
	}, [hostId, isCloudProject, openGlobalSettings, selectedAgent]);
	const loginRenewsItself = modelAuthIssue === "expired" && !hostId;
	const showModelNotice = Boolean(modelAuthIssue) && !loginRenewsItself;
	const modelWarningRetries = !(isCloudProject && cloudCatalog !== undefined &&
		(cloudModelUnavailable(selectedModelOrMode) || projectModelUnavailable));
	const modelWarning = isCloudProject
		? cloudModelWarning ?? (localCatalogForCloud ? modelWarningText : undefined)
		: modelAuthIssue && showModelNotice ? (
		<ModelCatalogNotice
			agentLabel={selectedAgentLabel}
			issue={modelAuthIssue}
			detail={modelWarningText}
			onLogin={startAgentLogin}
			onRetry={refreshSelectedModels}
		/>
	) : modelAuthIssue ? undefined : modelWarningText;
	useEffect(() => {
		if (!agentTouched) setAgent(defaultWorkerAgent);
	}, [agentTouched, defaultWorkerAgent]);
	useEffect(() => {
		if (!modelTouched && !restoringModel.current) {
			setModel(defaultModelForSelectedAgent);
			setMode(defaultModeForSelectedAgent);
		}
	}, [defaultModelForSelectedAgent, defaultModeForSelectedAgent, modelTouched]);
	useEffect(() => {
		if (!effortTouched && !restoringEffort.current) setEffort(defaultEffortForSelectedAgent || cloudDefaultEffort);
	}, [cloudDefaultEffort, defaultEffortForSelectedAgent, effortTouched]);
	useEffect(() => {
		restoringModel.current = false;
		restoringEffort.current = false;
	});

	useEffect(() => () => clearAttachments(), [clearAttachments]);

	const rememberedPreference = (on: "launch" | "agent-switch"): TaskComposerAgentPreference => {
		const modelChosen = modelTouched || rememberedModelIsValid || rememberedModeIsValid;
		if (!isCloudProject) {
			const keepModel = on === "agent-switch" || modelChosen;
			return {
				model: selectedModel && keepModel ? requestedModel ?? "" : "",
				mode: selectedMode && keepModel ? requestedModel ?? "" : "",
				...(requestedEffort !== undefined ? { effort: requestedEffort } : {}),
			};
		}
		const effortChosen = effortTouched
			? requestedEffort !== undefined && requestedEffort === effort
			: rememberedEffortIsExplicit;
		const keptEffort = effortTouched ? requestedEffort : rememberedConfigForSelectedAgent?.effort;
		return {
			model: selectedModel && modelChosen ? requestedModel ?? "" : "",
			mode: selectedMode && modelChosen ? requestedModel ?? "" : "",
			...(effortChosen && keptEffort !== undefined
				? { effort: keptEffort, effortChosen: true as const }
				: !effortTouched && unmarkedRememberedEffort !== "" ? { effort: unmarkedRememberedEffort } : {}),
		};
	};

	const launch = async (brief: string, { interfaceMode, approvalMode: requestedApproval, clientRequestId, replay, attachments: files }: TaskLaunchOptions = {}): Promise<string> => {
		if (!projectId || !canSubmit || isSubmitting) throw new Error(remoteLoadError ?? t("newTask.unableToStart"));

		setIsSubmitting(true);
		setError(undefined);
		setStatus(undefined);
		setErrorAction(undefined);
		setFallbackAction(undefined);
		const heldPreparation = taskPreparationRef.current;
		launchingPreparationRef.current = heldPreparation;
		let submittedPreparation = heldPreparation;
		try {
			if (!isCloudProject && selectedAgent) {
				void Promise.resolve()
					.then(() => ensureAgentReadiness([selectedAgent], "launch", hostId))
					.then((readiness) => cacheAgentReadiness(queryClient, readiness, hostId))
					.catch(() => {});
			}
			const attachmentPayloads = files ?? await toSettledPayload();
			submittedPreparation = heldPreparation || taskPreparationRef.current;
			launchingPreparationRef.current = submittedPreparation;
			const request: CreateTaskInput = {
				projectId,
				brief,
				...(branchOverride && !isCloudProject && !isStandalone ? { branch: branchOverride } : {}),
				...(replay ? {
					agent: replay.agent as CreateTaskInput["agent"],
					model: replay.model,
					effort: replay.effort,
					mode: replay.mode ?? (isCloudProject ? settings?.defaultSessionMode : undefined),
					approvalMode: replay.approvalMode,
					accountId: replay.accountId,
				} : {
					agent: selectedAgent ? (selectedAgent as CreateTaskInput["agent"]) : undefined,
					model: requestedModel,
					effort: requestedEffort,
					mode: interfaceMode ?? (isCloudProject ? settings?.defaultSessionMode : undefined),
					approvalMode: requestedApproval ?? launchApproval,
					accountId,
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
						undefined,
						retryErr.status,
					);
				}
			}
			const preparationAfterSubmit = taskPreparationRef.current;
			taskPreparationRef.current = "";
			cancelTaskPreparation(submittedPreparation, hostId);
			cancelTaskPreparation(preparationAfterSubmit, hostId);
			if (selectedAgent) {
				const preference = rememberedPreference("launch");
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
					: !isChatOnlyAgent(selectedAgent) && interfaceMode !== "tui" &&
							err instanceof TaskCreateError &&
							Boolean(err.code && CHAT_PREFLIGHT_CODES.has(err.code))
						? "tui"
						: undefined,
			);
			if (submittedPreparation && taskPreparationRef.current !== submittedPreparation) cancelTaskPreparation(submittedPreparation, hostId);
			let message = err instanceof Error ? err.message : t("newTask.unableToStart");
			if (err instanceof TaskCreateError) {
				if (err.code === "UNSUPPORTED_MODEL") {
					if (!modelTouched && catalogDefaultOption) {
						if (catalogUsesModes) setMode(catalogDefaultOption);
						else setModel(catalogDefaultOption);
						setModelTouched(true);
						message = t("newTask.savedModelUnsupported", { defaultValue: "The saved model is unavailable. The agent default is selected. Review it and start again." });
					} else message = t("newTask.selectedModelUnsupported", { defaultValue: "The selected model is unavailable. Choose another model before starting." });
				} else if (err.code === "UNSUPPORTED_EFFORT") {
					if (!effortTouched) {
						setEffort(effortModel?.defaultEffort ?? "");
						setEffortTouched(true);
						message = t("newTask.savedEffortUnsupported", { defaultValue: "The saved effort is unavailable. The agent default is selected. Review it and start again." });
					} else message = t("newTask.selectedEffortUnsupported", { defaultValue: "The selected effort is unavailable. Choose another level before starting." });
				} else if (err.code === "AGENT_BINARY_NOT_FOUND") {
					setErrorAction("install-agent");
					message = t("newTask.agentNotInstalled", { defaultValue: "This agent is not installed. Open Agent Harnesses settings to install it, then try again." });
				} else if (err.code === "DEFAULT_BRANCH_UNRESOLVED") {
					message = t("newTask.branchUnresolved", { defaultValue: "Choose a base branch in project settings, then start again." });
				} else if (err.code === "BRANCH_ALREADY_EXISTS") {
					setErrorAction("choose-branch");
					message = err.message;
				} else if (err.code === "BRANCH_CHECKED_OUT_ELSEWHERE") {
					if (!isCloudProject && !isStandalone) {
						setErrorAction("choose-branch");
						message = t("newTask.branchInUse", { defaultValue: "That branch is open in another worktree. Choose a new branch to start this task." });
					} else message = t("newTask.branchInUseElsewhere", { defaultValue: "That branch is open elsewhere. Choose another project or try again later." });
				}
			}
			setError(message);
			throw err instanceof TaskCreateError
				? new TaskCreateError(message, err.code, err.details, err.status)
				: err instanceof Error ? err : new Error(message);
		} finally {
			launchingPreparationRef.current = "";
			setIsSubmitting(false);
		}
	};
	const submitMode: TaskLaunchOptions["interfaceMode"] = isChatOnlyAgent(selectedAgent) ? "chat" : requiresTuiFallback ? "tui" : undefined;
	const launchFallback = (brief: string, clientRequestId?: string, files?: FileAttachmentPayload[]) =>
		fallbackAction === "bypass-permissions"
			? launch(brief, { interfaceMode: isChatOnlyAgent(selectedAgent) ? "chat" : undefined, approvalMode: "bypass-permissions", clientRequestId, attachments: files })
			: launch(brief, { interfaceMode: "tui", clientRequestId, attachments: files });

	const changeAgent = (value: string) => {
		setError(undefined);
		setStatus(undefined);
		setErrorAction(undefined);
		setBranchOverride("");
		if (selectedAgent) agentDrafts[selectedAgent] = rememberedPreference("agent-switch");
		setAgent(value);
		setAgentTouched(true);
		setModel("");
		setMode("");
		setEffort("");
		setModelTouched(false);
		setEffortTouched(false);
	};

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
	const switchAgent = (harness: string, pick: { model?: string; effort?: string } = {}) => {
		if (harness !== selectedAgent) changeAgent(harness);
		if (pick.model) {
			const catalog = isCloudProject ? undefined : queryClient.getQueryData(agentModelsQueryOptions(harness, catalogProjectId, hostId).queryKey);
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

	const shownEffort = isCloudProject ? effectiveEffort : effort;
	const showEffort = cursorChoice ? cursorChoice.family.efforts.length > 1 : !requiresTuiFallback && (effortOptions.length > 0 || Boolean(shownEffort && shownEffort !== "default"));
	const orchestratorModelHint = orchestratorModelIsValid
		? { id: orchestratorModelForSelectedAgent, label: t("newTask.sameAsOrchestrator") }
		: undefined;
	const chatModels = useMemo<ChatModel[]>(() => catalogModels.map((item) => ({
		id: item.id,
		displayName: item.id === "auto" ? t("settings.models.autoRouteLabel") : catalogUsesModes ? modelChoiceLabel(item) : item.label,
		default: Boolean(item.isDefault),
		efforts: item.efforts,
		defaultEffort: item.defaultEffort,
		...(orchestratorModelHint?.id === item.id ? { hint: orchestratorModelHint.label } : {}),
	})), [catalogModels, catalogUsesModes, orchestratorModelHint?.id, orchestratorModelHint?.label, t]);
	const turnSettings: TurnSettings = {
		model: (selectedModel || selectedMode) || undefined,
		reasoningEffort: showEffort ? effectiveEffort || undefined : undefined,
		approvalMode: effectiveApproval,
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
		launchSnapshot: (): LaunchReplay => ({
			agent: selectedAgent || undefined,
			model: requestedModel,
			effort: requestedEffort,
			approvalMode: launchApproval,
			mode: submitMode,
			accountId,
		}),
		account: accountProvider && !isCloudProject ? {
			provider: accountProvider,
			value: accountId,
			choose: (id: string | undefined) => setAccountPicks((picks) => ({ ...picks, [accountProvider]: id })),
		} : undefined,
		effectiveInterface: (submitMode ?? settings?.defaultSessionMode ?? "tui") as "chat" | "tui",
		refreshModels: refreshSelectedModels,
		preparedSessionId,
		submitMode,
		isCloudProject,
		isCoderCloudProject,
		canSubmit,
		isSubmitting,
		isDirty: modelTouched || effortTouched || attachments.length > 0,
		error,
		status,
		errorAction: errorAction === "install-agent" ? {
			label: t("newTask.openHarnessSettings", { defaultValue: "Open Agent Harnesses" }),
			onClick: () => openGlobalSettings("harness", { focusAgentId: selectedAgent, ...(hostId ? { hostId } : {}), harnessView: "local", preserveProject: true }),
		} : errorAction === "choose-branch" ? {
			label: t("newTask.chooseNewBranch", { defaultValue: "Choose new branch" }),
			input: {
				label: t("newTask.newBranchPrompt", { defaultValue: "New branch name" }),
				value: branchDraft,
				onChange: setBranchDraft,
			},
			onClick: () => {
				if (!branchDraft.trim()) return;
				setBranchOverride(branchDraft.trim());
				setErrorAction(undefined);
				setError(undefined);
				setStatus(t("newTask.newBranchReady", { branch: branchDraft.trim(), defaultValue: "Branch {{branch}} selected for your next task." }));
			},
		} : undefined,
		remoteLoadError,
		modelWarning,
		modelWarningRetries,
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
			projectId: isStandalone || isCloudStandalone ? "" : (projectId ?? ""),
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
		approvalModes: usesChatPermissions ? chatPermissionModes ?? [] : undefined,
		turnSettings,
		chooseTurnSettings,
		agentChoices,
		catalogProjectId,
		catalogCloudOrgId: isCloudProject ? cloudOrg?.id : undefined,
		switchAgent,
		renderAgentControl: (control: TaskComposerAgentControl) => <DesktopAgentControl {...control} hostId={hostId} manageView={isCloudProject ? "cloud" : "local"} />,
		renderEffortControl: (control: TaskComposerEffortControl) => <TaskEffortPicker {...control} value={control.value || cloudDefaultEffort} defaultEffort={cursorChoice ? undefined : inheritedEffort || effortModel?.defaultEffort} availability={cursorChoice ? "supported" : effortAvailability}
			footer={cursorChoice?.family.hasThinkingChoice ? <ToggleRow label={t("agentSwitch.thinking")} checked={cursorChoice.variant.thinking} onChange={(thinking) => pickCursorVariant({ thinking })} /> : undefined} />,
		renderModelControl: (control: TaskComposerModelControl) => <TaskModelPicker {...control}
			hostId={hostId}
			onRefresh={isCloudProject && !cloudCatalogAgent ? undefined : refreshSelectedModels}
			allowNativeSelection={isCloudProject}
			footer={cloudModelSource}
			modelHint={orchestratorModelHint}
			showFollowAgentAction={Boolean(catalogDefaultOption || !isConcreteModelID(projectModelOrMode))} />,
	};
}

export type TaskLaunch = ReturnType<typeof useTaskLaunch>;

function startsLocally(agent: AgentInfo): boolean {
	return agent.installation.state === "installed" && (isReadyAgent(agent) || agent.authentication.state === "configured");
}

type ParkedComposerPicks = {
	agent: string;
	model: string;
	mode: string;
	effort: string;
	modelTouched: boolean;
	effortTouched: boolean;
};

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
				status: task.status,
				errorAction: task.errorAction,
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

export function lastCloudSessionModel(sessions: CloudCpSession[] | undefined, agent: string, catalog: { id: string }[]): string {
	const newest = (sessions ?? [])
		.filter((session) => session.harness === agent && isConcreteModelID(session.model ?? "") && catalog.some((item) => item.id === session.model))
		.sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt))[0];
	return newest?.model ?? "";
}


function rememberCloudChatSettings(
	orgId: string,
	sessionId: string,
	harness: string,
	input: Pick<CreateTaskInput, "model" | "effort" | "approvalMode">,
): void {
	const saved = {
		...(input.model ? { model: input.model } : {}),
		...(input.effort ? { reasoningEffort: input.effort } : {}),
		...(input.approvalMode ? { approvalMode: input.approvalMode, approvalModeChosen: true } : {}),
	};
	if (Object.keys(saved).length === 0) return;
	try {
		window.localStorage.setItem(`cloud-chat-settings:${orgId}:${sessionId}:${toAgentProvider(harness)}`, JSON.stringify(saved));
	} catch {
	}
}

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
