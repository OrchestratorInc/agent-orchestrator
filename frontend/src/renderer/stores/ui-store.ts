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

/** Project settings pages: the project form sections plus the cues manager. */
export type ProjectSettingsSection = ProjectFormSection | "environment" | "scripts" | "cues" | "repositories" | "instructions" | "sharing";

/** Which failure a "Show diagnostics" shortcut is about: the job it produced,
 * if any, and the error the user saw. */
export type HarnessDiagnosticsRequest = { jobStartedAt?: string; error?: string };

export type SettingsModal =
	| {
			scope: "global";
			section?: GlobalSettingsSection;
			focusAgentId?: string;
			hostId?: string;
			/** Which Harness page view (local or cloud logins) to open. */
			harnessView?: "local" | "cloud";
			/** Start focusAgentId's login flow as soon as the Harness page can. */
			startLogin?: boolean;
			/** Turn Connect Mobile on as the page opens: the user asked to pair a phone. */
			startPairing?: boolean;
			/** Open the diagnostics of focusAgentId's failed install or update, ready to copy. */
			showDiagnostics?: HarnessDiagnosticsRequest;
			/** Visible label of one setting on the page to scroll to and highlight, e.g. a palette hit. */
			focusSetting?: string;
			/** Preserve the project form while global recovery settings is above it. */
			returnTo?: Extract<SettingsModal, { scope: "project" }>;
	}
	| {
			scope: "project";
			projectId: string;
			hostId?: string;
			/** Page to open on, so callers can deep-link a project setting. */
			section?: ProjectSettingsSection;
			cloudOrgId?: string;
	};

/** Worker detail view toggles — Changes (Git rail) is the default. */
export type WorkbenchTab = "changes" | "files" | "terminal";
export type InspectorView = "summary" | "reviews" | "browser" | "sideChat" | "files" | "artifacts";

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
	/** The current non-empty browser content lifecycle has already been revealed. */
	browserContentRevealed?: boolean;
	/** Real browser activity occurred while Browser was not visible. */
	browserUnseen?: boolean;
	/** An artifact was registered while the user was busy in Browser or Files. */
	artifactsUnseen?: boolean;
	/** Files tab: review changed files directly. Defaults to true; false shows the full tree. */
	filesChangedOnly?: boolean;
	/** Files tab: the branch and changes under review, shared by the docked and maximized explorers. Unset follows the default. */
	filesSelection?: FilesSelection;
	/** Cloud Files: review targets per repository, shared by docked and maximized explorers. The main repository uses "". */
	cloudReviewSelection?: Record<string, { scope: CloudCpWorkspaceReviewScope; commitSha?: string }>;
	/** Cloud Files: selected repository across remounts; "" or unset selects the main repository. */
	cloudReviewRepository?: string;
	cloudReviewUi?: Record<string, CloudReviewUiState>;
	/** Files: display mode picked per path in a centre tab, restored for the same open request after a remount. */
	fileDisplayModes?: Record<string, RememberedFileDisplayMode>;
	/** The session-entry defaulting (Summary tab, baseline browser reveal) has already run once for this session's lifetime. */
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
	/** Zero keeps the toast visible until explicitly dismissed. */
	durationMs?: number;
	dedupeKey?: string;
	/** One button on the toast, such as Undo or Open; pressing it dismisses the toast. */
	action?: { label: string; run: () => void };
	nonce: number;
};

export type GlobalToastOptions = Pick<GlobalToast, "tone" | "placement" | "dismissible" | "durationMs" | "dedupeKey" | "action">;

export type WorkspaceFileOpenRequest = {
	sessionId: string;
	hostId?: string;
	path: string;
	/** "artifact" resolves `path` against the session's artifact files instead of its workspace. */
	source?: "artifact";
	/** 1-based line to reveal, e.g. a palette content hit. */
	line?: number;
	nonce: number;
};

/** Asks a local session's chat to scroll to one message, e.g. a palette chat hit. */
export type ChatMessageFocusRequest = {
	sessionId: string;
	messageId: string;
	/** The matched text, used to mark the passage inside the message. */
	text: string;
	/** Set when the message is in the session's reviewer chat for this review. */
	reviewId?: string;
	nonce: number;
};

