import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useRouter } from "@tanstack/react-router";
import { Folder, Plus, Smartphone, StickyNote } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useRemoteWorkspaces, useWorkspaceQuery, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { seedOpeningLocalEcho, useStageAttachments, type ConversationLocalEcho } from "../hooks/useConversation";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { readChatSessionDraft, writeChatAttachments, writeChatComposerText } from "../lib/chat-drafts";
import { clientForHost, labelForHost } from "../lib/host-clients";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { openComposerModelPicker } from "../lib/composer-shortcuts";
import { useSessionShortcut } from "../hooks/useAppShortcut";
import { CLOUD_PROJECT_KIND, STANDALONE_PROJECT_KIND, STANDALONE_WORKSPACE_ID } from "../types/workspace";
import type { ChatConfigOption, ChatConfigOptionValue, ChatModel, ConversationSnapshot, TurnSettings } from "../types/conversation";
import { readChatSettingsTemplate, readChatSkills } from "../lib/chat-settings-template";
import { agentModelDisplayLabel } from "../lib/agent-model-choices";
import { ChatWorkspace } from "./chat/ChatWorkspace";
import { startingConversationSnapshot } from "./chat/OrchestratorStartingChat";
import { LaunchSettingsBar, rememberLaunchSettings, rememberOpeningStart, type LaunchSettings } from "./chat/LaunchSettingsBar";
import { hasProviderPermissionMode, partitionConfigOptions, TRIGGER_CLASS, TurnSettingsBar } from "./chat/TurnSettingsBar";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";
import { fetchDevices, mobileDevicesQueryKey } from "./settings/MobileDevicesSection";
import { useUiStore } from "../stores/ui-store";
import { topbarDragStyle } from "./ShellTopbar";
import { useTaskLaunch, type LaunchReplay } from "./TaskComposer";
import { Button } from "./ui/button";
import { cn } from "../lib/utils";

/**
 * New task as a chat that has no session yet. The first send creates the
 * session; the page keeps the brief on screen and hands it to the session's own
 * chat, so starting a task reads as one continuous conversation.
 */
