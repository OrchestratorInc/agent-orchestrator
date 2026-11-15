import { create } from "zustand";
import { aoBridge } from "../lib/bridge";
import { developerModeStorageKey, readStoredWindowTranslucency, windowTranslucencyStorageKey } from "../lib/window-translucency";
import { sessionUiKey } from "../lib/hosts";
import type { ProjectSettingsSection as ProjectFormSection } from "../components/ProjectSettingsForm";
import type { TerminalTarget } from "../types/terminal";
import type { TerminalClickMode } from "../lib/terminal-mouse";
import type { FilesSelection } from "../hooks/useSessionWorkspaceFiles";
import type { CloudCpWorkspaceReviewScope } from "../lib/cloud-cp";
import {
	applyDocumentTheme,
	applyDocumentThemeStyle,
	isDocumentThemeStyle,
	readStoredThemePreference,
	readStoredThemeStyle,
	resolveTheme,
	runThemeTransition,
	systemTheme,
	themeStorageKey,
	themeStyleStorageKey,
	type Theme,
	type ThemePreference,
	type ThemeStyle,
} from "../lib/theme";

export type { Theme, ThemePreference, ThemeStyle } from "../lib/theme";
export { readStoredThemePreference, readStoredThemeStyle, resolveTheme } from "../lib/theme";

export type ChatSendKeyMode = "enter" | "mod-enter";

export type GlobalSettingsSection =
	| "general"
	| "harness"
	| "teams"
	| "billing"
	| "remoteHosts"
	| "coder"
	| "mobile"
	| "shortcuts"
	| "browserProfiles"
	| "computerUse"
	| "diagnostics"
	| "updates"
	| "help";

export type ProjectSettingsSection = ProjectFormSection | "environment" | "scripts" | "cues" | "repositories" | "instructions" | "sharing";

export type HarnessDiagnosticsRequest = { jobStartedAt?: string; error?: string };

export type SettingsModal =
	| {
			scope: "global";
			section?: GlobalSettingsSection;
			focusAgentId?: string;
			hostId?: string;
			harnessView?: "local" | "cloud";
			startLogin?: boolean;
			startPairing?: boolean;
			showDiagnostics?: HarnessDiagnosticsRequest;
			focusSetting?: string;
			returnTo?: Extract<SettingsModal, { scope: "project" }>;
	}
	| {
			scope: "project";
			projectId: string;
			hostId?: string;
			section?: ProjectSettingsSection;
			cloudOrgId?: string;
	};

export type WorkbenchTab = "changes" | "files" | "terminal";
export type InspectorView = "summary" | "reviews" | "browser" | "sideChat" | "subagents" | "files" | "artifacts";

export type CloudReviewUiState = {
	showChanges?: boolean;
	filter?: string;
	selectedPath?: string | null;
	commitBrowserOpen?: boolean;
	pane?: {
		selectionKey: string;
		files: Record<string, { fingerprint: string; collapsed: boolean; loadedDeferred: boolean }>;
	};
};

export type InspectorSessionState = {
	isOpen: boolean;
	view: InspectorView;
	browserContentRevealed?: boolean;
	browserUnseen?: boolean;
	subagentsAvailable?: boolean;
	artifactsUnseen?: boolean;
	filesChangedOnly?: boolean;
	filesSelection?: FilesSelection;
	cloudReviewSelection?: Record<string, { scope: CloudCpWorkspaceReviewScope; commitSha?: string }>;
	cloudReviewRepository?: string;
	cloudReviewUi?: Record<string, CloudReviewUiState>;
	fileDisplayModes?: Record<string, RememberedFileDisplayMode>;
	initialized?: boolean;
};

export type FileDisplayMode = "diff" | "file" | "rendered";

export type RememberedFileDisplayMode = { mode: FileDisplayMode; requestKey: number };

export type GlobalToast = {
	title: string;
	body?: string;
	tone?: "info" | "error" | "success";
	placement?: "bottom-right" | "top-center" | "bottom-center";
	dismissible?: boolean;
	durationMs?: number;
	dedupeKey?: string;
	action?: { label: string; run: () => void };
	nonce: number;
};

export type GlobalToastOptions = Pick<GlobalToast, "tone" | "placement" | "dismissible" | "durationMs" | "dedupeKey" | "action">;

export type WorkspaceFileOpenRequest = {
	sessionId: string;
	hostId?: string;
	path: string;
	source?: "artifact";
	line?: number;
	nonce: number;
};

export type ChatMessageFocusRequest = {
	sessionId: string;
	messageId: string;
	text: string;
	reviewId?: string;
	nonce: number;
};

export type LoginReturn = {
	sessionId: string;
	agentId: string;
	hostId?: string;
	accountId?: string;
	accountPending?: boolean;
	heldLogin?: { accountId?: string };
	loggedInAt?: number;
	settled?: boolean;
	turnId?: string;
};