/**
 * A chat whose agent refused its login, waiting while the user logs in from the
 * Agents page. A login that takes there closes the page; the chat then sends
 * its refused message again when that is safe, or says the user is in.
 */
export type LoginReturn = {
	sessionId: string;
	agentId: string;
	/** The remote host whose login is being fixed; omitted for this computer. */
	hostId?: string;
	/**
	 * The account the session runs on, by id (the computer's own login is one
	 * too). A login to another account does not fix the session, so it does not
	 * count. Unknown for a remote host, whose accounts this app cannot read.
	 */
	accountId?: string;
	/**
	 * The account is still being read. A login that takes meanwhile is held until
	 * it is known, since only that account's login fixes the session.
	 */
	accountPending?: boolean;
	/** A login that took while the account was read, by the account it signed in. */
	heldLogin?: { accountId?: string };
	/** When the login took. */
	loggedInAt?: number;
	/** The chat has seen the login and sent nothing again: it says the user is in. */
	settled?: boolean;
	/**
	 * The refused message the chat saw the login for. A refusal of another one
	 * (the message sent again, refused once more) means the login did not fix it.
	 */
	turnId?: string;
};

/** What the archive modal shows. */
export type ArchiveModalRequest = {
	/** The project it starts scoped to; unset shows every project on the host. */
	projectId?: string;
	hostId?: string;
	/** Prefills the search. */
	query?: string;
	/**
	 * Where focus goes once the modal closes: the board's button, the sidebar
	 * menu's trigger or row, or wherever Cmd+K was opened from. The dialog has
	 * no trigger of its own to return to.
	 */
	returnFocus?: HTMLElement | null;
	/** New for every request, so a request made while open retargets it. */
	nonce: number;
};

// Monotonic across closes, so a request is never mistaken for the last one.
let archiveModalNonce = 0;

