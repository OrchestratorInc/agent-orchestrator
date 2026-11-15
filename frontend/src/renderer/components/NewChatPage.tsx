import { apiErrorException } from "../lib/api-error-exception";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useRouter } from "@tanstack/react-router";
import { Cloud, Folder, Plus, Smartphone, StickyNote } from "@/components/app-icons";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useRemoteWorkspaces, useWorkspaceQuery, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { seedOpeningLocalEcho, useStageAttachments, type ConversationLocalEcho } from "../hooks/useConversation";
import { purgeFileAttachments, type FileAttachmentPayload } from "../hooks/useFileAttachments";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { PLAN_REFUSAL_CODES } from "../lib/cloud-cp/client";
import { dropReportPrompt, sessionNameFromBrief } from "../lib/report-problem";
import { chatDraftScopeKey, newChatDraftScope, readChatSessionDraft, writeChatAttachments, writeChatComposerText } from "../lib/chat-drafts";
import { clientForHost, labelForHost } from "../lib/host-clients";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { openComposerModelPicker, toggleComposerDictation } from "../lib/composer-shortcuts";
import { useSessionShortcut } from "../hooks/useAppShortcut";
import { CLOUD_PROJECT_KIND, CLOUD_STANDALONE_WORKSPACE_ID, isScratchpadWorkspace, STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { STANDALONE_AGENTS_FEATURE } from "../lib/cloud-offering";
import { standaloneAgentProjectId } from "../lib/cloud-standalone-agents";
import type { ApprovalMode, ChatConfigOption, ChatConfigOptionValue, ChatModel, ConversationSnapshot, TurnSettings } from "../types/conversation";
import { readChatSettingsTemplate, readChatSkills } from "../lib/chat-settings-template";
import { agentModelDisplayLabel } from "../lib/agent-model-choices";
import { ChatWorkspace } from "./chat/ChatWorkspace";
import { startingConversationSnapshot } from "./chat/OrchestratorStartingChat";
import { LaunchSettingsBar, rememberLaunchSettings, rememberOpeningStart, type LaunchSettings } from "./chat/LaunchSettingsBar";
import { hasProviderPermissionMode, partitionConfigOptions, TRIGGER_CLASS, TurnSettingsBar } from "./chat/TurnSettingsBar";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";
import { fetchDevices, isPhone, mobileDevicesQueryKey } from "./settings/MobileDevicesSection";
import { useUiStore } from "../stores/ui-store";
import { BareTopbarDragStrip } from "./BareTopbarDragStrip";
import { SessionTabsTopbar } from "./PageTopbar";
import { useTaskLaunch, type LaunchReplay } from "./TaskComposer";
import { agentAccountLabel, useAgentAccounts, withoutSameLogins } from "../hooks/useAgentAccounts";
import type { AccountChoice } from "./chat/AgentSwitchPicker";
import { Button } from "./ui/button";
import { cn } from "../lib/utils";
import { MAX_CLOUD_CP_BINARY_BYTES } from "../../shared/cloud-binary";

export function NewChatPage({ projectId, hostId, report }: { projectId: string; hostId?: string; report?: boolean }) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const router = useRouter();
	const queryClient = useQueryClient();
	const task = useTaskLaunch({ projectId, hostId });
	const hostAccounts = useAgentAccounts(Boolean(task.account), hostId);
	const providerRows = task.account ? (hostAccounts.data ?? []).filter((row) => row.provider === task.account?.provider) : [];
	const accountRows = withoutSameLogins(providerRows, task.account?.value ?? providerRows.find((row) => row.default)?.id);
	const accountChoice: AccountChoice | undefined = task.account && accountRows.length > 0 ? {
		options: accountRows.map((row) => ({ id: row.id, label: agentAccountLabel(row, accountRows, t), disabled: !row.signedIn })),
		value: task.account.value ?? accountRows.find((row) => row.default)?.id,
		onPick: task.account.choose,
		disabled: accountRows.length < 2,
	} : undefined;
	const taskRef = useRef(task);
	taskRef.current = task;
	const stalePick = Boolean(task.account?.value && hostAccounts.data && !accountRows.some((row) => row.id === task.account?.value && row.signedIn));
	useEffect(() => {
		if (stalePick) taskRef.current.account?.choose(undefined);
	}, [stalePick]);
	const targetRef = useRef({ projectId, hostId });
	targetRef.current = { projectId, hostId };
	useSessionShortcut("model-picker", openComposerModelPicker);
	useSessionShortcut("dictate", toggleComposerDictation);
	const mounted = useRef(true);
	useEffect(() => {
		mounted.current = true;
		return () => {
			mounted.current = false;
		};
	}, []);
	const hostKey = hostId ?? "local";
	const draftId = `new-chat:${hostKey}`;
	const draftRevision = useUiStore((state) => state.newChatDraftRevision);
	const draftScope = useMemo(() => newChatDraftScope(hostId), [hostId]);
	const [echo, setEcho] = useState<ConversationLocalEcho>();
	const [launched, setLaunched] = useState<LaunchSettings>();
	const [fallbackFailed, setFallbackFailed] = useState(false);
	const [reportedLoadError, setReportedLoadError] = useState<string>();
	const [refusedRetry, setRefusedRetry] = useState<string>();
	const fallbackRequest = useRef<string | undefined>(undefined);
	const fallbackInFlight = useRef(false);
	const [heldAttachments, setHeldAttachments] = useState<FileAttachmentPayload[]>([]);
	const [attachmentsPreparing, setAttachmentsPreparing] = useState(false);
	const onHeldAttachmentsChange = useCallback((attachments: FileAttachmentPayload[], preparing: boolean) => {
		setHeldAttachments(attachments);
		setAttachmentsPreparing(preparing);
	}, []);
	const [, setLaunchRevision] = useState(0);
	const preparedId = task.preparedSessionId || undefined;
	const workspaceFiles = useQuery({
		queryKey: ["new-chat-files", hostKey, preparedId ?? ""],
		enabled: Boolean(preparedId),
		retry: false,
		staleTime: 30_000,
		refetchInterval: (query) => (query.state.data ? 30_000 : Math.min(1_500 * 2 ** query.state.errorUpdateCount, 15_000)),
		queryFn: async () => {
			const { data, error } = await (hostId ? clientForHost(hostId) : apiClient).GET("/api/v1/sessions/{sessionId}/workspace/files", {
				params: { path: { sessionId: preparedId as string } },
			});
			if (error) throw error;
			return {
				paths: (data?.files ?? []).filter((file) => file.status !== "deleted").map((file) => file.path),
				truncated: Boolean(data?.truncated),
			};
		},
	});
	const stagePrepared = useStageAttachments(preparedId, hostId);
	const cloudOffering = useCloudOffering(task.isCloudProject);
	const cloudTaskTakesFiles = task.isCloudProject && projectId !== CLOUD_STANDALONE_WORKSPACE_ID;
	const cloudFiles = useMemo(() => cloudTaskTakesFiles && cloudOffering.maxAttachmentBytes !== undefined
		? { maxBytes: Math.min(cloudOffering.maxAttachmentBytes, MAX_CLOUD_CP_BINARY_BYTES) } : undefined,
	[cloudOffering.maxAttachmentBytes, cloudTaskTakesFiles]);
	const attachmentsRefusal = projectId === CLOUD_STANDALONE_WORKSPACE_ID && task.isCloudProject
		? t("newTask.cloudStandaloneNoFiles", { defaultValue: "Files can't start a standalone agent. Remove them to continue." })
		: task.isCloudProject && cloudOffering.loaded && !cloudFiles
			? t("newTask.cloudProjectNoFiles", { defaultValue: "Files can't start a task in this Cloud project yet. Remove them to continue." })
			: undefined;
	const workspaceReady = Boolean(preparedId && workspaceFiles.data);
	const attachmentsProject = useRef(projectId);
	if (attachmentsProject.current !== projectId) {
		attachmentsProject.current = projectId;
		writeChatAttachments(draftScope, []);
	}
	const skills = useMemo(() => task.isCloudProject ? [] : readChatSkills(task.agent.value, hostId), [hostId, task.agent.value, task.isCloudProject]);
	const snapshot = useMemo<ConversationSnapshot>(
		() => ({ ...startingConversationSnapshot(draftId, task.agent.value || "claude-code"), controller: { state: "ready" } }),
		[draftId, task.agent.value],
	);
	const harness = task.agent.value;
	const template = useMemo(
		() => task.isCloudProject ? undefined : readChatSettingsTemplate(harness, hostId),
		[harness, hostId, task.isCloudProject],
	);
	const barFor = (settings: TurnSettings): LaunchSettings => {
		const providerOptions = template ? draftProviderOptions(template.options, harness, task.chatModels, settings, task.approvalModes) : undefined;
		return {
			harness,
			projectId: task.catalogProjectId,
			hostId,
			models: providerOptions ? [] : task.chatModels,
			settings,
			configOptions: providerOptions,
			showApprovalMode: !task.approvalModes || task.approvalModes.length > 0,
			approvalModes: task.approvalModes,
			...(task.isCloudProject ? { unsetModelLabel: t("newTask.cloudConfiguredDefault") } : {}),
		};
	};
	const barSettings = barFor(task.turnSettings);
	const providerOptions = barSettings.configOptions;
	const barRef = useRef(barSettings);
	barRef.current = barSettings;
	const chooseProviderOption = (optionId: string, value: ChatConfigOptionValue) => {
		const option = template?.options.find((candidate) => candidate.id === optionId);
		if (!option || !("value" in value)) return;
		const grouped = partitionConfigOptions(template?.options ?? []);
		const choice = option.choices.find((candidate) => candidate.value === value.value);
		if (grouped.model.some((candidate) => candidate.id === optionId)) {
			const model = choice ? catalogModelFor(choice, harness, task.chatModels) : undefined;
			if (model) task.chooseTurnSettings({ ...task.turnSettings, model: model.id });
		} else if (grouped.effort.some((candidate) => candidate.id === optionId)) {
			task.chooseTurnSettings({ ...task.turnSettings, reasoningEffort: value.value === "default" ? undefined : value.value });
		} else if (grouped.mode?.id === optionId && choice?.permissionMode) {
			task.chooseTurnSettings({ ...task.turnSettings, approvalMode: choice.permissionMode });
		}
	};

	const pendingLaunch = readPendingLaunch(hostKey, draftScope);
	useEffect(() => {
		if (!pendingLaunch || pendingLaunch.projectId === projectId) return;
		void navigate({
			to: "/new",
			search: { project: pendingLaunch.projectId === STANDALONE_WORKSPACE_ID ? undefined : pendingLaunch.projectId, host: hostId, report },
			replace: true,
		});
	}, [hostId, navigate, pendingLaunch, projectId, report]);
	const listedWorkspaces = useWorkspaceQuery({ enabled: !hostId }).data;
	const agentWorkspace = hostId ? undefined : listedWorkspaces?.find((workspace) => workspace.id === projectId && workspace.standaloneAgent);
	useEffect(() => {
		if (!agentWorkspace || pendingLaunch || report) return;
		void navigate({
			to: "/new",
			search: { project: agentWorkspace.shared ? undefined : CLOUD_STANDALONE_WORKSPACE_ID, host: undefined },
			replace: true,
		});
	}, [agentWorkspace, navigate, pendingLaunch, report]);
	const resolveLaunch = (clientMessageId: string) => {
		clearPendingLaunch(hostKey, clientMessageId);
		setLaunchRevision((revision) => revision + 1);
	};

	const pendingRef = useRef(false);
	useEffect(() => {
		let settingsOwnsEscape = false;
		const noteSettings = (event: KeyboardEvent) => {
			if (event.key === "Escape") settingsOwnsEscape = useUiStore.getState().settingsModal !== null;
		};
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key !== "Escape" || event.defaultPrevented || event.isComposing || event.keyCode === 229) return;
			if (settingsOwnsEscape || pendingRef.current) return;
			if (document.querySelector('[role="menu"], [role="dialog"], [role="alertdialog"], [role="listbox"]')) return;
			if (router.history.canGoBack()) router.history.back();
		};
		window.addEventListener("keydown", noteSettings, true);
		window.addEventListener("keydown", onKeyDown);
		return () => {
			window.removeEventListener("keydown", noteSettings, true);
			window.removeEventListener("keydown", onKeyDown);
		};
	}, [router]);

	useEffect(() => {
		if (!task.isCoderCloudProject) return;
		void navigate({ to: "/new", search: { host: hostId }, replace: true });
		useUiStore.getState().requestNewTask(projectId, hostId);
	}, [hostId, navigate, projectId, task.isCoderCloudProject]);

	useEffect(() => {
		void router.preloadRoute(sessionNavigateTarget(projectId, "preload", hostId)).catch(() => undefined);
	}, [hostId, projectId, router]);

	const openSession = async (
		sessionId: string,
		brief: ConversationLocalEcho,
		settings: LaunchSettings,
		interfaceMode: "chat" | "tui",
		target: { projectId: string; hostId?: string },
	) => {
		if (!taskRef.current.isCloudProject && interfaceMode === "chat") {
			seedOpeningLocalEcho(queryClient, sessionId, target.hostId, brief);
			rememberLaunchSettings(queryClient, sessionId, target.hostId, settings);
			rememberOpeningStart(queryClient, sessionId, target.hostId, brief.createdAt);
		}
		if (!taskRef.current.isCloudProject) await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(target.hostId) });
		const sessionProjectId = target.projectId === CLOUD_STANDALONE_WORKSPACE_ID ? standaloneAgentProjectId(sessionId) : target.projectId;
		setTimeout(() => {
			if (!mounted.current) return;
			const here = targetRef.current;
			if (here.projectId === target.projectId && here.hostId === target.hostId) {
				void navigate({ ...sessionNavigateTarget(sessionProjectId, sessionId, target.hostId), replace: true });
				return;
			}
			setEcho(undefined);
			setLaunched(undefined);
		}, 0);
	};

	const send = async (text: string, attachments?: FileAttachmentPayload[], clientMessageId: string = crypto.randomUUID()) => {
		const target = { ...targetRef.current };
		const saved = readPendingLaunch(hostKey, draftScope, clientMessageId);
		const brief = { clientMessageId, text, createdAt: new Date().toISOString() };
		setEcho(brief);
		setRefusedRetry(undefined);
		setLaunched(saved?.settings ?? barRef.current);
		try {
			if (saved && saved.projectId !== target.projectId) throw new Error(t("newTask.unableToStart"));
			if (!(await until(() => !mounted.current || taskRef.current.canSubmit || Boolean(taskRef.current.remoteLoadError), 10_000)) || !mounted.current || !taskRef.current.canSubmit) {
				setReportedLoadError(taskRef.current.remoteLoadError);
				throw Object.assign(new Error(taskRef.current.remoteLoadError ?? t("newTask.unableToStart")), { code: "TASK_NOT_READY" });
			}
			const here = targetRef.current;
			if (here.projectId !== target.projectId || here.hostId !== target.hostId) {
				throw Object.assign(new Error(t("newTask.unableToStart")), { code: "TASK_RETARGETED" });
			}
			const launch = taskRef.current;
			const settings = saved?.settings ?? barRef.current;
			const replay = saved?.replay ?? launch.launchSnapshot();
			setLaunched(settings);
			writePendingLaunch(hostKey, { clientMessageId, projectId: target.projectId, replay, settings });
			const sessionId = await launch.launch(text, { clientRequestId: clientMessageId, replay, attachments });
			resolveLaunch(clientMessageId);
			await openSession(sessionId, brief, settings, replay.mode ?? launch.effectiveInterface, target);
		} catch (error) {
			setEcho(undefined);
			if (saved && !sendIsUncertain(error)) setRefusedRetry(apiErrorMessage(error, t("newTask.unableToStart")));
			if (sendIsUncertain(error) || saved) throw error;
			resolveLaunch(clientMessageId);
			throw Object.assign(apiErrorException(error, t("newTask.unableToStart")), { code: "TASK_CREATE_FAILED" });
		}
	};

	const sendFallback = async () => {
		const text = readChatSessionDraft(draftScope).composer.text.trim();
		if (!text || fallbackInFlight.current || attachmentsPreparing) return;
		fallbackInFlight.current = true;
		const target = { ...targetRef.current };
		fallbackRequest.current ??= crypto.randomUUID();
		const clientMessageId = fallbackRequest.current;
		const brief = { clientMessageId, text, createdAt: new Date().toISOString() };
		const fallback = task.fallbackAction;
		const settings = fallback === "bypass-permissions"
			? barFor({ ...task.turnSettings, approvalMode: "bypass-permissions" })
			: barSettings;
		setFallbackFailed(false);
		setEcho(brief);
		setLaunched(settings);
		try {
			const sessionId = await task.launchFallback(brief.text, clientMessageId, heldAttachments);
			fallbackRequest.current = undefined;
			writeChatComposerText(draftScope, "");
			writeChatAttachments(draftScope, []);
			purgeFileAttachments(chatDraftScopeKey(draftScope));
			await openSession(sessionId, brief, settings, fallback === "tui" ? "tui" : task.effectiveInterface, target);
		} catch (error) {
			if (!sendIsUncertain(error)) fallbackRequest.current = undefined;
			setFallbackFailed(true);
			setEcho(undefined);
		} finally {
			fallbackInFlight.current = false;
		}
	};

	const pending = Boolean(echo);
	const frozen = pending || Boolean(pendingLaunch);
	const frozenSettings = launched ?? pendingLaunch?.settings;
	useEffect(() => {
		if (!report || frozen || projectId === STANDALONE_WORKSPACE_ID) return;
		void navigate({ to: "/new", search: { host: hostId, report }, replace: true });
	}, [frozen, hostId, navigate, projectId, report]);
	useEffect(() => {
		if (!report && !frozen) dropReportPrompt(draftScope);
	}, [draftScope, frozen, report]);
	pendingRef.current = pending;
	useLayoutEffect(() => {
		useUiStore.getState().setNewChatSending(pending);
		return () => useUiStore.getState().setNewChatSending(false);
	}, [pending]);
	const notice = pending ? null : task.error && (task.errorAction || fallbackFailed) ? (
		<div role="alert" className="flex items-center gap-2 px-1 text-xs text-destructive">
			<span>{task.error}</span>
			{task.errorAction ? <div className="flex items-center gap-2">
				{task.errorAction.input ? <input
					aria-label={task.errorAction.input.label}
					value={task.errorAction.input.value}
					onChange={(event) => task.errorAction?.input?.onChange(event.target.value)}
					className="min-w-0 max-w-40 rounded border border-current bg-background px-2 py-1 text-foreground"
				/> : null}
				<Button size="sm" variant="ghost" disabled={Boolean(task.errorAction.input && !task.errorAction.input.value.trim())} onClick={task.errorAction.onClick}>{task.errorAction.label}</Button>
			</div> : null}
		</div>
	) : task.status ? (
		<p role="status" className="px-1 text-xs text-foreground">{task.status}</p>
	) : task.fallbackAction ? (
		<div className="flex items-center justify-end">
			<Button size="sm" variant="secondary" disabled={attachmentsPreparing} onClick={() => void sendFallback()}>{task.fallbackLabel}</Button>
		</div>
	) : pendingLaunch && refusedRetry ? (
		<p role="alert" className="px-1 text-xs text-destructive">{refusedRetry}</p>
	) : task.remoteLoadError && task.remoteLoadError !== reportedLoadError ? (
		<p role="alert" className="px-1 text-xs text-destructive">{task.remoteLoadError}</p>
	) : typeof task.modelWarning === "string" ? (
		<div className="flex items-center gap-2 px-1">
			<p role="status" className="min-w-0 text-xs text-warning">{task.modelWarning}</p>
			{task.modelWarningRetries ? (
				<Button size="xs" variant="ghost" className="shrink-0" onClick={() => void task.refreshModels().catch(() => undefined)}>
					{t("newTask.modelNotice.retry")}
				</Button>
			) : null}
		</div>
	) : task.modelWarning;

	return (
		<div className="flex h-full min-h-0 flex-col">
			<div aria-hidden="true" className="new-chat-topbar flex h-toolbar w-full shrink-0 overflow-hidden">
				<SessionTabsTopbar hideActions tabs={null} />
			</div>
			<BareTopbarDragStrip />
			<ChatWorkspace
				key={`${projectId}:${draftRevision}`}
				snapshot={snapshot}
				uiSessionId={draftId}
				parkUnsentSends={false}
				sessionTitle={echo ? sessionNameFromBrief(echo.text) : t("command.newTask")}
				sessionRole="worker"
				hideHeader
				centerInWindow
				welcomeHeading={report ? t("report.heading") : undefined}
				localEchos={echo ? [echo] : undefined}
				openingStartedAt={echo?.createdAt}
				holdAttachments={!task.isCloudProject || Boolean(cloudFiles) || Boolean(pendingLaunch)}
				cloudAttachments={cloudFiles}
				attachmentsRefusal={pendingLaunch ? undefined : attachmentsRefusal}
				onHeldAttachmentsChange={onHeldAttachmentsChange}
				onStageAttachments={workspaceReady ? stagePrepared : undefined}
				filePaths={workspaceFiles.data?.paths}
				filePathsTruncated={workspaceFiles.data?.truncated}
				skills={skills}
				composerTray={
					<>
						<NewChatProjectPicker projectId={projectId} hostId={hostId} disabled={frozen || report} onChange={(project) => void navigate({ to: "/new", search: { project, host: hostId }, replace: true })} />
						{hostId ? null : <MobileConnected />}
					</>
				}
				onSend={send}
				busy={pending}
				draftSettings={frozen ? (frozenSettings ? <LaunchSettingsBar {...frozenSettings} /> : null) : (
					<>
						<TurnSettingsBar
							models={barSettings.models}
							settings={barSettings.settings}
							harness={harness}
							onChange={providerOptions && hasProviderPermissionMode(providerOptions) ? undefined : task.chooseTurnSettings}
							configOptions={providerOptions}
							onChangeConfigOption={providerOptions ? chooseProviderOption : undefined}
							showApprovalMode={barSettings.showApprovalMode}
							unsetModelLabel={barSettings.unsetModelLabel}
							approvalModes={barSettings.approvalModes}
							agentSwitch={{
								projectId: task.catalogProjectId,
								cloudOrgId: task.catalogCloudOrgId,
								hostId,
								agents: task.agentChoices,
								disabled: task.agent.disabled,
								onPendingChange: (next) => {
									if (next) task.switchAgent(next.harness, { model: next.model, effort: next.effort ?? next.shownEffort });
								},
								account: accountChoice,
							}}
							autoSelectEffortOnOpen
						/>
					</>
				)}
				draftNotice={notice}
			/>
		</div>
	);
}