export type ArchiveModalRequest = {
	projectId?: string;
	hostId?: string;
	query?: string;
	returnFocus?: HTMLElement | null;
	nonce: number;
};

let archiveModalNonce = 0;

export type UiState = {
	workbenchTab: WorkbenchTab;
	isSidebarOpen: boolean;
	inspectorSessions: Record<string, InspectorSessionState>;
	isCommandPaletteOpen: boolean;
	newChatSending: boolean;
	settingsModal: SettingsModal | null;
	accountLogin: string | null;
	themePreference: ThemePreference;
	resolvedTheme: Theme;
	themeStyle: ThemeStyle;
	developerMode: boolean;
	remoteHosts: boolean;
	diagnostics: boolean;
	sideChats: boolean;
	splitPanes: boolean;
	prView: boolean;
	windowTranslucency: boolean;
	terminalCopyOnSelect: boolean;
	terminalClickMode: TerminalClickMode;
	terminalSelectHintShown: boolean;
	chatSendKeyMode: ChatSendKeyMode;
	navigationGestures: boolean;
	renderMath: boolean;
	projectAvatars: boolean;
	restartingProjectIds: ReadonlySet<string>;
	provisioningProjectIds: ReadonlySet<string>;
	projectCreationPending: boolean;
	autoStartOrchestratorProjectIds: ReadonlySet<string>;
	orchestratorReplacementErrors: Record<string, OrchestratorReplacementFailure>;
	orchestratorStartupErrors: Record<string, string>;
	globalToasts: GlobalToast[];
	globalToast: GlobalToast | null;
	globalToastSequence: number;
	newTaskRequest: { projectId: string; hostId?: string; report?: boolean; nonce: number } | null;
	newChatDraftRevision: number;
	archiveModal: ArchiveModalRequest | null;
	workspaceFileOpenRequest: WorkspaceFileOpenRequest | null;
	chatMessageFocusRequest: ChatMessageFocusRequest | null;
	createProjectNonce: number;
	firstRunPromptActive: boolean;
	setFirstRunPromptActive: (active: boolean) => void;
	folderDropRequest: { path: string; nonce: number } | null;
	newShellTerminalNonce: number;
	activeShellTerminalHandleId: string | null;
	visibleTerminalKindBySession: Record<string, TerminalTarget["kind"]>;
	setWorkbenchTab: (tab: WorkbenchTab) => void;
	setThemePreference: (theme: ThemePreference) => void;
	setThemeStyle: (style: ThemeStyle) => void;
	setDeveloperMode: (enabled: boolean) => void;
	setRemoteHosts: (enabled: boolean) => void;
	setDiagnostics: (enabled: boolean) => void;
	setSideChats: (enabled: boolean) => void;
	setSplitPanes: (enabled: boolean) => void;
	setPRView: (enabled: boolean) => void;
	setWindowTranslucency: (enabled: boolean) => void;
	setTerminalCopyOnSelect: (enabled: boolean) => void;
	setTerminalClickMode: (mode: TerminalClickMode) => void;
	markTerminalSelectHintShown: () => void;
	setChatSendKeyMode: (mode: ChatSendKeyMode) => void;
	setNavigationGestures: (enabled: boolean) => void;
	setRenderMath: (enabled: boolean) => void;
	setProjectAvatars: (enabled: boolean) => void;
	updateInstallPromptOpen: boolean;
	openUpdateInstallPrompt: () => void;
	closeUpdateInstallPrompt: () => void;
	openGlobalSettings: (section?: GlobalSettingsSection, options?: { focusAgentId?: string; hostId?: string; harnessView?: "local" | "cloud"; startLogin?: boolean; loginAccountId?: string; startPairing?: boolean; showDiagnostics?: HarnessDiagnosticsRequest; preserveProject?: boolean; focusSetting?: string }) => void;
	openProjectSettings: (projectId: string, options?: string | { section?: ProjectSettingsSection; cloudOrgId?: string }) => void;
	closeSettings: () => void;
	loginReturn: LoginReturn | null;
	startLoginReturn: (intent: Omit<LoginReturn, "loggedInAt" | "settled" | "turnId">) => void;
	completeLoginReturn: (agentId: string, hostId?: string, accountId?: string) => void;
	setLoginReturnAccount: (sessionId: string, accountId: string | undefined) => void;
	settleLoginReturn: (sessionId: string, turnId?: string) => void;
	clearLoginReturn: (sessionId?: string) => void;
	syncSystemTheme: () => void;
	toggleSidebar: () => void;
	setInspectorOpen: (sessionId: string, isOpen: boolean) => void;
	toggleInspector: (sessionId: string) => void;
	setInspectorView: (sessionId: string, view: InspectorView) => void;
	initializeInspectorSession: (sessionId: string, hasBrowserContent: boolean, hasInspector: boolean) => void;
	setBrowserContentRevealed: (sessionId: string, revealed: boolean) => void;
	setBrowserUnseen: (sessionId: string, unseen: boolean) => void;
	setSubagentsAvailable: (sessionId: string, available: boolean) => void;
	setArtifactsUnseen: (sessionId: string, unseen: boolean) => void;
	setFilesChangedOnly: (sessionId: string, changedOnly: boolean) => void;
	setFilesSelection: (sessionId: string, selection: FilesSelection) => void;
	resetFilesSelection: (sessionId: string) => void;
	setCloudReviewSelection: (sessionId: string, repository: string | undefined, selection: { scope: CloudCpWorkspaceReviewScope; commitSha?: string }) => void;
	setCloudReviewRepository: (sessionId: string, repository: string) => void;
	setCloudReviewUi: (sessionId: string, repository: string | undefined, changes: CloudReviewUiState) => void;
	setFileDisplayMode: (sessionId: string, path: string, mode: FileDisplayMode, requestKey: number) => void;
	setCommandPaletteOpen: (open: boolean) => void;
	setNewChatSending: (sending: boolean) => void;
	setProjectRestarting: (projectId: string, restarting: boolean, hostId?: string) => void;
	setProjectProvisioning: (projectId: string, provisioning: boolean, hostId?: string) => void;
	setAutoStartOrchestrator: (projectId: string, autoStart: boolean) => void;
	setProjectCreationPending: (pending: boolean) => void;
	setOrchestratorReplacementError: (projectId: string, failure: OrchestratorReplacementFailure | null) => void;
	setOrchestratorStartupError: (projectId: string, message: string | null, hostId?: string) => void;
	showGlobalToast: (title: string, body?: string, style?: GlobalToast["tone"] | GlobalToast["placement"] | GlobalToastOptions) => void;
	dismissGlobalToast: (nonce: number) => void;
	clearGlobalToast: () => void;
	requestNewTask: (projectId: string, hostId?: string, options?: { report?: boolean }) => void;
	openArchive: (request: Omit<ArchiveModalRequest, "nonce">) => void;
	closeArchive: () => void;
	requestWorkspaceFileOpen: (sessionId: string, path: string, hostId?: string, source?: "artifact", line?: number) => void;
	clearWorkspaceFileOpenRequest: (nonce: number) => void;
	requestChatMessageFocus: (sessionId: string, messageId: string, text: string, reviewId?: string) => void;
	clearChatMessageFocusRequest: (nonce: number) => void;
	requestCreateProject: () => void;
	requestCreateProjectFromPath: (path: string) => void;
	requestNewShellTerminal: () => void;
	setActiveShellTerminal: (handleId: string | null) => void;
	setVisibleTerminalKind: (sessionId: string, kind: TerminalTarget["kind"]) => void;
	clearVisibleTerminalKind: (sessionId: string) => void;
};