// Selection (which project/session is open) now lives in the URL — the router
// is the single source of truth, read via route params. This store holds only
// ephemeral UI: theme, sidebar collapse, command palette, per-session inspector
// state, and the active workbench tab within a session.
export type UiState = {
	workbenchTab: WorkbenchTab;
	/** The user's durable sidebar preference. */
	isSidebarOpen: boolean;
	inspectorSessions: Record<string, InspectorSessionState>;
	isCommandPaletteOpen: boolean;
	/** The new chat page has sent its first message and is waiting to hand off to the session. */
	newChatSending: boolean;
	settingsModal: SettingsModal | null;
	accountLogin: string | null;
	themePreference: ThemePreference;
	/** Resolved light/dark for React consumers; may track OS while preference is system. */
	resolvedTheme: Theme;
	/** Named color style theme (e.g. "catppuccin", "nord") — independent of light/dark mode. */
	themeStyle: ThemeStyle;
	/** When true, developer-only release controls are available. Default off. */
	developerMode: boolean;
	/** Experimental: connect to AO daemons on other machines. Default off. */
	remoteHosts: boolean;
	/** Memory and CPU monitoring (card chips, memory light, Diagnostics page). Only takes effect in Developer mode. Default off. */
	diagnostics: boolean;
	/** Experimental side chats in the inspector. Only takes effect in Developer mode. Default off. */
	sideChats: boolean;
	/** Home offers the session import once: until the user imports or dismisses it. */
	sessionImportOfferClosed: boolean;
	/** Drag center tabs side by side into split panes. Only takes effect in Developer mode. Default off. */
	splitPanes: boolean;
	/** The PR view: a PR's own tab, its comments, stacks and actions. Only takes effect in Developer mode. Default off. */
	prView: boolean;
	/** Developer Mode, macOS only: blur the desktop behind a translucent window. */
	windowTranslucency: boolean;
	/** Copy the terminal selection to the clipboard on mouse-up, like native terminals. Default on. */
	terminalCopyOnSelect: boolean;
	/**
	 * Where plain terminal clicks go when the running program has asked for the
	 * mouse (DECSET 1000/1002/1003). "app" reports them to the program, like a
	 * native terminal; Option (macOS) or Shift (elsewhere) still selects text.
	 * "select" always selects text, the behavior before this setting existed.
	 */
	terminalClickMode: TerminalClickMode;
	/** The one-time "hold Option to select text" hint has been shown. */
	terminalSelectHintShown: boolean;
	/** Chat only: plain Enter sends by default; mod-enter reserves it for newlines. */
	chatSendKeyMode: ChatSendKeyMode;
	navigationGestures: boolean;
	/** Typeset `$…$`, `$$…$$`, `\(…\)` and `\[…\]` in markdown. Default off: a lone `$` is also currency. */
	renderMath: boolean;
	/** Show each project's repository owner avatar in the sidebar instead of a folder. Default on. */
	projectAvatars: boolean;
	restartingProjectIds: ReadonlySet<string>;
	// Projects whose initial orchestrator spawn (after import/clone) is still
	// running in the background. The board renders a progress banner and gates
	// session actions until the spawn settles, instead of blocking navigation.
	provisioningProjectIds: ReadonlySet<string>;
	projectCreationPending: boolean;
	// Freestyle cloud projects just created, whose board starts the orchestrator
	// on its own instead of waiting for the Orchestrator button.
	autoStartOrchestratorProjectIds: ReadonlySet<string>;
	orchestratorReplacementErrors: Record<string, OrchestratorReplacementFailure>;
	orchestratorStartupErrors: Record<string, string>;
	globalToasts: GlobalToast[];
	globalToast: GlobalToast | null;
	globalToastSequence: number;
	// Transient "start a new task for this project" signal. The nonce bumps on
	// every request so a repeat press (even for the same project) still re-fires;
	// the always-mounted NewTaskNavigator consumes it by opening the new chat
	// page. Selection still lives in the URL — this is a one-shot action.
	newTaskRequest: { projectId: string; hostId?: string; report?: boolean; nonce: number } | null;
	// Bumped after writing the new chat draft from outside the page, so an open
	// composer reloads it.
	newChatDraftRevision: number;
	// The archive modal, open over whatever page is showing (the shell hosts
	// it). Null while closed. Every entry point (the board's Archive button,
	// a sidebar project's menu, Cmd+K) opens it here; none navigates.
	archiveModal: ArchiveModalRequest | null;
	/** Transient one-shot request to reveal a path in a session's existing Files UI. */
	workspaceFileOpenRequest: WorkspaceFileOpenRequest | null;
	chatMessageFocusRequest: ChatMessageFocusRequest | null;
	// Bumps to ask the sidebar's create-project flow to open (the ⌘N fallback
	// when no project is in scope).
	createProjectNonce: number;
	/** True while the first-run project prompt is pending, so shell notices wait for it. */
	firstRunPromptActive: boolean;
	setFirstRunPromptActive: (active: boolean) => void;
	// Transient "a folder was dropped onto the app window — open the
	// create-project flow for this path" signal, mirroring newTaskRequest: the
	// nonce always bumps so dropping the same folder twice in a row still
	// re-fires. Consumed by the same CreateProjectFlow instance that owns
	// openSignal for ⌘N (Sidebar's CreateProjectButton).
	folderDropRequest: { path: string; nonce: number } | null;
	// Bumps to ask for a new standalone shell terminal. Like newTaskRequest this
	// is a one-shot signal, not state: the tab-strip + button and Ctrl+Shift+` both
	// raise it so they cannot drift apart, and a repeat press re-fires because
	// the nonce always changes. The shell layout is its single consumer — it is
	// mounted on every route, so the request is honoured from anywhere in the app.
	newShellTerminalNonce: number;
	// The shell terminal the user most recently opened or selected. Both the
	// session view (tabs beside the session's pane) and the standalone terminals
	// view read it, so whichever one is on screen shows the same shell.
	activeShellTerminalHandleId: string | null;
	// Which terminal each mounted session is actually showing. The session pane
	// renders one terminal at a time, so opening a shell or the reviewer swaps
	// the agent's terminal off screen even though the route still points at that
	// session. Surfaces outside the session subtree (the notification runtime)
	// need that distinction, and SessionView's own target is local state.
	visibleTerminalKindBySession: Record<string, TerminalTarget["kind"]>;
	setWorkbenchTab: (tab: WorkbenchTab) => void;
	setThemePreference: (theme: ThemePreference) => void;
	setThemeStyle: (style: ThemeStyle) => void;
	setDeveloperMode: (enabled: boolean) => void;
	setRemoteHosts: (enabled: boolean) => void;
	setDiagnostics: (enabled: boolean) => void;
	setSideChats: (enabled: boolean) => void;
	closeSessionImportOffer: () => void;
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
	/** True while the restart-to-update confirmation is open. */
	updateInstallPromptOpen: boolean;
	openUpdateInstallPrompt: () => void;
	closeUpdateInstallPrompt: () => void;
	openGlobalSettings: (section?: GlobalSettingsSection, options?: { focusAgentId?: string; hostId?: string; harnessView?: "local" | "cloud"; startLogin?: boolean; loginAccountId?: string; startPairing?: boolean; showDiagnostics?: HarnessDiagnosticsRequest; preserveProject?: boolean; focusSetting?: string }) => void;
	/** `options` as a string is the owning daemon's host ID (remote projects). */
	openProjectSettings: (projectId: string, options?: string | { section?: ProjectSettingsSection; cloudOrgId?: string }) => void;
	closeSettings: () => void;
	loginReturn: LoginReturn | null;
	/** Remember a chat whose agent refused its login while that login is fixed. */
	startLoginReturn: (intent: Omit<LoginReturn, "loggedInAt" | "settled" | "turnId">) => void;
	/** A login took: return to the waiting chat, if it is that chat's agent, host and account. */
	completeLoginReturn: (agentId: string, hostId?: string, accountId?: string) => void;
	/**
	 * The account the waiting chat's session runs on, once read; only its login
	 * counts. None (no account of its own, or the read failed) lets any login count.
	 */
	setLoginReturnAccount: (sessionId: string, accountId: string | undefined) => void;
	/** The waiting chat saw the login for this refused message and sent nothing again. */
	settleLoginReturn: (sessionId: string, turnId?: string) => void;
	/** Drop the waiting chat, or only when it is this session's. */
	clearLoginReturn: (sessionId?: string) => void;
	/** Refresh resolvedTheme from OS without writing light/dark to storage. */
	syncSystemTheme: () => void;
	toggleSidebar: () => void;
	setInspectorOpen: (sessionId: string, isOpen: boolean) => void;
	toggleInspector: (sessionId: string) => void;
	setInspectorView: (sessionId: string, view: InspectorView) => void;
	/**
	 * Runs the "entering this session" defaults — Summary tab, baseline browser
	 * reveal — exactly once per session's lifetime. Backed by persisted store
	 * state (not a component-local ref) so it stays a no-op across unmount and
	 * remount of the session view, not just across re-renders of one mounted
	 * instance.
	 */
	initializeInspectorSession: (sessionId: string, hasBrowserContent: boolean, hasInspector: boolean) => void;
	setBrowserContentRevealed: (sessionId: string, revealed: boolean) => void;
	setBrowserUnseen: (sessionId: string, unseen: boolean) => void;
	setArtifactsUnseen: (sessionId: string, unseen: boolean) => void;
	setFilesChangedOnly: (sessionId: string, changedOnly: boolean) => void;
	setFilesSelection: (sessionId: string, selection: FilesSelection) => void;
	/** Back to the checked-out branch and the default changes. */
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
const sessionImportOfferClosedStorageKey = "ao.sessionImport.offerClosed";
const splitPanesStorageKey = "ao.splitPanes";
const prViewStorageKey = "ao.prView";
const terminalCopyOnSelectStorageKey = "ao.terminalCopyOnSelect";
const terminalClickModeStorageKey = "ao.terminalClickMode";
const terminalSelectHintShownStorageKey = "ao.terminalSelectHintShown";
const chatSendKeyModeStorageKey = "ao.chatSendKeyMode";
const navigationGesturesStorageKey = "ao.navigationGestures";
const renderMathStorageKey = "ao.renderMath";
const projectAvatarsStorageKey = "ao.projectAvatars";
// The retired Developer Mode opt-out from the old left sidebar. Ignored, and
// cleared at startup so a stored "false" leaves nothing behind.
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
	// On by default: signed-in devices discover account hosts without an extra
	// per-device setup switch.
	return getLocalStorage()?.getItem(remoteHostsStorageKey) !== "false";
}