function draftProviderOptions(options: ChatConfigOption[], harness: string, models: ChatModel[], settings: TurnSettings, approvalModes?: ApprovalMode[]): ChatConfigOption[] | undefined {
	const grouped = partitionConfigOptions(options);
	const out: ChatConfigOption[] = [];
	const model = grouped.model[0];
	if (!model) return undefined;
	const rows = options.find((option) => option.id === model.id)?.choices ?? model.choices;
	const listed = (item: ChatModel) => rows.some((choice) =>
		choice.value === item.id || choice.name === agentModelDisplayLabel(harness, item.displayName));
	if (models.some((item) => item.id !== "default" && !listed(item))) return undefined;
	const modelChoices = rows.flatMap((choice) => {
		const listed = catalogModelFor(choice, harness, models);
		return listed ? [listed.hint ? { ...choice, hint: listed.hint } : choice] : [];
	});
	const chosen = models.find((item) => item.id === settings.model);
	const label = chosen ? agentModelDisplayLabel(harness, chosen.displayName) : undefined;
	const modelValue = modelChoices.find((choice) => choice.value === settings.model)?.value ??
		modelChoices.find((choice) => choice.name === label)?.value;
	if (!modelValue) return undefined;
	out.push({ ...model, choices: modelChoices, currentValue: modelValue });
	const effort = grouped.effort[0];
	if (effort) {
		const offered = new Set(chosen?.efforts ?? []);
		const listedEfforts = effort.choices.map((choice) => choice.value).filter((value) => value !== "default");
		if (listedEfforts.length !== offered.size || listedEfforts.some((value) => !offered.has(value))) return undefined;
		out.push({ ...effort, currentValue: settings.reasoningEffort || "default" });
	}
	if (grouped.mode) {
		const choices = grouped.mode.choices.filter((choice) => choice.permissionMode && (!approvalModes || approvalModes.includes(choice.permissionMode)));
		const value = choices.find((choice) => choice.permissionMode === settings.approvalMode)?.value;
		if (!value) return undefined;
		out.push({ ...grouped.mode, choices, currentValue: value });
	}
	return out;
}