export type OrchestratorReplacementFailure = {
	message: string;
	code?: string;
	requestId?: string;
	details?: Record<string, unknown>;
};

const sidebarStorageKey = "ao.sidebar.open";
const remoteHostsStorageKey = "ao.remoteHosts";
const diagnosticsStorageKey = "ao.diagnostics";
const sideChatsStorageKey = "ao.sideChats";
const splitPanesStorageKey = "ao.splitPanes";
const prViewStorageKey = "ao.prView";
const terminalCopyOnSelectStorageKey = "ao.terminalCopyOnSelect";
const terminalClickModeStorageKey = "ao.terminalClickMode";
const terminalSelectHintShownStorageKey = "ao.terminalSelectHintShown";
const chatSendKeyModeStorageKey = "ao.chatSendKeyMode";
const navigationGesturesStorageKey = "ao.navigationGestures";
const renderMathStorageKey = "ao.renderMath";
const projectAvatarsStorageKey = "ao.projectAvatars";
const retiredNewSidebarDesignStorageKey = "ao.newSidebarDesign";
function getLocalStorage() {
	if (typeof window === "undefined" || !window.localStorage) return null;
	return window.localStorage;
}

function initialSidebarOpen() {
	return getLocalStorage()?.getItem(sidebarStorageKey) !== "false";
}

function initialDeveloperMode() {
	return getLocalStorage()?.getItem(developerModeStorageKey) === "true";
}

function initialRemoteHosts() {
	return getLocalStorage()?.getItem(remoteHostsStorageKey) !== "false";
}

function initialDiagnostics() {
	return getLocalStorage()?.getItem(diagnosticsStorageKey) === "true";
}

function initialSideChats() {
	return getLocalStorage()?.getItem(sideChatsStorageKey) === "true";
}

function initialTerminalClickMode(): TerminalClickMode {
	return getLocalStorage()?.getItem(terminalClickModeStorageKey) === "select" ? "select" : "app";
}

function initialTerminalCopyOnSelect() {
	return getLocalStorage()?.getItem(terminalCopyOnSelectStorageKey) !== "false";
}