export function NewChatPage({ projectId, hostId }: { projectId: string; hostId?: string }) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const router = useRouter();
	const queryClient = useQueryClient();
	const task = useTaskLaunch({ projectId, hostId });
	// Sends read the latest render's launch, not the one they started in.
	const taskRef = useRef(task);
	taskRef.current = task;
	// The page this render shows. A send is bound to the page it started on.
	const targetRef = useRef({ projectId, hostId });
	targetRef.current = { projectId, hostId };
	// The session's own composer shortcuts, before the session exists.
	useSessionShortcut("model-picker", openComposerModelPicker);
	const mounted = useRef(true);
	useEffect(() => {
		mounted.current = true;
		return () => {
			mounted.current = false;
		};
	}, []);
	// One draft per host, so switching project keeps what was typed.
	const hostKey = hostId ?? "local";
	const draftId = `new-chat:${hostKey}`;
	const draftScope = useMemo(() => ({ sessionId: draftId, incarnation: draftId }), [draftId]);
	const [echo, setEcho] = useState<ConversationLocalEcho>();
	// The settings bar the brief was sent with, held still until the session takes over.
	const [launched, setLaunched] = useState<LaunchSettings>();
	// A failed fallback start has no composer send to report it, so the page does.
	const [fallbackFailed, setFallbackFailed] = useState(false);
	// A load error a send already reported in the composer.
	const [reportedLoadError, setReportedLoadError] = useState<string>();
	// Why the daemon refused a retry of a send whose first answer was lost.
	const [refusedRetry, setRefusedRetry] = useState<string>();
	// A fallback start keeps its request id until it is answered, so a retry after a
	// lost response cannot create a second session.
	const fallbackRequest = useRef<string | undefined>(undefined);
	const fallbackInFlight = useRef(false);
	// Bumped when a saved launch is resolved, so the page re-reads it.
	const [, setLaunchRevision] = useState(0);
	// "@" files and attachments go through the prepared worktree's session, which
	// becomes the task's session on send. Scratchpad has none, so it offers neither.
	const preparedId = task.preparedSessionId || undefined;
	const workspaceFiles = useQuery({
		queryKey: ["new-chat-files", hostKey, preparedId ?? ""],
		enabled: Boolean(preparedId),
		retry: false,
		staleTime: 30_000,
		// The worktree is still being created when the id arrives; ask again until it
		// exists, backing off so a stalled preparation is not polled every 1.5s.
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
	const workspaceReady = Boolean(preparedId && workspaceFiles.data);
	// Files staged into one project's worktree do not exist in another's.
	const attachmentsProject = useRef(projectId);
	if (attachmentsProject.current !== projectId) {
		attachmentsProject.current = projectId;
		writeChatAttachments(draftScope, []);
	}
	const skills = useMemo(() => readChatSkills(task.agent.value, hostId), [hostId, task.agent.value]);
	const snapshot = useMemo<ConversationSnapshot>(
		() => ({ ...startingConversationSnapshot(draftId, task.agent.value || "claude-code"), controller: { state: "ready" } }),
		[draftId, task.agent.value],
	);
	// A session of this agent already reported its own settings: draw exactly those
	// controls, holding this chat's choices, so the bar never changes on handoff.
	const harness = task.agent.value;
	const template = useMemo(() => readChatSettingsTemplate(harness, hostId), [harness, hostId]);
	const barFor = (settings: TurnSettings): LaunchSettings => {
		const providerOptions = template ? draftProviderOptions(template.options, harness, task.chatModels, settings, task.isCloudProject) : undefined;
		return {
			harness,
			projectId: task.catalogProjectId,
			hostId,
			models: providerOptions ? [] : task.chatModels,
			settings,
			configOptions: providerOptions,
			showApprovalMode: !task.isCloudProject,
		};
	};
	const barSettings = barFor(task.turnSettings);
	const providerOptions = barSettings.configOptions;
	// Sends capture the bar as it is once loading finishes, not as it was at Enter.
	const barRef = useRef(barSettings);
	barRef.current = barSettings;
	const chooseProviderOption = (optionId: string, value: ChatConfigOptionValue) => {
		const option = template?.options.find((candidate) => candidate.id === optionId);
		if (!option || !("value" in value)) return;
		// Partitioning returns copies of the options, so match them by id.
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

	// A send whose response was lost is retried with the same request id. It is saved
	// with its project and request, so the retry repeats exactly that request, even
	// after a reload; the page returns to that project until it is resolved.
	const pendingLaunch = readPendingLaunch(hostKey, draftScope);
	useEffect(() => {
		if (!pendingLaunch || pendingLaunch.projectId === projectId) return;
		void navigate({
			to: "/new",
			search: { project: pendingLaunch.projectId === STANDALONE_WORKSPACE_ID ? undefined : pendingLaunch.projectId, host: hostId },
			replace: true,
		});
	}, [hostId, navigate, pendingLaunch, projectId]);
	const resolveLaunch = (clientMessageId: string) => {
		clearPendingLaunch(hostKey, clientMessageId);
		setLaunchRevision((revision) => revision + 1);
	};

	// Escape leaves, as closing the old dialog did; the draft is kept. Not while
	// Settings (which closes on the same key) or an input method owns it.
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

	// Cloud projects keep the New Task dialog for now: a link here hands over to it.
	useEffect(() => {
		if (!task.isCloudProject) return;
		void navigate({ to: "/new", search: { host: hostId }, replace: true });
		useUiStore.getState().requestNewTask(projectId, hostId);
	}, [hostId, navigate, projectId, task.isCloudProject]);

	// Load the session page's code now: a first visit otherwise renders the
	// router's empty pending frame between this page and the session.
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
		// The session's chat shows the brief from its first frame; cloud and
		// terminal sessions open on their own surfaces and need none of it.
		if (!taskRef.current.isCloudProject && interfaceMode === "chat") {
			seedOpeningLocalEcho(queryClient, sessionId, target.hostId, brief);
			rememberLaunchSettings(queryClient, sessionId, target.hostId, settings);
			rememberOpeningStart(queryClient, sessionId, target.hostId, brief.createdAt);
		}
		await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(target.hostId) });
		// After the composer settles this send, so its draft never outlives the page.
		// Someone who has since left, or opened another new task, stays where they are.
		setTimeout(() => {
			if (!mounted.current) return;
			const here = targetRef.current;
			if (here.projectId === target.projectId && here.hostId === target.hostId) {
				void navigate({ ...sessionNavigateTarget(target.projectId, sessionId, target.hostId), replace: true });
				return;
			}
			// The page now shows another project: the task is in the sidebar, and this
			// page is a fresh new chat again.
			setEcho(undefined);
			setLaunched(undefined);
		}, 0);
	};

	const send = async (text: string, _attachments?: unknown, clientMessageId: string = crypto.randomUUID()) => {
		const target = { ...targetRef.current };
		const saved = readPendingLaunch(hostKey, draftScope, clientMessageId);
		const brief = { clientMessageId, text, createdAt: new Date().toISOString() };
		setEcho(brief);
		setRefusedRetry(undefined);
		// Held still from Enter; replaced by the loaded bar once the page is ready.
		setLaunched(saved?.settings ?? barRef.current);
		try {
			// A saved send belongs to its own project; the page is on its way back there.
			if (saved && saved.projectId !== target.projectId) throw new Error(t("newTask.unableToStart"));
			// A send right after opening waits for the project and agents to load,
			// unless loading has already failed or the page has gone.
			if (!(await until(() => !mounted.current || taskRef.current.canSubmit || Boolean(taskRef.current.remoteLoadError), 10_000)) || !mounted.current || !taskRef.current.canSubmit) {
				// The composer shows this reason with the returned brief, so the notice below steps aside.
				setReportedLoadError(taskRef.current.remoteLoadError);
				throw Object.assign(new Error(taskRef.current.remoteLoadError ?? t("newTask.unableToStart")), { code: "TASK_NOT_READY" });
			}
			// Nothing was created yet, so a page that moved to another project or host
			// returns the brief rather than starting it there.
			const here = targetRef.current;
			if (here.projectId !== target.projectId || here.hostId !== target.hostId) {
				throw Object.assign(new Error(t("newTask.unableToStart")), { code: "TASK_RETARGETED" });
			}
			const launch = taskRef.current;
			const settings = saved?.settings ?? barRef.current;
			const replay = saved?.replay ?? launch.launchSnapshot();
			setLaunched(settings);
			writePendingLaunch(hostKey, { clientMessageId, projectId: target.projectId, replay, settings });
			const sessionId = await launch.launch(text, { clientRequestId: clientMessageId, replay });
			resolveLaunch(clientMessageId);
			await openSession(sessionId, brief, settings, replay.mode ?? launch.effectiveInterface, target);
		} catch (error) {
			setEcho(undefined);
			// Only a refusal from the daemon is final: it returns the brief with the
			// reason. A lost response, or a failure after the session may have started,
			// stays uncertain, so the composer retries with the same request id. A refused
			// retry says nothing about the attempt whose answer was lost: the composer
			// still holds it, so it stays pinned to its project and settings.
			if (saved && !sendIsUncertain(error)) setRefusedRetry(apiErrorMessage(error, t("newTask.unableToStart")));
			if (sendIsUncertain(error) || saved) throw error;
			resolveLaunch(clientMessageId);
			throw Object.assign(new Error(apiErrorMessage(error, t("newTask.unableToStart"))), { code: "TASK_CREATE_FAILED" });
		}
	};

	// The fallback starts what the composer holds now; a brief the user cleared stays cleared.
	const sendFallback = async () => {
		const text = readChatSessionDraft(draftScope).composer.text.trim();
		// A second click before the page re-renders must not start it twice.
		if (!text || fallbackInFlight.current) return;
		fallbackInFlight.current = true;
		const target = { ...targetRef.current };
		fallbackRequest.current ??= crypto.randomUUID();
		const clientMessageId = fallbackRequest.current;
		const brief = { clientMessageId, text, createdAt: new Date().toISOString() };
		const fallback = task.fallbackAction;
		// The bar shows what the fallback actually sends.
		const settings = fallback === "bypass-permissions"
			? barFor({ ...task.turnSettings, approvalMode: "bypass-permissions" })
			: barSettings;
		setFallbackFailed(false);
		setEcho(brief);
		setLaunched(settings);
		try {
			const sessionId = await task.launchFallback(brief.text, clientMessageId);
			fallbackRequest.current = undefined;
			writeChatComposerText(draftScope, "");
			await openSession(sessionId, brief, settings, fallback === "tui" ? "tui" : task.effectiveInterface, target);
		} catch (error) {
			if (!sendIsUncertain(error)) fallbackRequest.current = undefined;
			// The hook keeps the reason; the notice below shows it.
			setFallbackFailed(true);
			setEcho(undefined);
		} finally {
			fallbackInFlight.current = false;
		}
	};

	const pending = Boolean(echo);
	const frozen = pending || Boolean(pendingLaunch);
	const frozenSettings = launched ?? pendingLaunch?.settings;
	pendingRef.current = pending;
	const notice = pending ? null : task.fallbackAction ? (
		<div className="flex items-center justify-end">
			<Button size="sm" variant="secondary" onClick={() => void sendFallback()}>{task.fallbackLabel}</Button>
		</div>
	) : fallbackFailed && task.error ? (
		<p role="alert" className="px-1 text-xs text-destructive">{task.error}</p>
	) : pendingLaunch && refusedRetry ? (
		<p role="alert" className="px-1 text-xs text-destructive">{refusedRetry}</p>
	) : task.remoteLoadError && task.remoteLoadError !== reportedLoadError ? (
		<p role="alert" className="px-1 text-xs text-destructive">{task.remoteLoadError}</p>
	) : typeof task.modelWarning === "string" ? (
		<div className="flex items-center gap-2 px-1">
			<p role="status" className="min-w-0 text-xs text-warning">{task.modelWarning}</p>
			<Button size="sm" variant="ghost" className="h-6 shrink-0 px-2 text-xs" onClick={() => void task.refreshModels().catch(() => undefined)}>
				{t("newTask.modelNotice.retry")}
			</Button>
		</div>
	) : task.modelWarning;

	return (
		<div className="flex h-full min-h-0 flex-col">
			{/* No topbar yet: the strip only holds the session header's place, so the
			    chat does not move when the session takes over, and drags the window. */}
			<div aria-hidden="true" className="h-inspector-tabs w-full shrink-0" style={topbarDragStyle} />
			<ChatWorkspace
				// A different project is a different worktree: the composer reloads its
				// draft (text kept, staged files dropped above).
				key={projectId}
				snapshot={snapshot}
				uiSessionId={draftId}
				// The session is titled after its brief, so the tab already reads that way.
				sessionTitle={echo ? echo.text.trim().slice(0, 100) : t("command.newTask")}
				sessionRole="worker"
				hideHeader
				centerInWindow
				localEchos={echo ? [echo] : undefined}
				openingStartedAt={echo?.createdAt}
				attachLater={!workspaceReady}
				onStageAttachments={workspaceReady ? stagePrepared : undefined}
				filePaths={workspaceFiles.data?.paths}
				filePathsTruncated={workspaceFiles.data?.truncated}
				skills={skills}
				composerTray={
					<>
						<NewChatProjectPicker projectId={projectId} hostId={hostId} disabled={frozen} onChange={(project) => void navigate({ to: "/new", search: { project, host: hostId }, replace: true })} />
						{hostId ? null : <MobileConnected />}
					</>
				}
				onSend={send}
				busy={pending}
				// The session's own composer controls from the start: the same bar, held
				// still once sent, with the agent at the top of the model menu.
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
							// The session's own picker, so the bar is the one the chat keeps. With no
							// session to switch yet, picking another agent changes it at once.
							agentSwitch={{
								projectId: task.catalogProjectId,
								hostId,
								agents: task.agentChoices,
								disabled: task.agent.disabled,
								onPendingChange: (next) => {
									if (next) task.switchAgent(next.harness, { model: next.model, effort: next.effort ?? next.shownEffort });
								},
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

/**
 * The agent's model, effort, and permission options, set to this chat's choices.
 * Undefined when they cannot show exactly what the task will start with; the page
 * then draws AO's own controls instead.
 */
function draftProviderOptions(options: ChatConfigOption[], harness: string, models: ChatModel[], settings: TurnSettings, cloud: boolean): ChatConfigOption[] | undefined {
	const grouped = partitionConfigOptions(options);
	const out: ChatConfigOption[] = [];
	const model = grouped.model[0];
	if (!model) return undefined;
	// A saved template predates the live catalog: a model it does not list means it
	// can no longer show every choice, so AO's own controls take over.
	const listed = (item: ChatModel) => model.choices.some((choice) =>
		choice.value === item.id || choice.name === agentModelDisplayLabel(harness, item.displayName));
	if (models.some((item) => item.id !== "default" && !listed(item))) return undefined;
	// Only choices a new task can start with: those AO's model list also has.
	const modelChoices = model.choices.filter((choice) => catalogModelFor(choice, harness, models));
	const chosen = models.find((item) => item.id === settings.model);
	const label = chosen ? agentModelDisplayLabel(harness, chosen.displayName) : undefined;
	const modelValue = modelChoices.find((choice) => choice.value === settings.model)?.value ??
		modelChoices.find((choice) => choice.name === label)?.value;
	if (!modelValue) return undefined;
	out.push({ ...model, choices: modelChoices, currentValue: modelValue });
	const effort = grouped.effort[0];
	if (effort) {
		// Efforts belong to a model; the template's list must match the chosen one's.
		const offered = new Set(chosen?.efforts ?? []);
		const listedEfforts = effort.choices.map((choice) => choice.value).filter((value) => value !== "default");
		if (listedEfforts.length !== offered.size || listedEfforts.some((value) => !offered.has(value))) return undefined;
		out.push({ ...effort, currentValue: settings.reasoningEffort || "default" });
	}
	// Cloud tasks do not send a permission choice, so none is offered.
	if (grouped.mode && !cloud) {
		// Only choices a new task can start with: those with an AO permission equivalent.
		const choices = grouped.mode.choices.filter((choice) => choice.permissionMode);
		const value = choices.find((choice) => choice.permissionMode === settings.approvalMode)?.value;
		if (!value) return undefined;
		out.push({ ...grouped.mode, choices, currentValue: value });
	}
	return out;
}

// Errors raised after the daemon may have created the session: a retry with the
// same request id must be possible, so these never count as a final refusal.
const POST_START_ERRORS = new Set([
	"SPAWN_COMMIT_FAILED", "SPAWN_INTERNAL", "SPAWN_PROMPT_DELIVERY_FAILED", "SPAWN_LAUNCH_COMMAND_FAILED",
	"SPAWN_SUPERVISOR_FAILED", "SPAWN_PREPARE_LAUNCH_FAILED", "RUNTIME_CREATE_FAILED",
]);

/** A send whose outcome is unknown: no answer, or a failure after the session may exist. */
function sendIsUncertain(error: unknown): boolean {
	if (!(error instanceof Error)) return true;
	const code = "code" in error && typeof error.code === "string" ? error.code : undefined;
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

/**
 * This host's unresolved send, while the composer still holds its delivery; a send
 * the composer no longer holds is dropped. With an id, only that send.
 */
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
		// Without it a retry after a reload rebuilds the request from the current picks.
	}
}

/** Drops this host's saved send; with an id, only when it is still that send. */
function clearPendingLaunch(hostKey: string, clientMessageId?: string): void {
	const all = readPendingLaunches();
	if (!(hostKey in all)) return;
	if (clientMessageId !== undefined && all[hostKey]?.clientMessageId !== clientMessageId) return;
	delete all[hostKey];
	try {
		window.localStorage.setItem(PENDING_LAUNCH_KEY, JSON.stringify(all));
	} catch {
		// Nothing to undo: an unreadable entry is dropped on the next read.
	}
}

/** Resolves once `ready` holds, or false when it has not within `ms`. */
async function until(ready: () => boolean, ms: number): Promise<boolean> {
	const deadline = Date.now() + ms;
	while (!ready() && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 100));
	return ready();
}

/** The catalog model a provider choice names, by id or by the name both show. */
function catalogModelFor(choice: { value: string; name: string }, harness: string, models: ChatModel[]): ChatModel | undefined {
	return models.find((item) => item.id === choice.value) ??
		models.find((item) => agentModelDisplayLabel(harness, item.displayName) === choice.name);
}

function NewChatProjectPicker({ projectId, hostId, disabled, onChange }: { projectId: string; hostId?: string; disabled?: boolean; onChange: (projectId: string) => void }) {
	const { t } = useTranslation();
	const localWorkspaces = useWorkspaceQuery().data ?? [];
	const remoteWorkspaces = useRemoteWorkspaces().data ?? [];
	const workspaces = hostId ? remoteWorkspaces.filter((workspace) => workspace.hostId === hostId) : localWorkspaces;
	// Cloud projects start tasks from the New Task dialog for now.
	const projects = workspaces.filter(
		(workspace) => workspace.id !== STANDALONE_WORKSPACE_ID && workspace.kind !== STANDALONE_PROJECT_KIND && workspace.kind !== CLOUD_PROJECT_KIND,
	);
	const projectOptions = [
		{ value: STANDALONE_WORKSPACE_ID, label: t("standalone.workspaceName") },
		...projects.map((project) => ({ value: project.id, label: project.name })),
	];
	// A project that is not (or not yet) listed, such as one still loading or deleted,
	// reads as unselected rather than as the scratchpad.
	const selectedProjectName = projectId === STANDALONE_WORKSPACE_ID
		? t("standalone.workspaceName")
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
			triggerClassName={cn(TRIGGER_CLASS, "w-fit shrink-0")}
			showCaret={false}
			// Icon and label share the row's colour, so hover changes both together.
			menuItemClassName="gap-2! [&_svg]:text-current!"
			// Adding a project opens the local Add Project flow, so remote hosts skip it.
			action={hostId ? undefined : {
				label: t("newTask.createNew"),
				icon: <Plus aria-hidden="true" className="size-icon-sm shrink-0" />,
				onSelect: requestCreateProject,
			}}
			renderTrigger={() => (
				<span className="inline-flex min-w-0 items-center gap-1.5">
					<ProjectIcon projectId={projectId} className="size-3.5 text-muted-foreground" />
					<span className="min-w-0 truncate">{selectedProjectName}</span>
					{hostLabel ? <span className="shrink-0 text-muted-foreground">· {hostLabel}</span> : null}
				</span>
			)}
			renderMenuItem={(option) => (
				<>
					<ProjectIcon projectId={option.value} className="size-icon-sm" />
					<span className="min-w-0 truncate">{option.label}</span>
				</>
			)}
		/>
	);
}

function ProjectIcon({ projectId, className }: { projectId: string; className: string }) {
	const Icon = projectId === STANDALONE_WORKSPACE_ID ? StickyNote : Folder;
	return <Icon aria-hidden="true" className={cn("shrink-0", className)} />;
}

/** Shown while a paired phone has the app open, so a task started here can be followed there. */
function MobileConnected() {
	const { t } = useTranslation();
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	const { data } = useQuery({ queryKey: mobileDevicesQueryKey, queryFn: fetchDevices, refetchInterval: 10_000, retry: false });
	const live = data?.filter((device) => device.live) ?? [];
	if (live.length === 0) return null;
	return (
		<button
			type="button"
			className={cn(TRIGGER_CLASS, "ml-auto inline-flex w-fit shrink-0 items-center gap-1.5")}
			title={live.map((device) => device.deviceName).filter(Boolean).join(", ") || undefined}
			onClick={() => openGlobalSettings("mobile")}
		>
			<Smartphone aria-hidden="true" className="size-3.5 shrink-0" />
			{t("newTask.phoneConnected", { count: live.length })}
		</button>
	);
}