const POST_START_ERRORS = new Set([
	"SPAWN_COMMIT_FAILED", "SPAWN_INTERNAL", "SPAWN_PROMPT_DELIVERY_FAILED", "SPAWN_LAUNCH_COMMAND_FAILED",
	"SPAWN_SUPERVISOR_FAILED", "SPAWN_PREPARE_LAUNCH_FAILED", "RUNTIME_CREATE_FAILED",
]);

function sendIsUncertain(error: unknown): boolean {
	if (!(error instanceof Error)) return true;
	const code = "code" in error && typeof error.code === "string" ? error.code : undefined;
	const status = "status" in error && typeof error.status === "number" ? error.status : undefined;
	if (status !== undefined && (status === 0 || status >= 500)) return true;
	if (status === 429) return !(code && PLAN_REFUSAL_CODES.has(code));
	if (code) return POST_START_ERRORS.has(code);
	return error.name !== "TaskCreateError";
}

type PendingLaunch = { clientMessageId: string; projectId: string; replay: LaunchReplay; settings: LaunchSettings };
const PENDING_LAUNCH_KEY = "ao.newTask.pendingLaunch.v1";

function readPendingLaunches(): Record<string, PendingLaunch> {
	try {
		const parsed: unknown = JSON.parse(window.localStorage.getItem(PENDING_LAUNCH_KEY) ?? "{}");
		return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? (parsed as Record<string, PendingLaunch>) : {};
	} catch {
		return {};
	}
}