function initialProjectAvatars() {
	return getLocalStorage()?.getItem(projectAvatarsStorageKey) !== "false";
}

function initialChatSendKeyMode(): ChatSendKeyMode {
	return getLocalStorage()?.getItem(chatSendKeyModeStorageKey) === "mod-enter" ? "mod-enter" : "enter";
}

function syncDeveloperModeToUpdater(enabled: boolean): void {
	const request = aoBridge.updateSettings?.setMacDifferentialUpdates?.(enabled);
	void request?.catch(() => undefined);
}

function loginTaken(state: UiState, waiting: LoginReturn): Partial<UiState> {
	const modal = state.settingsModal;
	return {
		loginReturn: { ...waiting, loggedInAt: Date.now() },
		...(modal?.scope === "global" ? { settingsModal: modal.returnTo ?? null } : {}),
	};
}

function inspectorState(sessions: Record<string, InspectorSessionState>, sessionId: string): InspectorSessionState {
	return sessions[sessionId] ?? { isOpen: false, view: "summary" };
}

export function inspectorIsOpen(sessions: Record<string, InspectorSessionState>, sessionId: string): boolean {
	return sessions[sessionId]?.isOpen ?? false;
}

export function rememberedFileDisplayMode(
	state: Pick<UiState, "inspectorSessions">,
	sessionId: string,
	path: string | null,
	requestKey: number,
): FileDisplayMode | undefined {
	if (!path) return undefined;
	const remembered = state.inspectorSessions[sessionId]?.fileDisplayModes?.[path];
	return remembered?.requestKey === requestKey ? remembered.mode : undefined;
}

export function sidebarIsVisible(state: Pick<UiState, "isSidebarOpen">): boolean {
	return state.isSidebarOpen;
}

export function sidebarOccupiesLayout(state: Pick<UiState, "isSidebarOpen">): boolean {
	return state.isSidebarOpen;
}

const initialThemePreference = readStoredThemePreference();
const initialThemeStyle = readStoredThemeStyle();
const initialDeveloperModeValue = initialDeveloperMode();

