import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useRouter } from "@tanstack/react-router";
import { Folder, Smartphone, StickyNote } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useRemoteWorkspaces, useWorkspaceQuery, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { seedOpeningLocalEcho, type ConversationLocalEcho } from "../hooks/useConversation";
import { apiErrorMessage } from "../lib/api-client";
import { readChatSessionDraft, writeChatComposerText } from "../lib/chat-drafts";
import { labelForHost } from "../lib/host-clients";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { STANDALONE_PROJECT_KIND, STANDALONE_WORKSPACE_ID } from "../types/workspace";
import type { ChatConfigOption, ChatConfigOptionValue, ChatModel, ConversationSnapshot, TurnSettings } from "../types/conversation";
import { readChatSettingsTemplate } from "../lib/chat-settings-template";
import { agentModelDisplayLabel } from "../lib/agent-model-choices";
import { ChatWorkspace } from "./chat/ChatWorkspace";
import { startingConversationSnapshot } from "./chat/OrchestratorStartingChat";
import { LaunchSettingsBar, rememberLaunchSettings, rememberOpeningStart, type LaunchSettings } from "./chat/LaunchSettingsBar";
import { hasProviderPermissionMode, partitionConfigOptions, TRIGGER_CLASS, TurnSettingsBar } from "./chat/TurnSettingsBar";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";
import { fetchDevices, mobileDevicesQueryKey } from "./settings/MobileDevicesSection";
import { useUiStore } from "../stores/ui-store";
import { topbarDragStyle } from "./ShellTopbar";
import { useTaskLaunch } from "./TaskComposer";
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
	const mounted = useRef(true);
	useEffect(() => {
		mounted.current = true;
		return () => {
			mounted.current = false;
		};
	}, []);
	// One draft per host, so switching project keeps what was typed.
	const draftId = `new-chat:${hostId ?? "local"}`;
	const draftScope = { sessionId: draftId, incarnation: draftId };
	const [echo, setEcho] = useState<ConversationLocalEcho>();
	// The settings bar the brief was sent with, held still until the session takes over.
	const [launched, setLaunched] = useState<LaunchSettings>();
	const lastBrief = useRef("");
	// A failed fallback start has no composer send to report it, so the page does.
	const [fallbackFailed, setFallbackFailed] = useState(false);
	const snapshot = useMemo<ConversationSnapshot>(
		() => ({ ...startingConversationSnapshot(draftId, task.agent.value || "claude-code"), controller: { state: "ready" } }),
		[draftId, task.agent.value],
	);
	// A session of this agent already reported its own settings: draw exactly those
	// controls, holding this chat's choices, so the bar never changes on handoff.
	const harness = task.agent.value;
	const template = useMemo(() => readChatSettingsTemplate(harness, hostId), [harness, hostId]);
	const providerOptions = template ? draftProviderOptions(template.options, harness, task.chatModels, task.turnSettings) : undefined;
	const barSettings: LaunchSettings = {
		harness,
		models: providerOptions ? [] : task.chatModels,
		settings: task.turnSettings,
		configOptions: providerOptions,
		showApprovalMode: !task.isCloudProject,
	};
	const chooseProviderOption = (optionId: string, value: ChatConfigOptionValue) => {
		const option = template?.options.find((candidate) => candidate.id === optionId);
		if (!option || !("value" in value)) return;
		const grouped = partitionConfigOptions(template?.options ?? []);
		const choice = option.choices.find((candidate) => candidate.value === value.value);
		if (grouped.model.includes(option)) {
			const model = choice ? catalogModelFor(choice, harness, task.chatModels) : undefined;
			if (model) task.chooseTurnSettings({ ...task.turnSettings, model: model.id });
		} else if (grouped.effort.includes(option)) {
			task.chooseTurnSettings({ ...task.turnSettings, reasoningEffort: value.value === "default" ? undefined : value.value });
		} else if (grouped.mode === option && choice?.permissionMode) {
			task.chooseTurnSettings({ ...task.turnSettings, approvalMode: choice.permissionMode });
		}
	};

	const localWorkspaces = useWorkspaceQuery();
	const remoteWorkspaces = useRemoteWorkspaces();
	const workspacesLoaded = hostId ? remoteWorkspaces.data !== undefined : localWorkspaces.data !== undefined;
	const projectMissing = workspacesLoaded && projectId !== STANDALONE_WORKSPACE_ID &&
		!(hostId ? remoteWorkspaces.data ?? [] : localWorkspaces.data ?? []).some((workspace) => workspace.id === projectId);
	useEffect(() => {
		if (projectMissing) void navigate({ to: "/new", search: { host: hostId }, replace: true });
	}, [hostId, navigate, projectMissing]);

	// Escape leaves, as closing the old dialog did; the draft is kept.
	const pendingRef = useRef(false);
	useEffect(() => {
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key !== "Escape" || event.defaultPrevented || pendingRef.current) return;
			if (document.querySelector('[role="menu"], [role="dialog"], [role="alertdialog"], [role="listbox"]')) return;
			if (router.history.canGoBack()) router.history.back();
		};
		window.addEventListener("keydown", onKeyDown);
		return () => window.removeEventListener("keydown", onKeyDown);
	}, [router]);

	// Load the session page's code now: a first visit otherwise renders the
	// router's empty pending frame between this page and the session.
	useEffect(() => {
		void router.preloadRoute(sessionNavigateTarget(projectId, "preload", hostId)).catch(() => undefined);
	}, [hostId, projectId, router]);

	const openSession = async (sessionId: string, brief: ConversationLocalEcho, settings: LaunchSettings, interfaceMode?: "chat" | "tui") => {
		// The session's chat shows the brief from its first frame; cloud and
		// terminal sessions open on their own surfaces instead.
		if (!task.isCloudProject && interfaceMode !== "tui") {
			seedOpeningLocalEcho(queryClient, sessionId, hostId, brief);
			rememberLaunchSettings(queryClient, sessionId, hostId, settings);
			rememberOpeningStart(queryClient, sessionId, hostId, brief.createdAt);
		}
		await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
		// After the composer settles this send, so its draft never outlives the page.
		// Someone who already left is not pulled back to the session.
		setTimeout(() => {
			if (mounted.current) void navigate({ ...sessionNavigateTarget(projectId, sessionId, hostId), replace: true });
		}, 0);
	};

	const send = async (text: string, _attachments?: unknown, clientMessageId: string = crypto.randomUUID()) => {
		const brief = { clientMessageId, text, createdAt: new Date().toISOString() };
		const settings = barSettings;
		lastBrief.current = text;
		setEcho(brief);
		setLaunched(settings);
		try {
			// A send right after opening waits for the project and agents to load.
			if (!(await until(() => taskRef.current.canSubmit, 10_000))) {
				throw Object.assign(new Error(taskRef.current.remoteLoadError ?? t("newTask.unableToStart")), { code: "TASK_NOT_READY" });
			}
			const launch = taskRef.current;
			const sessionId = await launch.launch(text, { interfaceMode: launch.submitMode, clientRequestId: clientMessageId });
			await openSession(sessionId, brief, settings, launch.submitMode);
		} catch (error) {
			setEcho(undefined);
			// Only an answer from the daemon is final: it returns the brief with the
			// reason. A lost response stays uncertain, so the composer retries with the
			// same request id instead of creating a second task.
			if (!(error instanceof Error && ("code" in error || error.name === "TaskCreateError"))) throw error;
			throw Object.assign(new Error(apiErrorMessage(error, t("newTask.unableToStart"))), { code: "TASK_CREATE_FAILED" });
		}
	};

	// The fallback starts what the composer holds now, edits included.
	const sendFallback = async () => {
		const text = readChatSessionDraft(draftScope).composer.text.trim() || lastBrief.current;
		const brief = { clientMessageId: crypto.randomUUID(), text, createdAt: new Date().toISOString() };
		const settings = barSettings;
		const tui = task.fallbackAction === "tui";
		setFallbackFailed(false);
		setEcho(brief);
		setLaunched(settings);
		try {
			const sessionId = await task.launchFallback(brief.text, brief.clientMessageId);
			writeChatComposerText(draftScope, "");
			await openSession(sessionId, brief, settings, tui ? "tui" : undefined);
		} catch {
			// The hook keeps the reason; the notice below shows it.
			setFallbackFailed(true);
			setEcho(undefined);
		}
	};

	const pending = Boolean(echo);
	pendingRef.current = pending;
	const notice = pending ? null : task.fallbackAction ? (
		<div className="flex items-center justify-end">
			<Button size="sm" variant="secondary" onClick={() => void sendFallback()}>{task.fallbackLabel}</Button>
		</div>
	) : fallbackFailed && task.error ? (
		<p role="alert" className="px-1 text-xs text-destructive">{task.error}</p>
	) : task.remoteLoadError ? (
		<p role="alert" className="px-1 text-xs text-destructive">{task.remoteLoadError}</p>
	) : typeof task.modelWarning === "string" ? (
		<p role="status" className="px-1 text-xs text-warning">{task.modelWarning}</p>
	) : task.modelWarning;

	return (
		<div className="flex h-full min-h-0 flex-col">
			{/* No topbar yet: the strip only holds the session header's place, so the
			    chat does not move when the session takes over, and drags the window. */}
			<div aria-hidden="true" className="h-inspector-tabs w-full shrink-0" style={topbarDragStyle} />
			<ChatWorkspace
				snapshot={snapshot}
				uiSessionId={draftId}
				// The session is titled after its brief, so the tab already reads that way.
				sessionTitle={echo ? echo.text.trim().slice(0, 100) : t("command.newTask")}
				sessionRole="worker"
				hideHeader
				centerInWindow
				localEchos={echo ? [echo] : undefined}
				openingStartedAt={echo?.createdAt}
				attachLater
				composerTray={
					<>
						<NewChatProjectPicker projectId={projectId} hostId={hostId} onChange={(project) => void navigate({ to: "/new", search: { project, host: hostId }, replace: true })} />
						{hostId ? null : <MobileConnected />}
					</>
				}
				onSend={send}
				busy={pending}
				// The session's own composer controls from the start: the same bar, held
				// still once sent, with the agent at the top of the model menu.
				draftSettings={pending ? (launched ? <LaunchSettingsBar {...launched} /> : null) : (
					<>
						<TurnSettingsBar
							models={barSettings.models}
							settings={barSettings.settings}
							harness={harness}
							onChange={providerOptions && hasProviderPermissionMode(providerOptions) ? undefined : task.chooseTurnSettings}
							configOptions={providerOptions}
							onChangeConfigOption={providerOptions ? chooseProviderOption : undefined}
							showApprovalMode={barSettings.showApprovalMode}
							agentMenu={task.agentMenu}
							agentIcon
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
function draftProviderOptions(options: ChatConfigOption[], harness: string, models: ChatModel[], settings: TurnSettings): ChatConfigOption[] | undefined {
	const grouped = partitionConfigOptions(options);
	const out: ChatConfigOption[] = [];
	const model = grouped.model[0];
	if (model) {
		// Only choices a new task can start with: those AO's model list also has.
		const choices = model.choices.filter((choice) => catalogModelFor(choice, harness, models));
		const chosen = models.find((item) => item.id === settings.model);
		const label = chosen ? agentModelDisplayLabel(harness, chosen.displayName) : undefined;
		const value = choices.find((choice) => choice.value === settings.model)?.value ??
			choices.find((choice) => choice.name === label)?.value;
		if (!value) return undefined;
		out.push({ ...model, choices, currentValue: value });
	}
	const effort = grouped.effort[0];
	if (effort) out.push({ ...effort, currentValue: settings.reasoningEffort || "default" });
	if (grouped.mode) {
		// Only choices a new task can start with: those with an AO permission equivalent.
		const choices = grouped.mode.choices.filter((choice) => choice.permissionMode);
		const value = choices.find((choice) => choice.permissionMode === settings.approvalMode)?.value;
		if (!value) return undefined;
		out.push({ ...grouped.mode, choices, currentValue: value });
	}
	return out;
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

function NewChatProjectPicker({ projectId, hostId, onChange }: { projectId: string; hostId?: string; onChange: (projectId: string) => void }) {
	const { t } = useTranslation();
	const localWorkspaces = useWorkspaceQuery().data ?? [];
	const remoteWorkspaces = useRemoteWorkspaces().data ?? [];
	const workspaces = hostId ? remoteWorkspaces.filter((workspace) => workspace.hostId === hostId) : localWorkspaces;
	const projects = workspaces.filter(
		(workspace) => workspace.id !== STANDALONE_WORKSPACE_ID && workspace.kind !== STANDALONE_PROJECT_KIND,
	);
	const projectOptions = [
		{ value: STANDALONE_WORKSPACE_ID, label: t("standalone.workspaceName") },
		...projects.map((project) => ({ value: project.id, label: project.name })),
	];
	const selectedProjectName = projects.find((project) => project.id === projectId)?.name ?? t("standalone.workspaceName");
	const hostLabel = hostId ? labelForHost(hostId) ?? hostId : undefined;
	return (
		<SettingsOptionMenu
			aria-label={`${t("newTask.project")}: ${selectedProjectName}`}
			value={projectId}
			options={projectOptions}
			onChange={onChange}
			menuAlign="start"
			menuClassName="w-48 max-h-72"
			triggerClassName={cn(TRIGGER_CLASS, "w-fit shrink-0")}
			showCaret={false}
			menuItemClassName="gap-2!"
			renderTrigger={() => (
				<span className="inline-flex min-w-0 items-center gap-1.5">
					<ProjectIcon projectId={projectId} className="size-3.5" />
					<span className="min-w-0 truncate">{selectedProjectName}</span>
					{hostLabel ? <span className="shrink-0 text-muted-foreground">· {hostLabel}</span> : null}
				</span>
			)}
			renderMenuItem={(option) => (
				<>
					<ProjectIcon projectId={option.value} className="size-icon-sm" />
					<span className="min-w-0 truncate text-settings-label">{option.label}</span>
				</>
			)}
		/>
	);
}

function ProjectIcon({ projectId, className }: { projectId: string; className: string }) {
	const Icon = projectId === STANDALONE_WORKSPACE_ID ? StickyNote : Folder;
	return <Icon aria-hidden="true" className={cn("shrink-0 text-muted-foreground", className)} />;
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