function readPendingLaunch(hostKey: string, scope: { sessionId: string; incarnation: string }, clientMessageId?: string): PendingLaunch | undefined {
	const saved = readPendingLaunches()[hostKey];
	if (!saved) return undefined;
	if (readChatSessionDraft(scope).composer.delivery?.clientMessageId !== saved.clientMessageId) {
		clearPendingLaunch(hostKey);
		return undefined;
	}
	return clientMessageId === undefined || saved.clientMessageId === clientMessageId ? saved : undefined;
}

function writePendingLaunch(hostKey: string, launch: PendingLaunch): void {
	try {
		window.localStorage.setItem(PENDING_LAUNCH_KEY, JSON.stringify({ ...readPendingLaunches(), [hostKey]: launch }));
	} catch {
	}
}

function clearPendingLaunch(hostKey: string, clientMessageId?: string): void {
	const all = readPendingLaunches();
	if (!(hostKey in all)) return;
	if (clientMessageId !== undefined && all[hostKey]?.clientMessageId !== clientMessageId) return;
	delete all[hostKey];
	try {
		window.localStorage.setItem(PENDING_LAUNCH_KEY, JSON.stringify(all));
	} catch {
	}
}

async function until(ready: () => boolean, ms: number): Promise<boolean> {
	const deadline = Date.now() + ms;
	while (!ready() && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 100));
	return ready();
}