export const useUiStore = create<UiState>((set, get) => ({
	workbenchTab: "changes",
	isSidebarOpen: initialSidebarOpen(),
	inspectorSessions: {},
	isCommandPaletteOpen: false,
	newChatSending: false,
	settingsModal: null,
	accountLogin: null,
	themePreference: initialThemePreference,
	resolvedTheme: resolveTheme(initialThemePreference),
	themeStyle: initialThemeStyle,
	developerMode: initialDeveloperModeValue,
	remoteHosts: initialRemoteHosts(),
	diagnostics: initialDiagnostics(),
	sideChats: initialSideChats(),
	splitPanes: getLocalStorage()?.getItem(splitPanesStorageKey) === "true",
	prView: getLocalStorage()?.getItem(prViewStorageKey) === "true",
	windowTranslucency: readStoredWindowTranslucency(),
	terminalCopyOnSelect: initialTerminalCopyOnSelect(),
	terminalClickMode: initialTerminalClickMode(),
	terminalSelectHintShown: getLocalStorage()?.getItem(terminalSelectHintShownStorageKey) === "true",
	chatSendKeyMode: initialChatSendKeyMode(),
	navigationGestures: getLocalStorage()?.getItem(navigationGesturesStorageKey) !== "false",
	renderMath: getLocalStorage()?.getItem(renderMathStorageKey) === "true",
	projectAvatars: initialProjectAvatars(),
	restartingProjectIds: new Set<string>(),
	provisioningProjectIds: new Set<string>(),
	projectCreationPending: false,
	autoStartOrchestratorProjectIds: new Set<string>(),
	orchestratorReplacementErrors: {},
	orchestratorStartupErrors: {},
	globalToasts: [],
	globalToast: null,
	globalToastSequence: 0,
	newTaskRequest: null,
	archiveModal: null,
	newChatDraftRevision: 0,
	firstRunPromptActive: false,
	setFirstRunPromptActive: (active) => set({ firstRunPromptActive: active }),
	workspaceFileOpenRequest: null,
	chatMessageFocusRequest: null,
	createProjectNonce: 0,
	folderDropRequest: null,
	newShellTerminalNonce: 0,
	activeShellTerminalHandleId: null,
	visibleTerminalKindBySession: {},
	setWorkbenchTab: (workbenchTab) => set({ workbenchTab }),
	setThemePreference: (themePreference) => {
		if (get().themePreference === themePreference) return;
		runThemeTransition(
			() => {
				const resolvedTheme = resolveTheme(themePreference);
				getLocalStorage()?.setItem(themeStorageKey, themePreference);
				applyDocumentTheme(resolvedTheme);
				set({ themePreference, resolvedTheme });
			},
			() => void aoBridge.theme?.set(themePreference)?.catch(() => undefined),
		);
	},
	setThemeStyle: (themeStyle) => {
		if (get().themeStyle === themeStyle) return;
		if (isDocumentThemeStyle(themeStyle)) {
			getLocalStorage()?.setItem(themeStyleStorageKey, themeStyle);
			set({ themeStyle });
			return;
		}
		runThemeTransition(() => {
			getLocalStorage()?.setItem(themeStyleStorageKey, themeStyle);
			applyDocumentThemeStyle(themeStyle);
			set({ themeStyle });
		});
	},
	setDeveloperMode: (developerMode) => {
		getLocalStorage()?.setItem(developerModeStorageKey, String(developerMode));
		set({ developerMode });
		syncDeveloperModeToUpdater(developerMode);
	},
	setRemoteHosts: (remoteHosts) => {
		getLocalStorage()?.setItem(remoteHostsStorageKey, String(remoteHosts));
		set({ remoteHosts });
	},
	setDiagnostics: (diagnostics) => {
		getLocalStorage()?.setItem(diagnosticsStorageKey, String(diagnostics));
		set({ diagnostics });
	},
	setSideChats: (sideChats) => {
		getLocalStorage()?.setItem(sideChatsStorageKey, String(sideChats));
		set({ sideChats });
	},
	setSplitPanes: (splitPanes) => {
		getLocalStorage()?.setItem(splitPanesStorageKey, String(splitPanes));
		set({ splitPanes });
	},
	setPRView: (prView) => {
		getLocalStorage()?.setItem(prViewStorageKey, String(prView));
		set({ prView });
	},
	setWindowTranslucency: (windowTranslucency) => {
		getLocalStorage()?.setItem(windowTranslucencyStorageKey, String(windowTranslucency));
		set({ windowTranslucency });
	},
	setNavigationGestures: (navigationGestures) => {
		getLocalStorage()?.setItem(navigationGesturesStorageKey, String(navigationGestures));
		set({ navigationGestures });
	},
	setRenderMath: (renderMath) => {
		getLocalStorage()?.setItem(renderMathStorageKey, String(renderMath));
		set({ renderMath });
	},
	setTerminalCopyOnSelect: (terminalCopyOnSelect) => {
		getLocalStorage()?.setItem(terminalCopyOnSelectStorageKey, String(terminalCopyOnSelect));
		set({ terminalCopyOnSelect });
	},
	setTerminalClickMode: (terminalClickMode) => {
		getLocalStorage()?.setItem(terminalClickModeStorageKey, terminalClickMode);
		set({ terminalClickMode });
	},
	markTerminalSelectHintShown: () => {
		getLocalStorage()?.setItem(terminalSelectHintShownStorageKey, "true");
		set({ terminalSelectHintShown: true });
	},
	setProjectAvatars: (projectAvatars) => {
		getLocalStorage()?.setItem(projectAvatarsStorageKey, String(projectAvatars));
		set({ projectAvatars });
	},
	setChatSendKeyMode: (chatSendKeyMode) => {
		getLocalStorage()?.setItem(chatSendKeyModeStorageKey, chatSendKeyMode);
		set({ chatSendKeyMode });
	},
	updateInstallPromptOpen: false,
	openUpdateInstallPrompt: () => set({ updateInstallPromptOpen: true }),
	closeUpdateInstallPrompt: () => set({ updateInstallPromptOpen: false }),
	openGlobalSettings: (section, options) => set((state) => ({
		accountLogin: options?.loginAccountId ?? null,
		settingsModal: {
			scope: "global",
			section,
			...(options?.focusAgentId ? { focusAgentId: options.focusAgentId } : {}),
			...(options?.hostId && options.hostId !== "local" ? { hostId: options.hostId } : {}),
			...(options?.harnessView ? { harnessView: options.harnessView } : {}),
			...(options?.startLogin && options.focusAgentId ? { startLogin: true } : {}),
			...(options?.startPairing && section === "mobile" ? { startPairing: true } : {}),
			...(options?.showDiagnostics && options.focusAgentId ? { showDiagnostics: options.showDiagnostics } : {}),
			...(options?.focusSetting ? { focusSetting: options.focusSetting } : {}),
			...(options?.preserveProject && state.settingsModal?.scope === "project"
				? { returnTo: state.settingsModal }
				: options?.preserveProject && state.settingsModal?.scope === "global" && state.settingsModal.returnTo
					? { returnTo: state.settingsModal.returnTo }
					: {}),
		},
	})),
	openProjectSettings: (projectId, options) => set({
		accountLogin: null,
		settingsModal: {
			scope: "project",
			projectId,
			...(typeof options === "string" && options !== "local" ? { hostId: options } : {}),
			...(typeof options === "object" && options?.section ? { section: options.section } : {}),
			...(typeof options === "object" && options?.cloudOrgId !== undefined ? { cloudOrgId: options.cloudOrgId } : {}),
		},
	}),
	closeSettings: () => set((state) => ({
		accountLogin: null,
		settingsModal: state.settingsModal?.scope === "global" ? state.settingsModal.returnTo ?? null : null,
	})),
	loginReturn: null,
	startLoginReturn: (intent) => set({ loginReturn: intent }),
	completeLoginReturn: (agentId, hostId, accountId) => set((state) => {
		const waiting = state.loginReturn;
		if (!waiting || waiting.loggedInAt !== undefined || waiting.agentId !== agentId) return {};
		if ((waiting.hostId ?? "local") !== (hostId ?? "local")) return {};
		if (waiting.accountPending) return { loginReturn: { ...waiting, heldLogin: accountId ? { accountId } : {} } };
		if (waiting.accountId !== undefined && waiting.accountId !== accountId) return {};
		return loginTaken(state, waiting);
	}),
	setLoginReturnAccount: (sessionId, accountId) => set((state) => {
		if (state.loginReturn?.sessionId !== sessionId || state.loginReturn.loggedInAt !== undefined) return {};
		const { heldLogin, ...waiting } = state.loginReturn;
		const known: LoginReturn = { ...waiting, accountPending: false, ...(accountId ? { accountId } : {}) };
		const counts = heldLogin && (known.accountId === undefined || known.accountId === heldLogin.accountId);
		return counts ? loginTaken(state, known) : { loginReturn: known };
	}),
	settleLoginReturn: (sessionId, turnId) => set((state) =>
		state.loginReturn?.sessionId === sessionId && state.loginReturn.loggedInAt !== undefined && !state.loginReturn.settled
			? { loginReturn: { ...state.loginReturn, settled: true, ...(turnId ? { turnId } : {}) } }
			: {}),
	clearLoginReturn: (sessionId) => set((state) =>
		!state.loginReturn || (sessionId !== undefined && state.loginReturn.sessionId !== sessionId) ? {} : { loginReturn: null }),
	syncSystemTheme: () => {
		const { themePreference, resolvedTheme } = get();
		if (themePreference !== "system") return;
		const next = systemTheme();
		if (next === resolvedTheme) return;
		runThemeTransition(() => {
			applyDocumentTheme(next);
			set({ resolvedTheme: next });
		});
	},
	toggleSidebar: () =>
		set((state) => {
			const isSidebarOpen = !state.isSidebarOpen;
			getLocalStorage()?.setItem(sidebarStorageKey, String(isSidebarOpen));
			return { isSidebarOpen };
		}),
	setInspectorOpen: (sessionId, isOpen) =>
		set((state) => {
			const current = inspectorState(state.inspectorSessions, sessionId);
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: { ...current, isOpen },
				},
			};
		}),
	toggleInspector: (sessionId) =>
		set((state) => {
			const current = inspectorState(state.inspectorSessions, sessionId);
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: { ...current, isOpen: !current.isOpen },
				},
			};
		}),
	setInspectorView: (sessionId, view) =>
		set((state) => {
			const current = inspectorState(state.inspectorSessions, sessionId);
			const browserUnseen = view === "browser" ? false : current.browserUnseen;
			const artifactsUnseen = view === "artifacts" ? false : current.artifactsUnseen;
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: { ...current, view, browserUnseen, artifactsUnseen },
				},
			};
		}),
	initializeInspectorSession: (sessionId, hasBrowserContent, hasInspector) =>
		set((state) => {
			if (!hasInspector) return state;
			const current = inspectorState(state.inspectorSessions, sessionId);
			if (current.initialized) return state;
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: {
						...current,
						initialized: true,
						view: "summary",
						browserContentRevealed: current.browserContentRevealed ?? hasBrowserContent,
					},
				},
			};
		}),
	setBrowserContentRevealed: (sessionId, browserContentRevealed) =>
		set((state) => {
			const current = inspectorState(state.inspectorSessions, sessionId);
			if (Boolean(current.browserContentRevealed) === browserContentRevealed) return state;
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: {
						...current,
						browserContentRevealed,
						browserUnseen: browserContentRevealed ? current.browserUnseen : false,
					},
				},
			};
		}),
	setBrowserUnseen: (sessionId, browserUnseen) =>
		set((state) => {
			const current = inspectorState(state.inspectorSessions, sessionId);
			if (Boolean(current.browserUnseen) === browserUnseen) return state;
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: { ...current, browserUnseen },
				},
			};
		}),
	setSubagentsAvailable: (sessionId, subagentsAvailable) =>
		set((state) => {
			const current = inspectorState(state.inspectorSessions, sessionId);
			if (Boolean(current.subagentsAvailable) === subagentsAvailable) return state;
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: { ...current, subagentsAvailable },
				},
			};
		}),
	setArtifactsUnseen: (sessionId, artifactsUnseen) =>
		set((state) => {
			const current = inspectorState(state.inspectorSessions, sessionId);
			if (Boolean(current.artifactsUnseen) === artifactsUnseen) return state;
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: { ...current, artifactsUnseen },
				},
			};
		}),
	setFilesChangedOnly: (sessionId, filesChangedOnly) =>
		set((state) => {
			const current = inspectorState(state.inspectorSessions, sessionId);
			if ((current.filesChangedOnly ?? true) === filesChangedOnly) return state;
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: { ...current, filesChangedOnly },
				},
			};
		}),
	setFilesSelection: (sessionId, filesSelection) =>
		set((state) => {
			const current = inspectorState(state.inspectorSessions, sessionId);
			if (current.filesSelection === filesSelection) return state;
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: { ...current, filesSelection },
				},
			};
		}),
	resetFilesSelection: (sessionId) =>
		set((state) => {
			const current = state.inspectorSessions[sessionId];
			if (!current || (!current.filesSelection && !current.cloudReviewSelection && current.cloudReviewRepository === undefined && !current.cloudReviewUi)) return state;
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: { ...current, filesSelection: undefined, cloudReviewSelection: undefined, cloudReviewRepository: undefined, cloudReviewUi: undefined },
				},
			};
		}),
	setCloudReviewSelection: (sessionId, repository, selection) =>
		set((state) => {
			const current = inspectorState(state.inspectorSessions, sessionId);
			const key = repository ?? "";
			const previous = current.cloudReviewSelection?.[key];
			if (previous?.scope === selection.scope && previous.commitSha === selection.commitSha) return state;
			const cloudReviewUi = current.cloudReviewUi?.[key]?.pane
				? { ...current.cloudReviewUi, [key]: { ...current.cloudReviewUi[key], pane: undefined } }
				: current.cloudReviewUi;
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: { ...current, cloudReviewSelection: { ...current.cloudReviewSelection, [key]: selection }, cloudReviewUi },
				},
			};
		}),
	setCloudReviewRepository: (sessionId, cloudReviewRepository) =>
		set((state) => {
			const current = inspectorState(state.inspectorSessions, sessionId);
			if ((current.cloudReviewRepository ?? "") === cloudReviewRepository) return state;
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: { ...current, cloudReviewRepository },
				},
			};
		}),
	setCloudReviewUi: (sessionId, repository, changes) =>
		set((state) => {
			const current = inspectorState(state.inspectorSessions, sessionId);
			const key = repository ?? "";
			const previous = current.cloudReviewUi?.[key];
			if (previous && Object.entries(changes).every(([field, value]) => previous[field as keyof CloudReviewUiState] === value)) return state;
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: { ...current, cloudReviewUi: { ...current.cloudReviewUi, [key]: { ...previous, ...changes } } },
				},
			};
		}),
	setFileDisplayMode: (sessionId, path, mode, requestKey) =>
		set((state) => {
			const current = inspectorState(state.inspectorSessions, sessionId);
			const previous = current.fileDisplayModes?.[path];
			if (previous?.mode === mode && previous.requestKey === requestKey) return state;
			return {
				inspectorSessions: {
					...state.inspectorSessions,
					[sessionId]: { ...current, fileDisplayModes: { ...current.fileDisplayModes, [path]: { mode, requestKey } } },
				},
			};
		}),
	setCommandPaletteOpen: (isCommandPaletteOpen) => set({ isCommandPaletteOpen }),
	setNewChatSending: (newChatSending) => set({ newChatSending }),
	setProjectRestarting: (projectId, restarting, hostId) =>
		set((state) => {
			const restartingProjectIds = new Set(state.restartingProjectIds);
			const key = sessionUiKey(projectId, hostId);
			if (restarting) {
				restartingProjectIds.add(key);
			} else {
				restartingProjectIds.delete(key);
			}
			return { restartingProjectIds };
		}),
	setProjectCreationPending: (pending) => set({ projectCreationPending: pending }),
	setAutoStartOrchestrator: (projectId, autoStart) =>
		set((state) => {
			if (state.autoStartOrchestratorProjectIds.has(projectId) === autoStart) return state;
			const autoStartOrchestratorProjectIds = new Set(state.autoStartOrchestratorProjectIds);
			if (autoStart) autoStartOrchestratorProjectIds.add(projectId);
			else autoStartOrchestratorProjectIds.delete(projectId);
			return { autoStartOrchestratorProjectIds };
		}),
	setProjectProvisioning: (projectId, provisioning, hostId) =>
		set((state) => {
			const provisioningProjectIds = new Set(state.provisioningProjectIds);
			const key = sessionUiKey(projectId, hostId);
			if (provisioning) {
				provisioningProjectIds.add(key);
			} else {
				provisioningProjectIds.delete(key);
			}
			return { provisioningProjectIds };
		}),
	setOrchestratorReplacementError: (projectId, failure) =>
		set((state) => {
			const orchestratorReplacementErrors = { ...state.orchestratorReplacementErrors };
			if (failure) {
				orchestratorReplacementErrors[projectId] = failure;
			} else {
				delete orchestratorReplacementErrors[projectId];
			}
			return { orchestratorReplacementErrors };
		}),
	setOrchestratorStartupError: (projectId, message, hostId) =>
		set((state) => {
			const orchestratorStartupErrors = { ...state.orchestratorStartupErrors };
			const key = sessionUiKey(projectId, hostId);
			if (message) {
				orchestratorStartupErrors[key] = message;
			} else {
				delete orchestratorStartupErrors[key];
			}
			return { orchestratorStartupErrors };
		}),
	showGlobalToast: (title, body, style) =>
		set((state) => {
			const nonce = state.globalToastSequence + 1;
			const options = typeof style === "object" ? style : undefined;
			const tone = options?.tone ?? (style === "error" || style === "info" || style === "success" ? style : "info");
			const placement = options?.placement ?? (style === "top-center" || style === "bottom-right" || style === "bottom-center" ? style : "bottom-right");
			const toast = { title, body, tone, placement, nonce, ...options };
			const globalToasts = toast.dedupeKey
				? state.globalToasts.filter((existing) => existing.dedupeKey !== toast.dedupeKey)
				: state.globalToasts;
			return { globalToast: toast, globalToasts: [...globalToasts, toast], globalToastSequence: nonce };
		}),
	dismissGlobalToast: (nonce) =>
		set((state) => ({
			globalToasts: state.globalToasts.filter((toast) => toast.nonce !== nonce),
			globalToast: state.globalToast?.nonce === nonce ? null : state.globalToast,
		})),
	clearGlobalToast: () => set({ globalToast: null, globalToasts: [], globalToastSequence: 0 }),
	requestNewTask: (projectId, hostId, options) => {
		if (get().provisioningProjectIds.has(sessionUiKey(projectId, hostId))) {
			get().showGlobalToast(
				"Project is still being set up",
				"The orchestrator is starting. Try again in a moment.",
				"info",
			);
			return;
		}
		set((state) => ({ newTaskRequest: { projectId, hostId, report: options?.report, nonce: (state.newTaskRequest?.nonce ?? 0) + 1 } }));
	},
	openArchive: (request) =>
		set(() => {
			archiveModalNonce += 1;
			return { archiveModal: { ...request, nonce: archiveModalNonce } };
		}),
	closeArchive: () => set((state) => (state.archiveModal ? { archiveModal: null } : state)),
	requestWorkspaceFileOpen: (sessionId, path, hostId, source, line) =>
		set((state) => ({
			workspaceFileOpenRequest: {
				sessionId,
				path,
				...(hostId ? { hostId } : {}),
				...(source ? { source } : {}),
				...(line ? { line } : {}),
				nonce: (state.workspaceFileOpenRequest?.nonce ?? 0) + 1,
			},
		})),
	clearWorkspaceFileOpenRequest: (nonce) =>
		set((state) => state.workspaceFileOpenRequest?.nonce === nonce
			? { workspaceFileOpenRequest: null }
			: state),
	requestChatMessageFocus: (sessionId, messageId, text, reviewId) =>
		set((state) => ({
			chatMessageFocusRequest: {
				sessionId,
				messageId,
				text,
				...(reviewId ? { reviewId } : {}),
				nonce: (state.chatMessageFocusRequest?.nonce ?? 0) + 1,
			},
		})),
	clearChatMessageFocusRequest: (nonce) =>
		set((state) => state.chatMessageFocusRequest?.nonce === nonce
			? { chatMessageFocusRequest: null }
			: state),
	requestCreateProject: () => set((state) => ({ createProjectNonce: state.createProjectNonce + 1 })),
	requestCreateProjectFromPath: (path) =>
		set((state) => ({ folderDropRequest: { path, nonce: (state.folderDropRequest?.nonce ?? 0) + 1 } })),
	requestNewShellTerminal: () => set((state) => ({ newShellTerminalNonce: state.newShellTerminalNonce + 1 })),
	setActiveShellTerminal: (activeShellTerminalHandleId) => set({ activeShellTerminalHandleId }),
	setVisibleTerminalKind: (sessionId, kind) =>
		set((state) =>
			state.visibleTerminalKindBySession[sessionId] === kind
				? state
				: { visibleTerminalKindBySession: { ...state.visibleTerminalKindBySession, [sessionId]: kind } },
		),
	clearVisibleTerminalKind: (sessionId) =>
		set((state) => {
			if (!(sessionId in state.visibleTerminalKindBySession)) return state;
			const visibleTerminalKindBySession = { ...state.visibleTerminalKindBySession };
			delete visibleTerminalKindBySession[sessionId];
			return { visibleTerminalKindBySession };
		}),
}));

try {
	getLocalStorage()?.removeItem(retiredNewSidebarDesignStorageKey);
} catch {
}

syncDeveloperModeToUpdater(initialDeveloperModeValue);

export function useResolvedTheme(): Theme {
	return useUiStore((state) => state.resolvedTheme);
}