function initialDiagnostics() {
	return getLocalStorage()?.getItem(diagnosticsStorageKey) === "true";
}

function initialSideChats() {
	return getLocalStorage()?.getItem(sideChatsStorageKey) === "true";
}

function initialSessionImportOfferClosed() {
	try {
		return getLocalStorage()?.getItem(sessionImportOfferClosedStorageKey) === "true";
	} catch {
		return false;
	}
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

/** The waiting chat's login took: mark when, and close global settings back to the chat. */
function loginTaken(state: UiState, waiting: LoginReturn): Partial<UiState> {
	const modal = state.settingsModal;
	// A login only finishes while the Agents page shows it, so closing global
	// settings here returns the user to the chat they came from.
	return {
		loginReturn: { ...waiting, loggedInAt: Date.now() },
		...(modal?.scope === "global" ? { settingsModal: modal.returnTo ?? null } : {}),
	};
}

function inspectorState(sessions: Record<string, InspectorSessionState>, sessionId: string): InspectorSessionState {
	return sessions[sessionId] ?? { isOpen: false, view: "summary" };
}

/** Opening a session keeps the inspector closed until the user (or a browser
 *  reveal) opens it; read every open check through here so that default can't drift. */
export function inspectorIsOpen(sessions: Record<string, InspectorSessionState>, sessionId: string): boolean {
	return sessions[sessionId]?.isOpen ?? false;
}

/**
 * The display mode the user picked for this file in this open request, if any.
 * A newer open request (a different key) chooses its own mode instead.
 */
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

/** The expanded sidebar occupies shell layout; a user close does not. */
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
	sessionImportOfferClosed: initialSessionImportOfferClosed(),
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
			// The window's vibrancy material follows nativeTheme; flip it as the
			// new frame is revealed so the tint and the material change together.
			() => void aoBridge.theme?.set(themePreference)?.catch(() => undefined),
		);
	},
	setThemeStyle: (themeStyle) => {
		if (get().themeStyle === themeStyle) return;
		// Choosing the theme being previewed keeps the preview as-is. A View
		// Transition here would apply it a frame late, after the menu's close
		// has restored the old theme, and flash between the two.
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
	closeSessionImportOffer: () => {
		try {
			getLocalStorage()?.setItem(sessionImportOfferClosedStorageKey, "true");
		} catch {
			// The offer comes back next launch; the agent page still has the import.
		}
		set({ sessionImportOfferClosed: true });
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
		// Settings reports a login once: kept until the account is known, then matched.
		if (waiting.accountPending) return { loginReturn: { ...waiting, heldLogin: accountId ? { accountId } : {} } };
		if (waiting.accountId !== undefined && waiting.accountId !== accountId) return {};
		return loginTaken(state, waiting);
	}),
	setLoginReturnAccount: (sessionId, accountId) => set((state) => {
		if (state.loginReturn?.sessionId !== sessionId || state.loginReturn.loggedInAt !== undefined) return {};
		const { heldLogin, ...waiting } = state.loginReturn;
		const known: LoginReturn = { ...waiting, accountPending: false, ...(accountId ? { accountId } : {}) };
		// A login held while the account was read counts now, by the same rule as any.
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
			// Sessions without an inspector (e.g. orchestrator sessions) must not
			// gain a store entry at all — leave inspectorSessions[sessionId]
			// undefined so callers that key off its presence stay correct.
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
		// Central gate: every New Task entry point (buttons, sidebar menus,
		// shortcuts) funnels through here, so a project whose orchestrator is
		// still provisioning cannot start tasks before it exists.
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
	// Storage can be unavailable (private mode, quota); nothing to clean then.
}

// Hydration synchronizes legacy renderer-only Developer Mode state into the
// main-process updater mirror. Until this completes, the updater is fail-closed.
syncDeveloperModeToUpdater(initialDeveloperModeValue);

export function useResolvedTheme(): Theme {
	return useUiStore((state) => state.resolvedTheme);
}