function catalogModelFor(choice: { value: string; name: string }, harness: string, models: ChatModel[]): ChatModel | undefined {
	return models.find((item) => item.id === choice.value) ??
		models.find((item) => agentModelDisplayLabel(harness, item.displayName) === choice.name);
}

function NewChatProjectPicker({ projectId, hostId, disabled, onChange }: { projectId: string; hostId?: string; disabled?: boolean; onChange: (projectId: string) => void }) {
	const { t } = useTranslation();
	const localWorkspaces = useWorkspaceQuery().data ?? [];
	const remoteWorkspaces = useRemoteWorkspaces().data ?? [];
	const workspaces = hostId ? remoteWorkspaces.filter((workspace) => workspace.hostId === hostId) : localWorkspaces;
	const offering = useCloudOffering(!hostId);
	const cloudStandalone = !hostId && offering.ready && Boolean(offering.org) && offering.features.includes(STANDALONE_AGENTS_FEATURE);
	const onCloudProject = projectId === CLOUD_STANDALONE_WORKSPACE_ID ||
		workspaces.some((workspace) => workspace.id === projectId && workspace.kind === CLOUD_PROJECT_KIND);
	const projects = workspaces.filter(
		(workspace) => workspace.id !== STANDALONE_WORKSPACE_ID && !isScratchpadWorkspace(workspace) &&
			(onCloudProject || workspace.kind !== CLOUD_PROJECT_KIND),
	);
	const cloudProjectIds = new Set([
		CLOUD_STANDALONE_WORKSPACE_ID,
		...projects.filter((project) => project.kind === CLOUD_PROJECT_KIND).map((project) => project.id),
	]);
	const projectOptions = [
		{ value: STANDALONE_WORKSPACE_ID, label: t("standalone.workspaceName") },
		...(cloudStandalone || projectId === CLOUD_STANDALONE_WORKSPACE_ID
			? [{ value: CLOUD_STANDALONE_WORKSPACE_ID, label: t("standalone.cloudWorkspaceName") }]
			: []),
		...projects.map((project) => ({ value: project.id, label: project.name })),
	];
	const selectedProjectName = projectId === STANDALONE_WORKSPACE_ID
		? t("standalone.workspaceName")
		: projectId === CLOUD_STANDALONE_WORKSPACE_ID
			? t("standalone.cloudWorkspaceName")
			: projects.find((project) => project.id === projectId)?.name ?? t("newTask.selectProject");
	const hostLabel = hostId ? labelForHost(hostId) ?? hostId : undefined;
	const requestCreateProject = useUiStore((state) => state.requestCreateProject);
	return (
		<SettingsOptionMenu
			aria-label={`${t("newTask.project")}: ${selectedProjectName}`}
			value={projectId}
			options={projectOptions}
			onChange={onChange}
			disabled={disabled}
			menuAlign="start"
			menuClassName="w-48 max-h-72"
			triggerVariant="ghost"
			triggerClassName={cn(TRIGGER_CLASS, "w-fit shrink-0")}
			showCaret={false}
			menuItemClassName="gap-2! [&_svg]:text-current!"
			action={hostId ? undefined : {
				label: t("newTask.createNew"),
				icon: <Plus aria-hidden="true" className="size-icon-sm shrink-0" />,
				onSelect: requestCreateProject,
			}}
			renderTrigger={() => (
				<span className="inline-flex min-w-0 items-center gap-1.5">
					<ProjectIcon projectId={projectId} cloud={cloudProjectIds.has(projectId)} className="size-3.5 text-muted-foreground" />
					<span className="min-w-0 truncate">{selectedProjectName}</span>
					{hostLabel ? <span className="shrink-0 text-muted-foreground">· {hostLabel}</span> : null}
				</span>
			)}
			renderMenuItem={(option) => (
				<>
					<ProjectIcon projectId={option.value} cloud={cloudProjectIds.has(option.value)} className="size-icon-sm" />
					<span className="min-w-0 truncate">{option.label}</span>
				</>
			)}
		/>
	);
}

function ProjectIcon({ projectId, cloud, className }: { projectId: string; cloud?: boolean; className: string }) {
	const Icon = projectId === STANDALONE_WORKSPACE_ID ? StickyNote : cloud ? Cloud : Folder;
	return <Icon aria-hidden="true" className={cn("shrink-0", className)} />;
}

function MobileConnected() {
	const { t } = useTranslation();
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	const { data } = useQuery({ queryKey: mobileDevicesQueryKey, queryFn: fetchDevices, refetchInterval: 10_000, retry: false });
	const live = data?.filter((device) => device.live && isPhone(device)) ?? [];
	if (live.length === 0) return null;
	return (
		<Button
			type="button"
			variant="quiet"
			size="sm"
			className="ml-auto w-fit"
			title={live.map((device) => device.deviceName).filter(Boolean).join(", ") || undefined}
			onClick={() => openGlobalSettings("mobile")}
		>
			<Smartphone aria-hidden="true" className="size-3.5 shrink-0" />
			{t("newTask.phoneConnected", { count: live.length })}
		</Button>
	);
}
