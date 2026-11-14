import type { SideCloseRequest, SideCloseState } from "./shared/side-close";
import { SideCloseRecovery, type SideSnapshotCoverage } from "./main/side-close";
import { SideCloseController } from "./main/side-close-controller";
import { SideLaunchRecovery } from "./main/side-launch-recovery";
import { launchTriageInTerminal } from "./main/triage-launch";
import { initializeDesktopLog } from "./main/desktop-log";
import { syncMacWindowButtons } from "./main/window-chrome";
import { MAC_TRAFFIC_LIGHT_ROW_HEIGHT, MAC_WINDOW_BUTTON_X, MAC_WINDOW_BUTTON_RADIUS } from "./shared/window-chrome";
import { finishUpdateQuit } from "./main/update-quit";
import { acknowledgeMacUpdateRestart } from "./main/mac-update-progress";
import { consumeUpdateRelaunchFlag } from "./main/update-relaunch-flag";
import {
	app,
	BaseWindow,
	BrowserWindow,
	clipboard,
	ClipboardItem,
	desktopCapturer,
	dialog,
	ipcMain,
	Menu,
	nativeTheme,
	net,
	nativeImage,
	Notification as ElectronNotification,
	powerMonitor,
	protocol,
	safeStorage,
	screen,
	shell,
	session,
	systemPreferences,
	WebContentsView,
	webContents,
	type WebContents,
	type OpenDialogOptions,
} from "electron";
import {
	setRendererSink,
	startAutoUpdates,
	ensureUpdatePrefs,
	checkForUpdatesNow,
	downloadUpdateNow,
	quitAndInstallUpdate,
	isUpdateRestartRequested,
	clearUpdateRestartRequest,
	setUpdateRestartFailureHandler,
	getUpdateStatus,
	recordUpdateDiagnostic,
	setUpdateSettings,
	setMacDifferentialUpdates,
	returnToHome,
	type UpdateCheckOptions,
} from "./main/auto-updater";
import { listFeatureBuilds, getActiveFeatureBuild } from "./main/feature-builds";
import { clearMainSentryCache, initMainSentry, sanitizeRendererCapture } from "./main/sentry-main";
import { TelemetryPolicyAuthority, resolveDesktopDataDir } from "./main/telemetry-policy-file";
import { DesktopTelemetryController } from "./main/desktop-telemetry-controller";
import { TelemetryIdentityController } from "./main/telemetry-identity";
import { readUpdateSettings, type UpdateSettings, type UpdateStatus } from "./main/update-settings";
import { readKeybindingOverrides, writeKeybindingOverrides } from "./main/keybinding-settings";
import { readEditorSettings, writeEditorPreference } from "./main/editor-settings";
import { createEditorHandoff } from "./main/editor-handoff";
import { discoverMacTerminalApps } from "./main/mac-terminal-apps";
import { registerCliLinkIpc } from "./main/cli-link-ipc";
import { registerCliShadowIpc } from "./main/cli-shadow-ipc";
import { createCliShadowShellPath } from "./main/cli-shadow-shell-path";
import { cliShadowEnabled } from "./shared/cli-shadow";
import { registerDictationIpc } from "./main/dictation";
import { launchCommand } from "./main/launch-command";
import { createLaunchTiming } from "./main/launch-timing";
import { readNativeAppIcon } from "./main/native-app-icon";
import { blocksRenderFrameNavigation, cloudArtifactPrefix } from "./main/render-frame-guard";
import { agentPageRequestAllowed } from "./main/render-frame-network";
import { isAgentPageUrl } from "./shared/agent-page-url";
import {
	decideRelocation,
	inspectInstalledBundle,
	installedBundlePath,
} from "./main/relocation";
import {
	coerceUiSettings,
	DEFAULT_UI_SETTINGS,
	readUiSettings,
	writeUiSettings,
	type UiSettings,
} from "./main/ui-settings";
import { execFile, spawn, type ChildProcess } from "node:child_process";
import { randomBytes, randomUUID } from "node:crypto";
import { closeSync, existsSync, mkdirSync, openSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { chmod, copyFile, mkdir, readFile, realpath, rename, rm, stat, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { promisify } from "node:util";
import { type DaemonLaunchSpec, bundledDaemonIdentityError, resolveDaemonLaunch } from "./shared/daemon-launch";
import { createListenPortScanner, defaultRunFilePath, parseRunFile } from "./shared/daemon-discovery";
import type { DaemonStatus } from "./shared/daemon-status";
import { canonicalPathInside, sameCanonicalPath } from "./shared/path-identity";
import {
	refreshSlowDaemonStartupDetails,
	slowDaemonStartupStatus,
} from "./shared/daemon-startup-status";
import { reloadAppView, toggleAppDevTools } from "./main/app-devtools";
import { acceptLanguages, applyBrowserIdentity, applyUserAgentFallback } from "./main/browser-identity";
import { purgeRootCachesOnce, sweepTemporaryBrowserPartitions } from "./main/browser-storage-cleanup";
import { createUserBrowser } from "./main/user-browser";
import { openChromeExtensionsPage } from "./main/chrome-extensions-page";
import { appShortcutDelivery, attachAppShortcuts, attachShellReloadGuard } from "./main/app-shortcuts";
import { relayScrollPhases } from "./main/scroll-phase";
import {
	KEYBOARD_SHORTCUTS_HELP_CHANNEL,
	OPEN_SETTINGS_SHORTCUT_CHANNEL,
	QUIT_CONFIRM_REQUEST_CHANNEL,
	QUIT_CONFIRM_RESULT_CHANNEL,
	type QuitConfirmRequest,
	SET_CLOSE_SHELL_TERMINAL_SHORTCUT_ENABLED_CHANNEL,
	SET_TERMINAL_FOCUSED_CHANNEL,
	SET_ARTIFACT_ZOOM_ENGAGED_CHANNEL,
	APP_MENU_ACTION_CHANNEL,
	APP_MENU_COMMAND_IDS,
	SET_APP_MENU_AVAILABILITY_CHANNEL,
	effectiveShortcutBindings,
	isAppMenuActionId,
	shortcutBindingAccelerator,
	type AppMenuAvailability,
	type AppShortcutId,
	type KeybindingOverrides,
} from "./shared/shortcuts";
import { OPEN_SESSION_DEEP_LINK_CHANNEL, normalizeSessionDeepLink } from "./shared/deep-links";
import { createTrayController, type TrayController } from "./main/tray";
import { createTrayLifecycle, isTrayEnabled } from "./main/tray-lifecycle";
import { createDesktopQuitController } from "./main/desktop-quit";
import { requestDesktopShutdown } from "./main/desktop-shutdown";
import { createQuitConfirmController } from "./main/quit-confirm";
import {
	TRAY_RENDERER_READY_CHANNEL,
	TRAY_SET_ATTENTION_STATE_CHANNEL,
} from "./shared/tray";
import {
	parseChatDraftBoundaryKinds,
	parseChatDraftDialogCopy,
	type ChatDraftDialogCopy,
	SET_CHAT_DRAFT_RISK_CHANNEL,
	type ChatDraftBoundaryKind,
} from "./shared/chat-draft-risk";
import {
	confirmUnsafeChatDraftLeave,
	shouldPreventUnsafeChatDraftClose,
} from "./main/chat-draft-unload";
import {
	type DaemonProbe,
	expectedDaemonPort,
	parseDaemonProbe,
	resolveDaemonFromPort,
	resolveDaemonFromRunFile,
} from "./shared/daemon-attach";
import {
	browserDaemonOwnershipDecision,
	browserRuntimeRecoveryDecision,
	keepAliveForReplacement,
	portHolderPidToReplace,
	shouldReplaceDaemonForStartup,
} from "./shared/daemon-takeover";
import {
	buildDaemonEnv,
	devDaemonAllowedOrigins,
	resolveShellEnv,
	resolveShellEnvWithSpec,
	resolveWindowsShellProbe,
	type ShellRunner,
} from "./shared/shell-env";
import { DEFAULT_TERMINAL_SHELL, type TerminalShellPreference } from "./shared/ui-locale";
import { bundledTmuxBinaryPath, stableBundledTmuxBinaryPath } from "./shared/bundled-tmux";
import {
	handleCloudDeepLink,
	getCloudAccessToken,
	getCloudSession,
	getCloudSessionCached,
	installCloudIPC,
	registerCloudProtocol,
	resolveCloudAuthDir,
	showCloudSignInFailure,
	protectedStorageAvailable,
	readAuthStore,
} from "./main/cloud-auth";
import { createPendingCloudSignIn } from "./main/pending-cloud-sign-in";
import { installCloudLocalAuthIPC } from "./main/cloud-auth-local";
import { clearShareSnapshots } from "./main/cloud-share";
import { registerCloudShareIpc } from "./main/cloud-share-ipc";
import { ShareLinkRecords } from "./main/cloud-share-links";
import { registerSessionShareUrlsIpc, SessionShareUrls } from "./main/session-share-urls";
import { createCloudAgentProxy } from "./main/cloud-agent-proxy";
import { installCloudCpProxy } from "./main/cloud-cp-proxy";
import { clearFolderImports, isChosenFolder, registerCloudFolderPushIpc, rememberChosenFolder } from "./main/cloud-folder-push";
import { createControlPlaneUrlResolver } from "./main/cloud-control-plane-url";
import { DEFAULT_POSTHOG_HOST, DEFAULT_POSTHOG_PROJECT_KEY } from "./shared/posthog-config";
import { daemonSentryDsn } from "./shared/sentry-config";
import { boundCloudUserId, buildTelemetryBootstrap, claimFirstOpen, loadOrAssignExperimentCohort, readTelemetryInstallContext, rendererTelemetryEnabled } from "./shared/telemetry";
import { detectInstallSource } from "./shared/install-environment";
import {
	TELEMETRY_CLEAR_RENDERER_QUEUES_CHANNEL,
	TELEMETRY_POLICY_CHANGED_CHANNEL,
	TELEMETRY_RENDERER_QUEUES_CLEARED_CHANNEL,
	telemetryPolicyRetryable,
	type RendererTelemetryCapture,
	type TelemetryPolicyView,
} from "./shared/telemetry-policy";
import {
	createBrowserViewHost,
	shouldHandleAppShortcutInBrowserContext,
	type BrowserViewHost,
	type BrowserRuntimeState,
} from "./main/browser-view-host";
import { createBrowserCloseDiagnostics } from "./main/browser-close-diagnostics";
import { repairBrowserProfileCookies } from "./main/browser-cookie-repair";
import { browserProfilePartition } from "./shared/browser-profiles";
import { createBrowserProfileStore } from "./main/browser-profile-store";
import { BrowserHistoryStore } from "./main/browser-history-store";
import { createAwakeTrayMonitor } from "./main/awake-tray";
import { createAwakeDownloadBridge } from "./main/awake-download-bridge";
import { createBrowserDownloadManager } from "./main/browser-download-manager";
import { registerAgentLoginIpc } from "./main/agent-login-ipc";
import { registerChatImageIpc } from "./main/chat-image-ipc";
import { createArtifactSnapshotStore, registerArtifactSnapshotIpc, registerArtifactSnapshotProtocol } from "./main/artifact-snapshots";
import { ARTIFACT_SNAPSHOT_SCHEME } from "./shared/artifact-snapshot";
import { createArtifactSnapshotRenderer } from "./main/artifact-snapshot-window";
import { pruneCloudArtifactFolders, registerCloudArtifactIpc } from "./main/cloud-artifact-ipc";
import { BrowserProfileImportService } from "./main/browser-profile-import";
import {
	registerBrowserProfileIpc,
	type BrowserProfileIpc,
	type BrowserProfileMenuItem,
} from "./main/browser-profile-ipc";
import type { BrowserProfileMenuInput } from "./shared/browser-profiles";
import { createWindowComposition, type WindowComposition } from "./main/window-composition";
import { AgentBrowserRuntime } from "./main/agent-browser-runtime";
import { sameBrowserRuntimeIdentity, type BrowserRuntimeIdentity } from "./main/browser-runtime-identity";
import { connectSupervisor, type SupervisorLinkHandle } from "./main/supervisor-link";
import {
	BROWSER_RUNTIME_DISCONNECTED,
	connectBrowserRuntime,
	type BrowserRuntimeLinkHandle,
} from "./main/browser-runtime-link";
import { resolveBrowserRuntimeToken } from "./main/browser-runtime-token";
import { checkRender, measureRender } from "./main/render-check";
import {
	connectBrowserStream,
	type BrowserStreamControl,
	type BrowserStreamLinkHandle,
} from "./main/browser-stream-link";
import type {
	BrowserRemoteInput,
	BrowserRemoteNavigation,
	BrowserRemoteTabAction,
} from "./main/browser-live-types";
import { keepDaemonAlive, shouldLinkOnAttach } from "./main/daemon-owner";
import { planDaemonScope, raiseOomScoreAdj, type DaemonScopePlan } from "./main/linux-daemon-scope";
import { createLifecycleLog, linuxProcessContext, markRunStarted, observeUnhandledRejections, type LifecycleRecord } from "./main/lifecycle-log";
import { planDaemonAutoRestart, daemonExitWasUngraceful } from "./main/daemon-auto-restart";
import { readProcessStartMs } from "./main/process-start";
import { claimFirstRunExperimentExposure, markFirstRunProjectPromptSeen, readFirstRunProjectPrompt, readMigrationState, updateMigration, writeAppStateMarker, type MigrationState } from "./main/app-state";
import { isAllowedAppExternalURL, openAllowedAppExternalURL } from "./main/external-open";
import { coercePrivacyGuideRequest, CUA_DRIVER_APP_PATH, isPrivacyGuideTarget, PRIVACY_GUIDE_HEIGHT, PrivacyGuide, type PrivacyGuideApp, type PrivacyPane } from "./main/privacy-guide";
import { appBundlePath, readAppPrivacy, requestAppPrivacy, type AppPrivacyGrants } from "./main/app-privacy";
import { watchSettingsWindow } from "./main/settings-window-watch";
import {
	dockBounceType,
	shouldReplaceBounce,
	shouldSignalAttention,
	shouldToast,
	toastSilent,
} from "./main/notification-signals";
import { buildLinuxAppMenuTemplate, buildMacAppMenuTemplate, buildWindowsAppMenuTemplate, coerceAppMenuAvailability, type AppMenuCommands } from "./main/menu";
import { ancestorRepositorySetupWarning, resolveCheckedOutBranch, scanImportFolder } from "./main/import-folder-scan";
import { parseOpenFolderPathArg } from "./main/open-folder-arg";
import { registerRemotesIpc, remotesFilePath } from "./main/remotes-main";
import { sealSavedPasswords, setPasswordCipher } from "./main/remotes-store";
import { createLocalHostIdentity } from "./main/remote-request";
import { RemoteRegistry } from "./main/remote-registry";
import { startRemoteProxy } from "./main/remote-proxy";
import { captureMainError, reportDaemonFailure, ExpectedIpcError, installIpcErrorCapture, setMainErrorSink } from "./main/main-error-capture";

installIpcErrorCapture(ipcMain);

declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;

const execFileAsync = promisify(execFile);

const ignoreStdStreamError = (err: NodeJS.ErrnoException): void => {
	if (err.code === "EPIPE") return;
};
process.stdout.on("error", ignoreStdStreamError);
process.stderr.on("error", ignoreStdStreamError);

const APP_DISPLAY_NAME = "Orchestrator.inc";

app.setName(app.isPackaged ? APP_DISPLAY_NAME : `${APP_DISPLAY_NAME} (dev)`);

if (!app.isPackaged && process.platform === "darwin") {
	app.commandLine.appendSwitch("use-mock-keychain");
}

if (process.platform === "win32") {
	app.setAppUserModelId("dev.agent-orchestrator.desktop");
}

const disableGpu = process.env.AO_DISABLE_GPU?.trim().toLowerCase();
if (disableGpu === "1" || disableGpu === "true" || disableGpu === "yes" || disableGpu === "on") {
	app.disableHardwareAcceleration();
}

app.setPath(
	"userData",
	app.isPackaged
		? path.join(os.homedir(), ".ao", "electron")
		: (process.env.AO_DEV_ELECTRON_DIR ?? path.join(os.homedir(), ".ao", "dev", "electron")),
);

const desktopLaunchWorkingDirectory = process.cwd();
const launchTiming = createLaunchTiming(process.getCreationTime() ?? performance.timeOrigin);
const desktopDataDir = resolveDesktopDataDir(process.env, os.homedir(), desktopLaunchWorkingDirectory, app.isPackaged);
initializeDesktopLog(desktopDataDir);
let telemetryPolicyController: DesktopTelemetryController | null = null;
const ANALYTICS_OPT_OUT_CHANGED_CHANNEL = "telemetry:analyticsOptOutChanged";
const telemetryIdentity = new TelemetryIdentityController(
	desktopDataDir,
	() => (daemonStatus.state === "ready" && daemonStatus.port ? `http://127.0.0.1:${daemonStatus.port}` : null),
	(url, init) => fetch(url, init),
	(optedOut) => {
		for (const shellContents of trustedShellWebContents.values()) if (!shellContents.isDestroyed()) shellContents.send(ANALYTICS_OPT_OUT_CHANGED_CHANNEL, optedOut);
	},
);
const trustedShellWebContents = new Map<number, WebContents>();
const pendingRendererQueuePurges = new Map<string, {
	senderId: number;
	contents: WebContents;
	resolve: () => void;
	reject: (error: Error) => void;
	timeout: ReturnType<typeof setTimeout>;
	onDestroyed: () => void;
}>();

function finishRendererQueuePurge(requestId: string, error?: Error): void {
	const pending = pendingRendererQueuePurges.get(requestId);
	if (!pending) return;
	pendingRendererQueuePurges.delete(requestId);
	clearTimeout(pending.timeout);
	pending.contents.removeListener("destroyed", pending.onDestroyed);
	if (error) pending.reject(error);
	else pending.resolve();
}

function requestRendererQueuePurge(contents: WebContents): Promise<void> {
	return new Promise((resolve, reject) => {
		const requestId = randomUUID();
		const onDestroyed = () => finishRendererQueuePurge(requestId, new Error("renderer destroyed before telemetry queue purge"));
		const timeout = setTimeout(() => finishRendererQueuePurge(requestId, new Error("renderer telemetry queue purge timed out")), 5_000);
		pendingRendererQueuePurges.set(requestId, { senderId: contents.id, contents, resolve, reject, timeout, onDestroyed });
		contents.once("destroyed", onDestroyed);
		try { contents.send(TELEMETRY_CLEAR_RENDERER_QUEUES_CHANNEL, { requestId }); }
		catch (error) { finishRendererQueuePurge(requestId, error instanceof Error ? error : new Error("renderer telemetry queue purge failed")); }
	});
}

async function clearRendererTelemetryQueues(): Promise<void> {
	const liveShells = [...trustedShellWebContents.values()].filter((contents) => !contents.isDestroyed());
	await Promise.all(liveShells.map(requestRendererQueuePurge));
}

let mainWindow: BaseWindow | null = null;
let trayController: TrayController | null = null;
const trayLifecycle = createTrayLifecycle({
	getWindow: () => null,
	getContents: () => getShellWebContents(),
	getTrayController: () => trayController,
	focusWindow: () => focusMainWindow(),
});
const OPEN_FOLDER_PATH_CHANNEL = "app:openFolderPath";
let pendingFolderPath: string | null = null;
let pendingSessionDeepLink: string | null = null;
let pendingBillingSection: string | null = null;
let daemonProcess: ChildProcess | null = null;
let lifecycleLog: LifecycleRecord = () => undefined;
let daemonStoppingProcess: ChildProcess | null = null;
let daemonRestartAfterExitProcess: ChildProcess | null = null;
type DaemonAutoRestartIntent = { generation: number; crashedPid: number; runFilePresent: boolean; keepAlive: boolean };
type DaemonStartTrigger = "startup" | "renderer_status" | "manual_start" | "manual_restart" | "browser_runtime_recovery" | "auto_restart" | "scope_fallback";
let daemonAutoRestartExits: number[] = [];
let daemonAutoRestartTimer: ReturnType<typeof setTimeout> | null = null;
let daemonAutoRestartGeneration = 0;
let daemonAutoRestartStandDown = false;
let daemonStartPromise: Promise<DaemonStatus> | null = null;
let daemonStartAutoRestartIntent: DaemonAutoRestartIntent | undefined;
let daemonStartEpoch = 0;
let daemonStatus: DaemonStatus = { state: "stopped" };
const awakeDownloads = createAwakeDownloadBridge({
 baseURL: () => daemonStatus.state === "ready" && daemonStatus.port ? `http://127.0.0.1:${daemonStatus.port}` : null,
 request: (url, init) => fetch(url, init),
});
app.once("will-quit", () => awakeDownloads.dispose());

let daemonAttachmentGeneration = 0;
let daemonOutput = "";
let browserViewHost: BrowserViewHost | null = null;
let browserProfileIpc: BrowserProfileIpc | null = null;
let browserProfileImporter: BrowserProfileImportService | null = null;
let windowComposition: WindowComposition | null = null;
const browserCleanupPromises = new Set<Promise<void>>();
let browserQuitCleanupPromise: Promise<void> | null = null;
let browserCleanupComplete = false;
let browserQuitRequested = false;
let preserveAgentsOnQuit = false;
let agentQuitCleanupComplete = false;
let agentQuitCleanupPromise: Promise<void> | null = null;
let desktopAgentShutdownAccepted = false;
let createWindowPromise: Promise<void> | null = null;
const desktopQuit = createDesktopQuitController({
	platform: process.platform,
	hasTray: () => trayController !== null && !browserQuitRequested,
	isUpdateRestartRequested,
	hasWindow: () => mainWindow !== null || createWindowPromise !== null,
	closeWindow: () => {
		if (mainWindow) mainWindow.close();
		else if (createWindowPromise) {
			void createWindowPromise
				.catch((error) => console.error("failed to close pending main window:", error))
				.then(() => {
					if (mainWindow) mainWindow.close();
					else desktopQuit.windowClosed();
				});
		}
	},
	hideDock: () => app.dock?.hide(),
	quit: () => app.quit(),
});
const quitConfirm = createQuitConfirmController({
	canConfirm: () => {
		const shell = getShellWebContents();
		return Boolean(mainWindow?.isVisible() && !mainWindow.isMinimized() && shell && !shell.isDestroyed());
	},
	requestConfirm: () => {
		const shell = getShellWebContents();
		shell?.focus();
		const request: QuitConfirmRequest = {
			keepsRunning: trayController !== null && !browserQuitRequested && !isUpdateRestartRequested(),
		};
		shell?.send(QUIT_CONFIRM_REQUEST_CHANNEL, request);
	},
	quit: () => app.quit(),
});
let browserRuntimeLink: BrowserRuntimeLinkHandle | null = null;
let browserRuntimeLinkIdentity: BrowserRuntimeIdentity | null = null;
let browserRuntimeRecoveryAttempted = false;
let forceReplaceAdoptedRequested = false;
let browserStreamLink: BrowserStreamLinkHandle | null = null;
let browserStreamLinkIdentity: BrowserRuntimeIdentity | null = null;
let keybindingOverrides: KeybindingOverrides = {};
let keybindingRecordingActive = false;
let appMenuAvailability: AppMenuAvailability = {};
let closeShellTerminalShortcutEnabled = false;
let terminalFocused = false;
let terminalFontSizeFocused = false;
let artifactZoomEngaged = false;
let chatDraftRisks: ChatDraftBoundaryKind[] = [];
let chatDraftDialog: ChatDraftDialogCopy | undefined;
let chatDraftQuitConfirmed = false;
let chatDraftWindowCloseConfirmed = false;
let supervisorLink: SupervisorLinkHandle | null = null;
let isFlashing = false;
let pendingBounce: { id: number; critical: boolean } | null = null;
let soundNotificationsEnabled = DEFAULT_UI_SETTINGS.soundNotificationsEnabled;

function playNotificationSound(): void {
	const shellContents = getShellWebContents();
	if (shellContents && !shellContents.isDestroyed()) {
		shellContents.send("notifications:playSound");
		return;
	}
	shell.beep();
}

const isDev = !app.isPackaged;

const DEV_DAEMON_PORT = 3002;
const DEV_STATE_SUBDIR = "dev";

const RENDERER_SCHEME = "app";
const RENDERER_HOST = "renderer";
const RENDERER_ORIGIN = `${RENDERER_SCHEME}://${RENDERER_HOST}`;
const NATIVE_WINDOW_BACKGROUND_DARK = "#0c0c0e";
const NATIVE_WINDOW_BACKGROUND_LIGHT = "#fbfbfb";

function getShellWebContents(): WebContents | null {
	return windowComposition?.shellWebContents ?? null;
}

let rendererWindowBackground: string | null = null;
let windowTranslucent = false;
const appliedNativeBackground = new WeakMap<BaseWindow, { vibrancy: string | null; color: string }>();

function syncNativeWindowBackground(force = false): void {
	if (!windowComposition || !mainWindow || mainWindow.isDestroyed()) return;
	const vibrancy = process.platform === "darwin" && windowTranslucent ? "under-window" : null;
	const color = windowTranslucent
		? "#00000000"
		: rendererWindowBackground ??
			(nativeTheme.shouldUseDarkColors ? NATIVE_WINDOW_BACKGROUND_DARK : NATIVE_WINDOW_BACKGROUND_LIGHT);
	const applied = appliedNativeBackground.get(mainWindow);
	if (process.platform === "darwin" && (force || applied?.vibrancy !== vibrancy)) {
		mainWindow.setVibrancy(vibrancy);
	}
	if (force || applied?.color !== color) mainWindow.setBackgroundColor(color);
	appliedNativeBackground.set(mainWindow, { vibrancy, color });
}

function resolvedDaemonDataDir(): string {
	const override = process.env.AO_DATA_DIR?.trim();
	if (override) return override;
	if (isDev) return path.join(os.homedir(), ".ao", DEV_STATE_SUBDIR, "data");
	return path.join(os.homedir(), ".ao", "data");
}

function persistTerminalThemeHint(scheme: "light" | "dark"): void {
	const dir = resolvedDaemonDataDir();
	try {
		mkdirSync(dir, { recursive: true, mode: 0o750 });
		const target = path.join(dir, "terminal-theme");
		const temporary = `${target}.${process.pid}.tmp`;
		writeFileSync(temporary, `${scheme}\n`, { mode: 0o600 });
		renameSync(temporary, target);
	} catch (error) {
		console.warn("AO: unable to persist terminal theme hint", error);
	}
}

nativeTheme.on("updated", () => syncNativeWindowBackground());

protocol.registerSchemesAsPrivileged([
	{
		scheme: RENDERER_SCHEME,
		privileges: { standard: true, secure: true, supportFetchAPI: true, codeCache: true },
	},
	{ scheme: ARTIFACT_SNAPSHOT_SCHEME, privileges: { standard: true, secure: true } },
]);

if (!app.requestSingleInstanceLock()) {
	app.exit(0);
} else {
	registerCloudProtocol();
	installLifecycleLog();
}

function installLifecycleLog(): void {
	const runFile = runFilePath();
	const directory = path.join(runFile ? path.dirname(runFile) : desktopDataDir, "logs");
	const record = createLifecycleLog({ directory, version: app.getVersion(), homeDir: os.homedir() });
	lifecycleLog = record;
	const clearRunMarker = markRunStarted({ directory, record, version: app.getVersion(), processAlive });
	record("app_start", {
		ppid: process.ppid,
		platform: process.platform,
		arch: process.arch,
		packaged: app.isPackaged,
		electron: process.versions.electron,
		appImage: Boolean(process.env.APPIMAGE),
		sessionType: process.env.XDG_SESSION_TYPE,
		...(process.platform === "linux" ? linuxProcessContext() : {}),
	});
	process.on("uncaughtExceptionMonitor", (error, origin) => record("uncaught_exception", { origin }, error));
	observeUnhandledRejections(process as unknown as { emit: (event: string | symbol, ...args: unknown[]) => boolean }, (reason) => record("unhandled_rejection", {}, reason));
	app.on("render-process-gone", (_event, contents, details) => {
		record("render_process_gone", { reason: details.reason, exitCode: details.exitCode, contentsType: contents.getType() });
	});
	app.on("child-process-gone", (_event, details) => {
		record("child_process_gone", {
			type: details.type,
			reason: details.reason,
			exitCode: details.exitCode,
			serviceName: details.serviceName,
			name: details.name,
		});
	});
	app.on("window-all-closed", () => record("window_all_closed"));
	app.on("before-quit", () => record("before_quit"));
	app.on("will-quit", () => record("will_quit"));
	app.on("quit", (_event, exitCode) => record("quit", { exitCode }));
	process.on("exit", (code) => {
		record("process_exit", { code, daemonPid: daemonProcess?.pid ?? null });
		clearRunMarker();
	});
}

function registerRendererProtocol(): void {
	const distRoot = path.join(__dirname, `../renderer/${MAIN_WINDOW_VITE_NAME}`);
	protocol.handle(RENDERER_SCHEME, async (request) => {
		const url = new URL(request.url);
		if (url.host !== RENDERER_HOST) {
			return new Response("Not found", { status: 404 });
		}
		const resolved = path.resolve(path.join(distRoot, decodeURIComponent(url.pathname)));
		if (resolved !== distRoot && !resolved.startsWith(distRoot + path.sep)) {
			return new Response("Forbidden", { status: 403 });
		}
		const target = path.extname(resolved) === "" ? path.join(distRoot, "index.html") : resolved;
		try {
			return await net.fetch(pathToFileURL(target).toString());
		} catch {
			return new Response("Not found", { status: 404 });
		}
	});
}

function rendererUrl(): string {
	if (typeof MAIN_WINDOW_VITE_DEV_SERVER_URL !== "undefined" && MAIN_WINDOW_VITE_DEV_SERVER_URL) {
		return MAIN_WINDOW_VITE_DEV_SERVER_URL;
	}

	return `${RENDERER_ORIGIN}/index.html`;
}

function preloadPath(): string {
	return path.join(__dirname, "preload.js");
}

function annotatePreloadPath(): string {
	return path.join(__dirname, "annotate-preload.js");
}

function privacyGuidePreloadPath(): string {
	return path.join(__dirname, "privacy-guide-preload.js");
}

function windowIconPath(): string | undefined {
	if (!app.isPackaged) return path.join(__dirname, "../../assets/icon-dev.png");
	const iconFile = process.platform === "win32" ? "icon.ico" : "icon.png";
	const candidate = path.join(process.resourcesPath, iconFile);
	if (existsSync(candidate)) return candidate;
	const fallback = path.join(process.resourcesPath, "icon.png");
	return existsSync(fallback) ? fallback : undefined;
}

function writeClipboardImage(image: Electron.NativeImage): Promise<void> {
	return clipboard.write([
		new ClipboardItem({ "image/png": new Blob([new Uint8Array(image.toPNG())], { type: "image/png" }) }),
	]);
}

function applyRuntimeAppIcon(): void {
	if (process.platform !== "darwin" || app.isPackaged) return;
	const iconPath = windowIconPath();
	if (!iconPath) return;
	const icon = nativeImage.createFromPath(iconPath);
	if (!icon.isEmpty()) {
		app.dock?.setIcon(icon);
	}
}

const WINDOW_SHOW_FALLBACK_MS = 2_500;

function showWhenRendererLoaded(window: BaseWindow, contents: WebContents): void {
	let shown = false;
	const show = () => {
		if (shown) return;
		shown = true;
		clearTimeout(fallback);
		if (window.isDestroyed() || window.isVisible()) return;
		window.show();
	};
	const fallback = setTimeout(show, WINDOW_SHOW_FALLBACK_MS);
	contents.once("did-finish-load", show);
	contents.once("did-fail-load", show);
	contents.on("did-fail-load", (_event, errorCode, _description, _url, isMainFrame) => {
		if (isMainFrame && errorCode !== -3) captureMainError({ category: "window", reason: "did_fail_load", detail: `code:${errorCode}` });
	});
	contents.on("unresponsive", () => captureMainError({ category: "window", reason: "unresponsive" }));
}

function focusMainWindow(): void {
	if (!mainWindow) {
		createWindow();
		return;
	}
	if (mainWindow.isMinimized()) mainWindow.restore();
	mainWindow.show();
	mainWindow.focus();
}

let sideLaunchState: { appRunId: string; sides: unknown[]; numbers?: Record<string, number> } | undefined;
const SIDE_STATE_BACKSTOP_MS = 5_000;
let sideStateTimer: ReturnType<typeof setInterval> | undefined;
const sideCloseRecovery = new SideCloseRecovery();
const sideCloseController = new SideCloseController({
	ledger: sideCloseRecovery,
	attachment: () => sideLaunchRecovery.attachment,
	ensureClaimed: () => sideLaunchRecovery.ensureClaimed(),
	capture: () => sideLaunchRecovery.captureForClose(),
	onOwnershipChange: () => { sideLaunchState = sideCloseRecovery.state; },
	onChange: (state: SideCloseState) => { getShellWebContents()?.send("sideChats:closeStateChanged", state); },
});
function rememberSideLaunchState(state: { appRunId: string; sides: unknown[]; numbers?: Record<string, number> }, coverage: SideSnapshotCoverage): void {
	sideCloseRecovery.remember(state, coverage);
	sideLaunchState = sideCloseRecovery.state;
	sideCloseController.reconcile(coverage);
}

function armSideStateBackstop(): void {
	if (sideStateTimer) return;
	sideStateTimer = setInterval(() => {
		if (sideLaunchRecovery.status === "ready" || sideCloseController.hasPending()) sideLaunchRecovery.captureCloseInBackground();
	}, SIDE_STATE_BACKSTOP_MS);
}

const sideLaunchRecovery = new SideLaunchRecovery({
	snapshot: () => sideLaunchState,
	onRestore: (snapshot) => sideCloseRecovery.markRestoreAttempt(snapshot),
	onRestoreAccepted: (snapshot) => { sideCloseRecovery.markRestoreAccepted(snapshot); sideLaunchState = sideCloseRecovery.state; sideCloseController.reconcile(); },
	onSnapshot: rememberSideLaunchState,
	onError: error => console.warn("AO: side chat recovery or capture failed", error),
	onClaimed: () => { sideCloseController.setRecovery("restoring"); armSideStateBackstop(); },
	onReady: () => {
		sideCloseController.setRecovery("ready");
		sideLaunchRecovery.captureCloseInBackground();
		armSideStateBackstop();
	},
});
app.once("will-quit", () => {
	sideLaunchRecovery.setAttachment(undefined);
	sideCloseController.setAttachment(undefined, "unavailable");
	if (sideStateTimer) clearInterval(sideStateTimer);
	sideStateTimer = undefined;
});

function setDaemonStatus(nextStatus: DaemonStatus): void {
	reportDaemonFailure(daemonStatus, nextStatus);
	if (nextStatus.state !== "ready" || daemonStatus.state !== "ready" || nextStatus.port !== daemonStatus.port || nextStatus.pid !== daemonStatus.pid) daemonAttachmentGeneration++;
	if (nextStatus.state !== "ready") disposeBrowserRuntimeLink();
	if (nextStatus.state !== "ready") {
		if (sideStateTimer) clearInterval(sideStateTimer);
		sideStateTimer = undefined;
		disposeBrowserStreamLink();
	}
	daemonStatus = nextStatus;
	if (nextStatus.state === "ready") launchTiming.mark("daemonReady");
	if (nextStatus.state === "ready") void telemetryIdentity.flush();
	else telemetryIdentity.invalidate();
	getShellWebContents()?.send("daemon:status", daemonStatus);
	if (nextStatus.state === "ready" && browserViewHost) {
		establishBrowserRuntimeLink();
		establishBrowserStreamLink();
	}
	const daemonIdentity = nextStatus.pid ?? daemonProcess?.pid;
	if (sideLaunchRecovery.setAttachment(nextStatus.state === "ready" && nextStatus.port && daemonIdentity
		? { generation: daemonAttachmentGeneration, pid: daemonIdentity, port: nextStatus.port, appRunId }
		: undefined)) {
		if (sideStateTimer) clearInterval(sideStateTimer);
		sideStateTimer = undefined;
	}
	sideCloseController.setAttachment(sideLaunchRecovery.attachment, sideLaunchRecovery.status);
}

const MAX_DAEMON_OUTPUT_CHARS = 12_000;

function appendDaemonOutput(text: string): void {
	daemonOutput = (daemonOutput + text).slice(-MAX_DAEMON_OUTPUT_CHARS);
	const nextStatus = refreshSlowDaemonStartupDetails(daemonStatus, daemonOutput);
	if (nextStatus !== daemonStatus) setDaemonStatus(nextStatus);
}

function appMenuOptions() {
	return {
		allowShellDebugging: isDev,
		onReload: (ignoreCache: boolean) => reloadAppView(browserViewHost, getShellWebContents, isDev, ignoreCache),
	};
}

function menuAcceleratorMayRun(id: AppShortcutId): boolean {
	if (keybindingRecordingActive) return false;
	if (!browserViewHost?.isLastUsedBrowser()) return true;
	const binding = effectiveShortcutBindings(id, true, keybindingOverrides)[0];
	return !binding || shouldHandleAppShortcutInBrowserContext(id, binding, true);
}

const appMenuCommands: AppMenuCommands = {
	accelerator: (id) => {
		if (isAppMenuActionId(id)) return undefined;
		const binding = effectiveShortcutBindings(id, true, keybindingOverrides)[0];
		return binding ? shortcutBindingAccelerator(binding) : undefined;
	},
	isEnabled: (id) => appMenuAvailability[id] === true,
	run: (id, triggeredByAccelerator) => {
		const shell = getShellWebContents();
		if (!shell || shell.isDestroyed() || appMenuAvailability[id] !== true) return;
		if (isAppMenuActionId(id)) {
			shell.send(APP_MENU_ACTION_CHANNEL, id);
			return;
		}
		if (triggeredByAccelerator && !menuAcceleratorMayRun(id)) return;
		const { channel, payload } = appShortcutDelivery(id);
		shell.send(channel, ...payload);
	},
};

function applyAppMenuAvailability(next: AppMenuAvailability): void {
	appMenuAvailability = next;
	const menu = Menu.getApplicationMenu();
	for (const id of APP_MENU_COMMAND_IDS) {
		const item = menu?.getMenuItemById(id);
		if (item) item.enabled = next[id] === true;
	}
}

function installMacAppMenu(): void {
	Menu.setApplicationMenu(
		Menu.buildFromTemplate(
			buildMacAppMenuTemplate(() => {
				void toggleAppDevTools(browserViewHost, getShellWebContents, isDev);
			}, { appName: app.name, onQuit: quitConfirm.menuQuit }, { ...appMenuOptions(), commands: appMenuCommands }),
		),
	);
}

function buildWindowsAppMenu(): Menu {
	return Menu.buildFromTemplate(
		buildWindowsAppMenuTemplate(() => {
			void toggleAppDevTools(browserViewHost, getShellWebContents, isDev);
		}, appMenuOptions()),
	);
}

function buildLinuxAppMenu(): Menu {
	return Menu.buildFromTemplate(
		buildLinuxAppMenuTemplate(() => {
			void toggleAppDevTools(browserViewHost, getShellWebContents, isDev);
		}, appMenuOptions()),
	);
}

async function disposeBrowserViewHost(): Promise<void> {
	const host = browserViewHost;
	browserViewHost = null;
	const profileIpc = browserProfileIpc;
	browserProfileIpc = null;
	profileIpc?.dispose();
	const profileImporter = browserProfileImporter;
	browserProfileImporter = null;
	if (!host && !profileImporter) return;
	const cleanup = Promise.resolve()
		.then(async () => {
			await profileImporter?.dispose();
			await host?.dispose();
		})
		.catch((error) => {
			console.error("browser host cleanup failed:", error);
		});
	browserCleanupPromises.add(cleanup);
	void cleanup.then(
		() => browserCleanupPromises.delete(cleanup),
		() => browserCleanupPromises.delete(cleanup),
	);
	await cleanup;
}

function browserProfileStateDir(): string {
	const runFile = runFilePath();
	return path.dirname(runFile ?? path.join(os.homedir(), ".ao", "running.json"));
}

const userBrowser = createUserBrowser({
	platform: process.platform,
	openExternal: (url) => shell.openExternal(url),
	defaultBrowserName: () => app.getApplicationNameForProtocol("https://"),
});

async function clearElectronBrowserProfileData(partition: string): Promise<void> {
	const browserSession = session.fromPartition(partition);
	await Promise.all([browserSession.clearStorageData(), browserSession.clearCache(), browserSession.clearCodeCaches({})]);
}

async function disposeAllBrowserViewHosts(): Promise<void> {
	for (;;) {
		if (browserViewHost) await disposeBrowserViewHost();
		const pending = [...browserCleanupPromises];
		if (pending.length === 0 && !browserViewHost) return;
		if (pending.length > 0) await Promise.all(pending);
	}
}

async function createWindowInternal(): Promise<void> {
	await disposeAllBrowserViewHosts();
	const agentBrowserRuntime = new AgentBrowserRuntime({
		binaryPath: resolveAgentBrowserBinaryPath(),
		dataDir: path.join(os.homedir(), ".ao", ...(app.isPackaged ? ["br"] : ["dev", "br"])),
		log: (message) => console.log(`AO: ${message}`),
	});
	await agentBrowserRuntime.prepare();
	if (browserQuitRequested) {
		await agentBrowserRuntime.dispose();
		return;
	}
	const browserProfileStore = await createBrowserProfileStore({ stateDir: browserProfileStateDir() });
	void repairBrowserProfileCookies({
		stateDir: browserProfileStateDir(),
		profileIds: browserProfileStore.profiles.map((profile) => profile.id),
		fromProfile: (profileId) => session.fromPartition(browserProfilePartition(profileId)),
		cookieDatabase: (profileId) =>
			path.join(app.getPath("userData"), "Partitions", browserProfilePartition(profileId).replace(/^persist:/, ""), "Cookies"),
		log: (message) => console.info(message),
	});
	const browserHistoryStore = new BrowserHistoryStore({ stateDir: browserProfileStateDir() });
	const profileImporter = new BrowserProfileImportService({
		stateDir: browserProfileStateDir(),
		profileStore: browserProfileStore,
		historyStore: browserHistoryStore,
		fromPartition: (partition) => session.fromPartition(partition),
	});
	await profileImporter.initialize();
	const windowOptions: Electron.BaseWindowConstructorOptions = {
		width: 1320,
		height: 860,
		minWidth: 960,
		minHeight: 640,
		title: app.isPackaged ? APP_DISPLAY_NAME : `${APP_DISPLAY_NAME} (dev)`,
		icon: windowIconPath(),
		backgroundColor: NATIVE_WINDOW_BACKGROUND_DARK,
		show: false,
		...(process.platform === "win32"
			? {
					titleBarStyle: "hidden" as const,
					autoHideMenuBar: true,
				}
			: process.platform === "linux"
				? {
						autoHideMenuBar: true,
					}
				: {
						titleBarStyle: "hiddenInset" as const,
						transparent: true,
						visualEffectState: "active" as const,
						trafficLightPosition: { x: MAC_WINDOW_BUTTON_X, y: MAC_TRAFFIC_LIGHT_ROW_HEIGHT / 2 - MAC_WINDOW_BUTTON_RADIUS },
					}),
	};
	mainWindow = new BaseWindow(windowOptions);
	const composition = createWindowComposition({
		mainWindow,
		WebContentsView,
		preload: preloadPath(),
		platform: process.platform,
		allowShellDebugging: isDev,
		hasLivePageBelow: () => browserViewHost?.hasVisiblePanel() ?? false,
	});
	windowComposition = composition;
	syncNativeWindowBackground(true);
	const shellWebContents = getShellWebContents();
	if (!shellWebContents) throw new Error("Orchestrator shell WebContents was not created");
	trustedShellWebContents.set(shellWebContents.id, shellWebContents);
	relayScrollPhases(shellWebContents);
	shellWebContents.once("destroyed", () => {
		trustedShellWebContents.delete(shellWebContents.id);
		quitConfirm.reset();
		applyAppMenuAvailability({});
	});
	shellWebContents.on("did-start-loading", () => quitConfirm.reset());
	shellWebContents.on("did-navigate", () => applyAppMenuAvailability({}));

	if (process.platform === "win32") {
		Menu.setApplicationMenu(buildWindowsAppMenu());
		mainWindow.setMenuBarVisibility(false);
	} else if (process.platform === "darwin") {
		installMacAppMenu();
	} else if (process.platform === "linux") {
		Menu.setApplicationMenu(buildLinuxAppMenu());
		mainWindow.setMenuBarVisibility(false);
	}

	shellWebContents.setWindowOpenHandler(({ url }) => {
		if (isAllowedAppExternalURL(url)) {
			void shell.openExternal(url);
		}
		return { action: "deny" };
	});

	shellWebContents.on("will-navigate", (event, url) => {
		if (url !== shellWebContents.getURL()) {
			event.preventDefault();
		}
	});

	shellWebContents.on("will-frame-navigate", (event) => {
		if (event.isMainFrame || !event.frame) return;
		const app = shellWebContents.mainFrame;
		if (event.initiator?.processId === app.processId && event.initiator.routingId === app.routingId) return;
		if (blocksRenderFrameNavigation(event.frame.url, event.url)) event.preventDefault();
	});

	shellWebContents.on("did-frame-navigate", (_event, url, httpResponseCode, _statusText, isMainFrame) => {
		if (isMainFrame) return;
		try {
			if (cloudArtifactPrefix(new URL(url)) === undefined) return;
		} catch {
			return;
		}
		shellWebContents.send("cloudArtifact:frameNavigated", { url, status: httpResponseCode });
	});

	const blockedPageHosts = new Set<string>();
	shellWebContents.session.webRequest.onBeforeRequest((details, callback) => {
		if (details.resourceType === "subFrame" && isAgentPageUrl(details.url)) return callback({});
		let pageUrl: string | undefined;
		try {
			for (let frame = details.frame; frame && !pageUrl; frame = frame.parent) {
				if (isAgentPageUrl(frame.url)) pageUrl = frame.url;
			}
		} catch {
		}
		if (!pageUrl) return callback({});
		void agentPageRequestAllowed(details.url, pageUrl).then(
			(allowed) => {
				if (!allowed) {
					const host = URL.canParse(details.url) ? new URL(details.url).host : details.url.slice(0, 80);
					if (blockedPageHosts.size < 100 && !blockedPageHosts.has(host)) {
						blockedPageHosts.add(host);
						console.warn(`AO: blocked an agent page request to ${host}`);
					}
				}
				callback({ cancel: !allowed });
			},
			() => callback({ cancel: true }),
		);
	});

	shellWebContents.on("will-prevent-unload", (event) => {
		if (
			chatDraftRisks.length > 0 &&
			(chatDraftQuitConfirmed ||
				chatDraftWindowCloseConfirmed ||
				confirmUnsafeChatDraftLeave(chatDraftRisks, (options) => dialog.showMessageBoxSync(options), chatDraftDialog))
		) {
			event.preventDefault();
			return;
		}
		desktopQuit.windowCloseCanceled();
	});

	mainWindow.on("close", (event) => {
		const preventClose = shouldPreventUnsafeChatDraftClose(
			chatDraftRisks,
			chatDraftQuitConfirmed || chatDraftWindowCloseConfirmed,
			(options) => dialog.showMessageBoxSync(options),
			chatDraftDialog,
		);
		if (preventClose) {
			desktopQuit.windowCloseCanceled();
			event.preventDefault();
			return;
		}
		if (chatDraftRisks.length > 0) chatDraftWindowCloseConfirmed = true;
	});

	const isMac = process.platform === "darwin";
	if (!isDev) {
		attachShellReloadGuard(
			shellWebContents,
			isMac,
			() => Boolean(browserViewHost?.getLastFocusedPanelContents()),
			() => keybindingRecordingActive,
		);
	}
	attachAppShortcuts(
		shellWebContents,
		isMac,
		shellWebContents,
		false,
		() => keybindingOverrides,
		() => keybindingRecordingActive,
		(id, chord) =>
			!browserViewHost?.isLastUsedBrowser() ||
			shouldHandleAppShortcutInBrowserContext(id, chord, isMac),
		(id) => {
			if (id !== "toggle-browser-devtools") return;
			void toggleAppDevTools(browserViewHost, getShellWebContents, isDev);
		},
		() => terminalFocused,
		() => terminalFontSizeFocused,
		() => artifactZoomEngaged,
	);

	browserViewHost = createBrowserViewHost({
		onTabCloseFailure: createBrowserCloseDiagnostics(browserProfileStateDir(), app.getVersion()),
		isAgentBrowserUrl: (url) => remoteRegistry.isAgentBrowserUrl(url) || cloudAgentProxy.isAgentBrowserUrl(url),
		mainWindow,
		shellWebContents,
		ipcMain,
		shell,
		WebContentsView,
		annotatePreloadPath: annotatePreloadPath(),
		rendererOrigin: new URL(rendererUrl()).origin,
		isMac,
		getKeybindingOverrides: () => keybindingOverrides,
		isKeybindingRecording: () => keybindingRecordingActive,
		agentBrowserRuntime,
		isCloseShellTerminalShortcutEnabled: () => closeShellTerminalShortcutEnabled,
		browserProfileStore,
		browserHistoryStore,
		browserDownloadManager: createBrowserDownloadManager({
			downloadsDirectory: app.getPath("downloads"),
			historyPath: path.join(desktopDataDir, "browser-downloads.json"),
			shell,
			notify: (state) => { awakeDownloads.update(state); shellWebContents.send("browser:downloadsChanged", state); },
		}),
		clearBrowserProfileData: clearElectronBrowserProfileData,
		openInUserBrowser: (url) => userBrowser.open(url),
		describeUserBrowser: () => userBrowser.describe(),
		prepareBrowserPartition: (partition) =>
			applyBrowserIdentity(session.fromPartition(partition), {
				chromeVersion: process.versions.chrome,
				platform: process.platform,
				languages: acceptLanguages(app.getLocale(), app.getPreferredSystemLanguages()),
			}),
		clipboard: { writeImage: writeClipboardImage },
		onNativeViewAdded: () => windowComposition?.restackShell(),
	});
	browserProfileImporter = profileImporter;
	browserProfileIpc = registerBrowserProfileIpc({
		ipcMain,
		shellWebContents,
		mainWindow,
		store: browserProfileStore,
		importer: profileImporter,
		host: browserViewHost,
		buildMenu: (items: BrowserProfileMenuItem[]) => {
			const menu = Menu.buildFromTemplate(items as Electron.MenuItemConstructorOptions[]);
			return { popup: (options) => menu.popup(options as Electron.PopupOptions) };
		},
		confirmSwitch: async (labels: BrowserProfileMenuInput["labels"]) => {
			const result = await dialog.showMessageBox({
				type: "warning",
				title: labels.switchTitle,
				message: labels.switchMessage,
				detail: labels.switchDetail,
				buttons: [labels.cancel, labels.confirm],
				defaultId: 0,
				cancelId: 0,
			});
			return result.response === 1;
		},
		reportSwitchFailure: (message, labels) => {
			if (!mainWindow || mainWindow.isDestroyed()) return;
			void dialog.showMessageBox(mainWindow, {
				type: "error",
				title: labels.switchTitle,
				message,
			}).catch((error) => console.error("browser profile error dialog failed:", error));
		},
	});
	if (daemonStatus.state === "ready") {
		establishBrowserRuntimeLink();
		establishBrowserStreamLink();
	}

	mainWindow.once("show", () => launchTiming.mark("windowShown"));
	showWhenRendererLoaded(mainWindow, shellWebContents);
	void shellWebContents.loadURL(rendererUrl());

	if (isDev && process.env.AO_OPEN_DEVTOOLS === "1") {
		shellWebContents.once("did-frame-finish-load", () => {
			shellWebContents.openDevTools({ mode: "detach" });
		});
	}

	const syncWindowChrome = (force = true) => {
		if (!mainWindow || shellWebContents.isDestroyed()) return;
		syncNativeWindowBackground(force);
		if (process.platform === "darwin") syncMacWindowButtons(mainWindow, shellWebContents);
		shellWebContents.send("window:zoom", shellWebContents.getZoomFactor());
	};
	mainWindow.on("resize", () => syncWindowChrome(false));
	mainWindow.on("show", () => syncWindowChrome());
	mainWindow.on("restore", () => syncWindowChrome());
	shellWebContents.on("did-finish-load", () => syncWindowChrome());
	shellWebContents.on("zoom-changed", () => setTimeout(syncWindowChrome, 0));

	const pushFullScreen = () => {
		if (!mainWindow) return;
		syncNativeWindowBackground(true);
		getShellWebContents()?.send("window:fullscreen", mainWindow.isFullScreen());
	};
	const pushMaximized = () => {
		if (!mainWindow) return;
		getShellWebContents()?.send("window:maximized", mainWindow.isMaximized());
	};
	mainWindow.on("enter-full-screen", pushFullScreen);
	mainWindow.on("leave-full-screen", () => {
		syncWindowChrome();
		pushFullScreen();
	});
	mainWindow.on("maximize", pushMaximized);
	mainWindow.on("unmaximize", pushMaximized);
	mainWindow.on("blur", () => {
		keybindingRecordingActive = false;
		dictation.stop();
	});
	shellWebContents.on("render-process-gone", () => {
		keybindingRecordingActive = false;
		terminalFocused = false;
		terminalFontSizeFocused = false;
		artifactZoomEngaged = false;
	});
	shellWebContents.on("did-start-loading", () => {
		terminalFocused = false;
		terminalFontSizeFocused = false;
		artifactZoomEngaged = false;
		trayLifecycle.clear();
	});
	shellWebContents.on("render-process-gone", () => trayLifecycle.clear());

	mainWindow.on("closed", () => {
		lifecycleLog("main_window_closed");
		artifactSnapshotStore.cancel();
		artifactSnapshotRenderer.close();
		chatDraftRisks = [];
		chatDraftDialog = undefined;
		chatDraftQuitConfirmed = false;
		chatDraftWindowCloseConfirmed = false;
		disposeBrowserRuntimeLink();
		keybindingRecordingActive = false;
		if (windowComposition === composition) windowComposition = null;
		void disposeBrowserViewHost()
			.finally(() => {
				composition.dispose();
			})
			.catch((error) => {
				console.error("AO: window teardown failed:", error);
			});
		mainWindow = null;
		cancelDockBounce();
		trayLifecycle.clearPendingTarget();
		desktopQuit.windowClosed();
	});
}

function createWindow(): Promise<void> {
	if (createWindowPromise) return createWindowPromise;
	void app.dock?.show();
	const pending = createWindowInternal();
	createWindowPromise = pending;
	void pending.then(
		() => {
			if (createWindowPromise === pending) createWindowPromise = null;
		},
		() => {
			if (createWindowPromise === pending) createWindowPromise = null;
		},
	);
	return pending;
}

function resolveAgentBrowserBinaryPath(): string {
	const override = process.env.AO_AGENT_BROWSER_PATH?.trim();
	if (override) return path.resolve(override);
	const binary = process.platform === "win32" ? "agent-browser.exe" : "agent-browser";
	return app.isPackaged
		? path.join(process.resourcesPath, "agent-browser", binary)
		: path.join(app.getAppPath(), "agent-browser", binary);
}

const PORT_DISCOVERY_TIMEOUT_MS = 30_000;
const DAEMON_RESTART_STOP_TIMEOUT_MS = 5_000;
const RUN_FILE_POLL_MS = 300;
const RUN_FILE_FRESHNESS_SKEW_MS = 2_000;
const DAEMON_PROBE_TIMEOUT_MS = 2_000;

function runFilePath(): string | null {
	if (process.env.AO_RUN_FILE) return process.env.AO_RUN_FILE;
	if (isDev) return path.join(os.homedir(), ".ao", DEV_STATE_SUBDIR, "running.json");
	return defaultRunFilePath(process.platform, process.env, os.homedir());
}

function editorStateDir(): string {
	const runFile = runFilePath();
	if (!runFile) throw new Error("Orchestrator state directory is not available.");
	return path.dirname(runFile);
}

const workspaceCheckUnavailable = (message: string) =>
	Object.assign(new Error(message), { code: "SERVICE_UNAVAILABLE" });

async function fetchDesktopSessionPath(
	sessionId: string,
	route: "workspace" | "artifacts",
	field: "workspacePath" | "artifactDir",
	unavailableMessage: string,
): Promise<string> {
	if (daemonStatus.state !== "ready" || !daemonStatus.port) {
		throw workspaceCheckUnavailable("Daemon is not ready.");
	}
	const controller = new AbortController();
	const timer = setTimeout(() => controller.abort(), DAEMON_PROBE_TIMEOUT_MS);
	try {
		const response = await fetch(
			`http://127.0.0.1:${daemonStatus.port}/api/v1/desktop/sessions/${encodeURIComponent(sessionId)}/${route}`,
			{ signal: controller.signal },
		).catch((error: unknown) => {
			if (error instanceof Error && error.name === "AbortError") throw error;
			throw workspaceCheckUnavailable("Daemon is not reachable.");
		});
		const body = await response.json().catch((error: unknown) => {
			if (response.status >= 500) throw workspaceCheckUnavailable("Daemon is not ready.");
			throw error;
		}) as Record<string, unknown>;
		if (!response.ok) {
			const message = typeof body.message === "string" ? body.message : unavailableMessage;
			throw Object.assign(new Error(message), typeof body.code === "string" ? { code: body.code } : {});
		}
		const sessionPath = body[field];
		if (typeof sessionPath !== "string" || !path.isAbsolute(sessionPath)) {
			throw new Error(unavailableMessage);
		}
		return sessionPath;
	} catch (error) {
		if (error instanceof Error && error.name === "AbortError") {
			throw workspaceCheckUnavailable("Timed out while resolving the session workspace.");
		}
		throw error;
	} finally {
		clearTimeout(timer);
	}
}

function resolveSessionWorkspaceForDesktop(sessionId: string): Promise<string> {
	return fetchDesktopSessionPath(sessionId, "workspace", "workspacePath", "Session workspace is not available.");
}

function resolveSessionArtifactDirForDesktop(sessionId: string): Promise<string> {
	return fetchDesktopSessionPath(sessionId, "artifacts", "artifactDir", "This session has no artifacts folder yet.");
}

async function openFileInDefaultBrowser(filePath: string): Promise<void> {
	if (process.platform === "darwin" || process.platform === "win32") {
		const browser = await app.getApplicationInfoForProtocol("https://").catch(() => undefined);
		if (browser?.path) {
			try {
				if (process.platform === "darwin") {
					await launchCommand("open", ["-a", browser.path, filePath], path.dirname(filePath));
				} else {
					await launchCommand(browser.path, [pathToFileURL(filePath).toString()], path.dirname(filePath));
				}
				return;
			} catch (error) {
				console.warn("AO: unable to launch the default browser; opening with the file's handler", error);
			}
		}
	}
	await shell.openExternal(pathToFileURL(filePath).toString());
}

const editorHandoff = createEditorHandoff({
	platform: process.platform,
	env: process.env,
	homeDir: os.homedir(),
	resolveWorkspace: resolveSessionWorkspaceForDesktop,
	resolveArtifactDir: resolveSessionArtifactDirForDesktop,
	readPreference: async () => (await readEditorSettings(editorStateDir())).preferredEditorId,
	writePreference: async (editorId) => {
		await writeEditorPreference(editorStateDir(), editorId);
	},
	launch: (command, args, cwd) => launchCommand(command, args, cwd),
	openDirectory: async (workspacePath) => {
		const error = await shell.openPath(workspacePath);
		if (error) throw new Error(error);
	},
	revealPath: async (filePath) => {
		if (!(await stat(filePath).catch(() => undefined))) throw new Error(`${filePath} does not exist`);
		shell.showItemInFolder(filePath);
	},
	openInBrowser: (filePath) => openFileInDefaultBrowser(filePath),
	realPath: (candidatePath) => realpath(candidatePath),
	copyText: (text) => clipboard.writeText(text),
	discoverTerminalApps: discoverMacTerminalApps,
	readIcon: (appPath) => readNativeAppIcon(appPath, {
		platform: process.platform,
		cacheDir: path.join(app.getPath("userData"), "native-app-icons"),
		fileIcon: async (filePath) => (await app.getFileIcon(filePath, { size: "normal" })).toDataURL(),
	}),
	logError: (message, error) => console.error(message, error),
});

const SHELL_ENV_TIMEOUT_MS = 3_000;

let cachedShellEnv: Record<string, string> | null = null;
let shellEnvPromise: Promise<void> | null = null;
let terminalShellPreference: TerminalShellPreference = { ...DEFAULT_TERMINAL_SHELL };
const configuredLoginShell = process.platform === "darwin" ? (os.userInfo().shell ?? undefined) : undefined;

function telemetryOverrides(): Record<string, string> {
	return {
		AO_TELEMETRY_EVENTS: process.env.AO_TELEMETRY_EVENTS ?? "on",
		AO_TELEMETRY_REMOTE: process.env.AO_TELEMETRY_REMOTE ?? (isDev ? "off" : "posthog"),
		AO_TELEMETRY_POSTHOG_KEY: process.env.AO_TELEMETRY_POSTHOG_KEY ?? DEFAULT_POSTHOG_PROJECT_KEY,
		AO_TELEMETRY_POSTHOG_HOST: process.env.AO_TELEMETRY_POSTHOG_HOST ?? DEFAULT_POSTHOG_HOST,
		AO_SENTRY_DSN: daemonSentryDsn(process.env.AO_SENTRY_DSN, process.env.AO_TELEMETRY_APP_VERSION ?? app.getVersion(), isDev),
		AO_TELEMETRY_APP_VERSION: process.env.AO_TELEMETRY_APP_VERSION ?? app.getVersion(),
		...(process.env.AO_TELEMETRY_DISABLED_EVENTS
			? { AO_TELEMETRY_DISABLED_EVENTS: process.env.AO_TELEMETRY_DISABLED_EVENTS }
			: {}),
	};
}

const loginShellRunner = (timeoutMs: number): ShellRunner => (shellPath, args) =>
	new Promise((resolve) => {
		let settled = false;
		const finish = (value: string | null) => {
			if (settled) return;
			settled = true;
			resolve(value);
		};
		let child: ReturnType<typeof spawn>;
		try {
			child = spawn(shellPath, args, { stdio: ["ignore", "pipe", "ignore"], windowsHide: true });
		} catch {
			finish(null);
			return;
		}
		const timer = setTimeout(() => {
			child.kill("SIGKILL");
			finish(null);
		}, timeoutMs);
		let stdout = "";
		child.stdout?.on("data", (chunk: Buffer) => {
			stdout += chunk.toString("utf8");
		});
		child.once("error", () => {
			clearTimeout(timer);
			finish(null);
		});
		child.once("exit", (code) => {
			clearTimeout(timer);
			finish(code === 0 ? stdout : null);
		});
	});

const runLoginShell = loginShellRunner(SHELL_ENV_TIMEOUT_MS);

function lookupWindowsExecutable(candidate: string): string | null {
	const trimmed = candidate.trim().replace(/^"|"$/g, "");
	if (!trimmed) return null;
	const hasDirectory = trimmed.includes("\\") || trimmed.includes("/") || path.isAbsolute(trimmed);
	const candidates = hasDirectory
		? [trimmed]
		: (process.env.PATH ?? process.env.Path ?? "")
				.split(path.delimiter)
				.filter(Boolean)
				.map((directory) => path.join(directory, trimmed));
	for (const resolved of candidates) {
		if (existsSync(resolved)) return resolved;
	}
	return null;
}

function ensureShellEnv(): Promise<void> {
	if (!shellEnvPromise) {
		if (process.platform === "win32") {
			const probe =
				resolveWindowsShellProbe(terminalShellPreference, process.env, lookupWindowsExecutable) ??
				resolveWindowsShellProbe(DEFAULT_TERMINAL_SHELL, process.env, lookupWindowsExecutable);
			if (!probe) {
				shellEnvPromise = Promise.resolve();
				cachedShellEnv = null;
				return shellEnvPromise;
			}
			shellEnvPromise = resolveShellEnvWithSpec(probe, runLoginShell).then((resolved) => {
				cachedShellEnv = resolved;
				if (!resolved) {
					console.error("AO: could not read the login-shell environment; falling back to the process environment.");
				}
			});
			return shellEnvPromise;
		}
		shellEnvPromise = resolveShellEnv(process.env, runLoginShell, configuredLoginShell).then((resolved) => {
			cachedShellEnv = resolved;
			if (!resolved) {
				console.error("AO: could not read the login-shell environment; falling back to a static PATH floor.");
			}
		});
	}
	return shellEnvPromise;
}

function retryFailedEarlyShellEnvProbe(): Promise<void> {
	if (!cachedShellEnv && earlyShellEnvProbe && shellEnvPromise === earlyShellEnvProbe) {
		earlyShellEnvProbe = null;
		shellEnvPromise = null;
	}
	return ensureShellEnv();
}

let earlyShellEnvProbe: Promise<void> | null = null;
if (process.platform !== "win32") {
	earlyShellEnvProbe = ensureShellEnv();
	earlyShellEnvProbe.catch(() => undefined);
}

const appRunId = process.env.AO_APP_RUN_ID ?? `apprun-${randomUUID()}`;
const browserRuntimeToken = (() => {
	try {
		return resolveBrowserRuntimeToken(desktopDataDir);
	} catch (error) {
		console.warn(`AO: browser runtime key unavailable (${String(error)}); using a per-launch token`);
		return randomBytes(32).toString("base64url");
	}
})();
let stagedBundledTmuxBinary: string | null = null;

async function ensureBundledTmuxStaged(): Promise<void> {
	const source = bundledTmuxBinaryPath(app.isPackaged, process.resourcesPath, process.platform);
	const destination = stableBundledTmuxBinaryPath(
		app.isPackaged,
		process.env.AO_DATA_DIR?.trim() || path.join(os.homedir(), ".ao"),
		app.getVersion(),
		process.platform,
		process.arch,
	);
	if (!source || !destination) {
		stagedBundledTmuxBinary = null;
		return;
	}
	if (existsSync(destination)) {
		stagedBundledTmuxBinary = destination;
		return;
	}
	await mkdir(path.dirname(destination), { recursive: true, mode: 0o750 });
	const temporary = `${destination}.tmp-${process.pid}-${randomUUID()}`;
	try {
		await copyFile(source, temporary);
		await chmod(temporary, 0o755);
		await rename(temporary, destination);
	} finally {
		await rm(temporary, { force: true });
	}
	stagedBundledTmuxBinary = destination;
}

function daemonEnv(forceKeep = keepDaemonAlive(process.env)): NodeJS.ProcessEnv {
	const AO_OWNER = forceKeep ? "persistent" : "app";
	const bundledTmuxBinary = stagedBundledTmuxBinary;
	const ownerTag = {
		AO_OWNER,
		AO_DATA_DIR: desktopDataDir,
		AO_APP_RUN_ID: appRunId,
        ...(process.platform === "darwin" ? { AO_WAKE_HELPER: app.isPackaged
            ? path.join(process.resourcesPath, "wake-helper", "ao-wake")
            : path.join(app.getAppPath(), "wake-helper", "ao-wake") } : {}),
		AO_BROWSER_RUNTIME_TOKEN: "",
		AO_BROWSER_RUNTIME_TOKEN_STDIN: "1",
		...(process.env.APPIMAGE ? { AO_APPIMAGE: process.env.APPIMAGE } : {}),
		AO_ACP_RUNTIME_DIR:
			process.env.AO_ACP_RUNTIME_DIR ??
			(app.isPackaged
				? path.join(process.resourcesPath, "acp-runtime")
				: path.join(app.getAppPath(), "resources", "acp-runtime")),
		...(bundledTmuxBinary ? { AO_TMUX_BINARY: bundledTmuxBinary, AO_TMUX_SOCKET_NAME: "ao" } : {}),
		...(app.isPackaged && process.platform === "darwin"
			? { AO_CLOUDFLARED_PATH: process.env.AO_CLOUDFLARED_PATH ?? path.join(process.resourcesPath, "cloudflared", "cloudflared") }
			: {}),
	};
	const devExtras: Record<string, string> = {};
	if (isDev) {
		if (!process.env.AO_PORT) devExtras.AO_PORT = String(DEV_DAEMON_PORT);
		if (!process.env.AO_RUN_FILE) devExtras.AO_RUN_FILE = runFilePath() ?? "";
		if (!process.env.AO_DATA_DIR) devExtras.AO_DATA_DIR = path.join(os.homedir(), ".ao", DEV_STATE_SUBDIR, "data");
		devExtras.AO_ALLOWED_ORIGINS = devDaemonAllowedOrigins(
			process.env.AO_ALLOWED_ORIGINS,
			rendererUrl(),
		);
	}
	if (process.platform === "win32") {
		return { ...process.env, ...(cachedShellEnv ?? {}), ...devExtras, ...telemetryOverrides(), ...ownerTag };
	}
	return buildDaemonEnv(
		process.env,
		cachedShellEnv,
		{ ...devExtras, ...telemetryOverrides(), ...ownerTag },
		configuredLoginShell,
	);
}

function processAlive(pid: number): boolean {
	if (!pid) return false;
	try {
		process.kill(pid, 0);
		return true;
	} catch {
		return false;
	}
}

async function readDaemonProbe(port: number, endpoint: "healthz" | "readyz"): Promise<DaemonProbe | null> {
	const controller = new AbortController();
	const timer = setTimeout(() => controller.abort(), DAEMON_PROBE_TIMEOUT_MS);
	try {
		const response = await fetch(`http://127.0.0.1:${port}/${endpoint}`, { signal: controller.signal });
		if (!response.ok) return null;
		return parseDaemonProbe(endpoint, await response.json());
	} catch {
		return null;
	} finally {
		clearTimeout(timer);
	}
}

function daemonIdentityError(launch: DaemonLaunchSpec, probe: DaemonProbe): string | null {
	if (launch.source === "dev") {
		const cwdMatches = probe.workingDirectory ? sameCanonicalPath(probe.workingDirectory, launch.cwd) : false;
		const startupCwdMatches = probe.startupWorkingDirectory
			? sameCanonicalPath(probe.startupWorkingDirectory, launch.cwd)
			: false;
		const executableMatches = probe.executablePath ? canonicalPathInside(probe.executablePath, launch.cwd) : false;
		if (!probe.workingDirectory && !probe.startupWorkingDirectory && !probe.executablePath) {
			return "An older Orchestrator daemon is already running, but it does not report its checkout identity. Stop it and restart this app.";
		}
		if (!cwdMatches && !startupCwdMatches && !executableMatches) {
			const actual =
				probe.startupWorkingDirectory ?? probe.workingDirectory ?? probe.executablePath ?? "an unknown location";
			return `Another Orchestrator daemon is already running from ${actual}; expected this checkout at ${launch.cwd}. Stop the other daemon before using this checkout.`;
		}
		return null;
	}

	if (launch.source === "bundled") {
		return bundledDaemonIdentityError(probe, launch.command, process.env.APPIMAGE, sameCanonicalPath);
	}
	return null;
}

function supervisorPipeFromRunFile(rfp: string | null): string {
	if (!rfp) return "\\\\.\\pipe\\ao-supervise";
	const dir = path.basename(path.dirname(rfp));
	if (dir === ".ao" || dir === "." || dir === "") return "\\\\.\\pipe\\ao-supervise";
	return "\\\\.\\pipe\\ao-supervise-" + dir.replace(/[^a-zA-Z0-9-]/g, "-");
}

function dropBrowserRuntimeLink(): void {
	browserRuntimeLink?.dispose();
	browserRuntimeLink = null;
	browserRuntimeLinkIdentity = null;
}

function disposeBrowserRuntimeLink(): void {
	const hadLink = browserRuntimeLink !== null;
	dropBrowserRuntimeLink();
	if (hadLink) publishBrowserRuntimeState(BROWSER_RUNTIME_DISCONNECTED);
}

function publishBrowserRuntimeState(state: BrowserRuntimeState): void {
	getShellWebContents()?.send("browser:runtimeState", state);
}

function reconnectBrowserRuntimeLink(): void {
	dropBrowserRuntimeLink();
	establishBrowserRuntimeLink();
	if (!browserRuntimeLink) publishBrowserRuntimeState(BROWSER_RUNTIME_DISCONNECTED);
}

function establishBrowserRuntimeLink(): void {
	if (!browserViewHost) return;
	const rfp = runFilePath();
	if (!rfp) {
		console.warn("AO: browser runtime link skipped; run-file path unavailable");
		return;
	}
	let runInfo: ReturnType<typeof parseRunFile> = null;
	try {
		runInfo = parseRunFile(readFileSync(rfp, "utf8"));
	} catch {
	}
	const address = runInfo?.browserRuntimeAddress;
	if (!address) {
		console.warn("AO: browser runtime link skipped; daemon did not publish an address");
		return;
	}
	const token = browserRuntimeToken;
	const identity = {
		pid: runInfo?.pid ?? 0,
		startedAtMs: runInfo?.startedAtMs ?? 0,
		address,
		token,
	};
	if (browserRuntimeLink && browserRuntimeLinkIdentity && sameBrowserRuntimeIdentity(browserRuntimeLinkIdentity, identity)) {
		return;
	}
	if (browserRuntimeLink) dropBrowserRuntimeLink();
	browserRuntimeLink = connectBrowserRuntime(address, {
		token,
		execute: (command, signal) => {
			if (command.action === "__render-check") {
				return checkRender({ BrowserWindow }, command.args ?? {}, signal);
			}
			if (command.action === "__render-measure") {
				return measureRender({ BrowserWindow }, command.args ?? {}, signal);
			}
			const host = browserViewHost;
			if (!host) {
				throw Object.assign(new Error("Browser target owner is unavailable"), {
					code: "BROWSER_TARGET_UNAVAILABLE",
				});
			}
			return host.execute(command.sessionId, command.action, command.args, signal);
		},
		log: (message) => console.log(`AO: ${message}`),
		onStateChange: publishBrowserRuntimeState,
		onHandshakeRejected: handleBrowserRuntimeRejection,
	});
	browserRuntimeLinkIdentity = identity;
}

function handleBrowserRuntimeRejection(reason: string): void {
	if (daemonAutoRestartStandDown) return;
	const decision = browserRuntimeRecoveryDecision({
		rejectedReason: reason,
		spawnedHere: daemonProcess !== null,
		attempted: browserRuntimeRecoveryAttempted,
	});
	if (decision.action === "wait") return;
	browserRuntimeRecoveryAttempted = true;
	console.warn(`AO: replacing an adopted daemon that refused this app's browser runtime (${decision.reason})`);
	disposeBrowserRuntimeLink();
	setDaemonStatus({
		state: "starting",
		message: "Restarting the Orchestrator daemon to restore the browser runtime.",
	});
	void startDaemon({ forceReplaceAdopted: true, trigger: "browser_runtime_recovery" });
}

function disposeBrowserStreamLink(): void {
	browserStreamLink?.dispose();
	browserStreamLink = null;
	browserStreamLinkIdentity = null;
}

async function handleBrowserStreamControl(control: BrowserStreamControl): Promise<void> {
	const host = browserViewHost;
	if (!host) throw Object.assign(new Error("Browser target owner is unavailable"), { code: "BROWSER_TARGET_UNAVAILABLE" });
	const sessionId = control.sessionId?.trim() ?? "";
	if (!sessionId) throw Object.assign(new Error("sessionId is required"), { code: "INVALID_ARGUMENT" });
	switch (control.type) {
		case "start": {
			const streamId = control.streamId ?? 0;
			await host.startLiveStream(sessionId, streamId, {
				frame: (frame) => browserStreamLink?.sendFrame({ streamId, ...frame }),
				state: (state) => { browserStreamLink?.sendControl({ type: "state", streamId, sessionId, payload: state }); },
				error: (code, message) => { browserStreamLink?.sendControl({ type: "error", streamId, sessionId, code, message }); },
			});
			return;
		}
		case "stop":
			await host.stopLiveStream(sessionId, control.streamId ?? 0);
			return;
		case "input":
			await host.handleRemoteInput(sessionId, control.payload as BrowserRemoteInput);
			return;
		case "navigate":
			await host.handleRemoteNavigation(sessionId, control.payload as BrowserRemoteNavigation);
			return;
		case "tab":
			await host.handleRemoteTab(sessionId, control.payload as BrowserRemoteTabAction);
			return;
		case "preview": {
			const url = (control.payload as { url?: unknown } | undefined)?.url;
			if (typeof url === "string") await host.openLivePreview(sessionId, url);
			return;
		}
		default:
			throw Object.assign(new Error(`Unsupported browser stream command: ${control.type}`), { code: "INVALID_ARGUMENT" });
	}
}

function establishBrowserStreamLink(): void {
	if (!browserViewHost) return;
	const rfp = runFilePath();
	if (!rfp) return;
	let runInfo: ReturnType<typeof parseRunFile> = null;
	try {
		runInfo = parseRunFile(readFileSync(rfp, "utf8"));
	} catch {
		return;
	}
	const address = runInfo?.browserStreamAddress;
	if (!address) return;
	const identity = { pid: runInfo?.pid ?? 0, startedAtMs: runInfo?.startedAtMs ?? 0, address, token: browserRuntimeToken };
	if (browserStreamLink && browserStreamLinkIdentity && sameBrowserRuntimeIdentity(browserStreamLinkIdentity, identity)) return;
	disposeBrowserStreamLink();
	browserStreamLink = connectBrowserStream(address, {
		token: browserRuntimeToken,
		onControl: handleBrowserStreamControl,
		onDisconnect: () => browserViewHost?.stopAllLiveStreams(),
		log: (message) => console.log(`AO: ${message}`),
	});
	browserStreamLinkIdentity = identity;
}

function establishSupervisorLink(): void {
	const rfp = runFilePath();
	const addr =
		process.platform === "win32"
			? supervisorPipeFromRunFile(rfp)
			: rfp
				? path.join(path.dirname(rfp), "supervise.sock")
				: null;
	if (addr) {
		supervisorLink?.dispose();
		supervisorLink = connectSupervisor(addr, {
			log: (msg) => console.log(`AO: ${msg}`),
		});
	} else {
		console.warn("AO: supervisor link skipped; run-file path unavailable");
	}
}

async function inspectExistingDaemon(
	launch: DaemonLaunchSpec,
): Promise<{ status: DaemonStatus; owner: string | undefined; appRunId: string | undefined } | null> {
	const handshakePath = runFilePath();
	let runFileContents: string | null = null;
	if (handshakePath) {
		try {
			runFileContents = await readFile(handshakePath, "utf8");
		} catch {
			runFileContents = null;
		}
	}
	const status = await resolveDaemonFromRunFile({
		runFileContents,
		isProcessAlive: processAlive,
		probe: readDaemonProbe,
		identityError: (probe) => daemonIdentityError(launch, probe),
	});
	if (!status) return null;
	const info = runFileContents ? parseRunFile(runFileContents) : null;
	return { status, owner: info?.owner, appRunId: info?.appRunId };
}

async function gracefullyReplaceDaemonForBrowser(status: DaemonStatus): Promise<void> {
	if (!status.port) throw new Error("the running daemon did not report a port");
	const response = await fetch(`http://127.0.0.1:${status.port}/shutdown`, { method: "POST" });
	if (!response.ok) throw new Error(`daemon shutdown returned HTTP ${response.status}`);

	const deadline = Date.now() + 8_000;
	while (Date.now() < deadline) {
		if (!(await readDaemonProbe(status.port, "healthz"))) return;
		await new Promise<void>((resolve) => setTimeout(resolve, 200));
	}
	throw new Error("the previous daemon did not stop within 8 seconds");
}

async function refreshDaemonStatus(): Promise<DaemonStatus> {
	if (daemonProcess) {
		return daemonStatus;
	}
	const launch = resolveDaemonLaunch(
		process.env,
		app.isPackaged,
		process.resourcesPath,
		app.getAppPath(),
		os.homedir(),
		process.platform,
	);
	if (!launch) return daemonStatus;
	const existing = await inspectExistingDaemon(launch);
	if (existing) {
		if (!daemonAutoRestartStandDown && browserDaemonOwnershipDecision(appRunId, existing).action === "replace") {
			return startDaemon({ trigger: "renderer_status" });
		}
		setDaemonStatus(existing.status);
	} else if (
		daemonStatus.state === "ready" ||
		(daemonStatus.state === "error" && (daemonStatus.pid || daemonStatus.port))
	) {
		lifecycleLog("daemon_unreachable", { previousState: daemonStatus.state, previousPid: daemonStatus.pid ?? null, decision: "mark_stopped_no_respawn" });
		setDaemonStatus({
			state: "stopped",
			message: "Orchestrator daemon is no longer reachable.",
			code: "daemon_unreachable",
		});
	}
	return daemonStatus;
}

async function startDaemon(options: { forceReplaceAdopted?: boolean; autoRestartIntent?: DaemonAutoRestartIntent; trigger?: DaemonStartTrigger; keepAlive?: boolean } = {}): Promise<DaemonStatus> {
	if (!options.autoRestartIntent && daemonStartAutoRestartIntent) {
		daemonStartEpoch += 1;
		daemonStartPromise = null;
	}
	if (options.forceReplaceAdopted) {
		forceReplaceAdoptedRequested = true;
	}
	if (daemonStartPromise) {
		return daemonStartPromise;
	}
	const startEpoch = daemonStartEpoch;
	daemonStartAutoRestartIntent = options.autoRestartIntent;
	const trigger = options.trigger ?? (options.autoRestartIntent ? "auto_restart" : "startup");
	const promise = startDaemonInner(startEpoch, options.autoRestartIntent, trigger, options.keepAlive).finally(() => {
		if (daemonStartPromise === promise) {
			daemonStartPromise = null;
			daemonStartAutoRestartIntent = undefined;
		}
	});
	daemonStartPromise = promise;
	return daemonStartPromise;
}

function resolvedDaemonPort(): number {
	return isDev && !process.env.AO_PORT ? DEV_DAEMON_PORT : expectedDaemonPort(process.env);
}

function daemonLaunchEnv(): NodeJS.ProcessEnv {
	if (!isDev || process.platform !== "win32") {
		return process.env;
	}
	try {
		const daemonDir = path.resolve(app.getAppPath(), "daemon");
		const manifestPath = path.join(daemonDir, "dev-daemon.json");
		const parsed = JSON.parse(readFileSync(manifestPath, "utf8")) as { path?: unknown };
		if (typeof parsed.path === "string") {
			const daemonPath = path.resolve(parsed.path.trim());
			const relativePath = path.relative(daemonDir, daemonPath);
			if (relativePath && !relativePath.startsWith("..") && !path.isAbsolute(relativePath)) {
				return { ...process.env, AO_DEV_DAEMON_BINARY: daemonPath };
			}
		}
	} catch {
	}
	return process.env;
}

async function repairDevAcpRuntime(): Promise<void> {
	if (app.isPackaged || process.env.AO_ACP_RUNTIME_DIR) return;
	const runtimeDir = path.join(app.getAppPath(), "resources", "acp-runtime");
	const adapter = path.join(
		runtimeDir,
		"node_modules",
		"@agentclientprotocol",
		"claude-agent-acp",
		"dist",
		"index.js",
	);
	if (existsSync(adapter)) return;
	const script = path.join(app.getAppPath(), "scripts", "build-acp-runtime.mjs");
	if (!existsSync(script)) return;
	console.warn(`[ao] ACP runtime missing at ${runtimeDir}; rebuilding`);
	await new Promise<void>((resolve) => {
		const child = spawn(process.execPath, [script], {
			cwd: app.getAppPath(),
			env: { ...process.env, ELECTRON_RUN_AS_NODE: "1" },
			stdio: "inherit",
		});
		child.once("error", (err) => {
			console.error("[ao] ACP runtime rebuild failed to start:", err);
			captureMainError({ category: "acp_runtime", reason: "rebuild", error: err });
			resolve();
		});
		child.once("exit", (code) => {
			if (code !== 0) {
				console.error(`[ao] ACP runtime rebuild exited with ${code}`);
				captureMainError({ category: "acp_runtime", reason: "rebuild", ...(typeof code === "number" ? { detail: `code:${code}` } : {}) });
			}
			resolve();
		});
	});
}

async function startDaemonInner(startEpoch: number, autoRestartIntent: DaemonAutoRestartIntent | undefined, trigger: DaemonStartTrigger, keepAliveOverride?: boolean): Promise<DaemonStatus> {
	const startCancelled = () => startEpoch !== daemonStartEpoch || browserQuitRequested ||
		(autoRestartIntent !== undefined && autoRestartIntent.generation !== daemonAutoRestartGeneration);
	if (daemonProcess) {
		return daemonStatus;
	}

	await repairDevAcpRuntime();
	if (startCancelled()) {
		return daemonStatus;
	}

	const shellEnvReady = ensureShellEnv();
	shellEnvReady.catch(() => undefined);

	const launch = resolveDaemonLaunch(
		daemonLaunchEnv(),
		app.isPackaged,
		process.resourcesPath,
		app.getAppPath(),
		os.homedir(),
		process.platform,
	);
	if (!launch) {
		setDaemonStatus({
			state: "stopped",
			message: "AO_DAEMON_COMMAND is not configured; renderer uses loopback REST when available.",
			code: "not_configured",
		});
		return daemonStatus;
	}

	let replacementKeepAlive: boolean | undefined;
	const existing = await inspectExistingDaemon(launch);
	if (startCancelled()) {
		return daemonStatus;
	}
	if (existing) {
		if (autoRestartIntent) return daemonStatus;
		const ownership = browserDaemonOwnershipDecision(appRunId, existing);
		if (shouldReplaceDaemonForStartup({ ownership, forceRequested: forceReplaceAdoptedRequested })) {
			lifecycleLog("daemon_replace", {
				trigger,
				via: "run_file",
				pid: existing.status.pid ?? null,
				forced: forceReplaceAdoptedRequested,
				ownership: ownership.action,
				decision: "graceful_shutdown_then_spawn",
			});
			try {
				await gracefullyReplaceDaemonForBrowser(existing.status);
				replacementKeepAlive = keepAliveForReplacement({
					ownership,
					forceRequested: forceReplaceAdoptedRequested,
				});
			} catch (err) {
				setDaemonStatus({
					state: "error",
					message: `Could not take ownership of the browser runtime: ${(err as Error).message}`,
					code: "not_ready",
				});
				return daemonStatus;
			}
		} else {
			setDaemonStatus(existing.status);
			if (shouldLinkOnAttach(existing.owner)) {
				establishSupervisorLink();
			}
			return daemonStatus;
		}
	}

	const directDaemon = await resolveDaemonFromPort({
		expectedPort: resolvedDaemonPort(),
		probe: readDaemonProbe,
		identityError: (probe) => daemonIdentityError(launch, probe),
	});
	if (startCancelled()) {
		return daemonStatus;
	}
	if (directDaemon) {
		if (autoRestartIntent) return daemonStatus;
		let portAttachOwner: string | undefined;
		let portAttachAppRunId: string | undefined;
		const rfp = runFilePath();
		if (rfp) {
			try {
				const info = parseRunFile(await readFile(rfp, "utf8"));
				portAttachOwner = info?.owner;
				portAttachAppRunId = info?.appRunId;
			} catch {
			}
		}
		const ownership = browserDaemonOwnershipDecision(appRunId, {
			owner: portAttachOwner,
			appRunId: portAttachAppRunId,
		});
		if (shouldReplaceDaemonForStartup({ ownership, forceRequested: forceReplaceAdoptedRequested })) {
			lifecycleLog("daemon_replace", {
				trigger,
				via: "port_probe",
				pid: directDaemon.pid ?? null,
				forced: forceReplaceAdoptedRequested,
				ownership: ownership.action,
				decision: "graceful_shutdown_then_spawn",
			});
			try {
				await gracefullyReplaceDaemonForBrowser(directDaemon);
				replacementKeepAlive = keepAliveForReplacement({
					ownership,
					forceRequested: forceReplaceAdoptedRequested,
				});
			} catch (err) {
				setDaemonStatus({
					state: "error",
					message: `Could not take ownership of the browser runtime: ${(err as Error).message}`,
					code: "not_ready",
				});
				return daemonStatus;
			}
		} else {
			setDaemonStatus(directDaemon);
			if (shouldLinkOnAttach(portAttachOwner)) establishSupervisorLink();
			return daemonStatus;
		}
	}

	await shellEnvReady;
	if (startCancelled()) {
		return daemonStatus;
	}
	await retryFailedEarlyShellEnvProbe();
	if (startCancelled()) {
		return daemonStatus;
	}

	const orphanProbe = await readDaemonProbe(resolvedDaemonPort(), "healthz");
	const runFilePath_ = runFilePath();
	let runFile: ReturnType<typeof parseRunFile> = null;
	if (runFilePath_) {
		try {
			runFile = parseRunFile(await readFile(runFilePath_, "utf8"));
		} catch {
		}
	}
	if (startCancelled() || (autoRestartIntent && orphanProbe)) return daemonStatus;
	const holder = autoRestartIntent ? { pid: null, reason: "automatic recovery never signals a port holder" } : await portHolderPidToReplace({
		probe: orphanProbe,
		runFile,
		processStartMs: readProcessStartMs,
	});
	if (holder.pid === null && runFile && processAlive(runFile.pid)) {
		console.warn(`AO: not stopping process ${runFile.pid} named by running.json: ${holder.reason}`);
		lifecycleLog("daemon_port_holder_left_alone", { trigger, pid: runFile.pid, healthz: orphanProbe ? "answering" : "silent", reason: holder.reason });
	}
	const holderPid = holder.pid;
	if (holderPid !== null) {
		lifecycleLog("daemon_kill_unresponsive", {
			trigger,
			pid: holderPid,
			healthz: orphanProbe ? "answering" : "silent",
			reason: "identified_ao_daemon_holding_port",
			decision: "sigterm_process_group_then_spawn",
		});
		try {
			process.kill(-holderPid, "SIGTERM");
		} catch {
			try {
				process.kill(holderPid, "SIGTERM");
			} catch {
			}
		}
		const TAKEOVER_TIMEOUT_MS = 8_000;
		const TAKEOVER_POLL_MS = 200;
		const deadline = Date.now() + TAKEOVER_TIMEOUT_MS;
		while (Date.now() < deadline) {
			const still = await readDaemonProbe(resolvedDaemonPort(), "healthz");
			if (!still) break;
			await new Promise<void>((r) => setTimeout(r, TAKEOVER_POLL_MS));
		}
		if (runFilePath_) {
			await rm(runFilePath_, { force: true });
		}
	}

	if (launch.source === "bundled" && !existsSync(launch.command)) {
		setDaemonStatus({
			state: "error",
			message: `Bundled Orchestrator daemon binary was not found at ${launch.command}. Rebuild the desktop package.`,
			code: "binary_missing",
		});
		return daemonStatus;
	}
	try {
		await ensureBundledTmuxStaged();
	} catch (err) {
		if (autoRestartIntent && startCancelled()) return daemonStatus;
		setDaemonStatus({
			state: "error",
			message: `Could not stage Orchestrator's bundled tmux under the Orchestrator data directory: ${(err as Error).message}`,
			code: "binary_missing",
		});
		return daemonStatus;
	}
	if (autoRestartIntent && startCancelled()) return daemonStatus;

	daemonOutput = "";
	setDaemonStatus({ state: "starting" });
	if (launch.source === "bundled") {
		try {
			await mkdir(launch.cwd, { recursive: true, mode: 0o750 });
		} catch (err) {
			if (autoRestartIntent && startCancelled()) return daemonStatus;
			setDaemonStatus({
				state: "error",
				message: `Could not create the Orchestrator data directory at ${launch.cwd}: ${(err as Error).message}`,
				code: "datadir_unwritable",
			});
			return daemonStatus;
		}
	}
	const isolation = await planDaemonSpawnIsolation(launch);
	if (startCancelled()) return daemonStatus;
	const spawnLaunch = isolation.kind === "scope" ? isolation.launch : launch;
	if (autoRestartIntent) {
		const runFile = await readRunFileState();
		if (!daemonAutoRestartMarkerMatches(autoRestartIntent, runFile)) return daemonStatus;
	}
	if (startCancelled()) return daemonStatus;

	const keep = autoRestartIntent?.keepAlive ?? keepAliveOverride ?? replacementKeepAlive ?? keepDaemonAlive(process.env);
	forceReplaceAdoptedRequested = false;
	let keepDaemonLogFd: number | undefined;
	let stdio: "pipe" | "ignore" | ["pipe", number | "ignore", number | "ignore"] = "pipe";
	if (keep) {
		const logPath = path.join(os.homedir(), ".ao", "daemon.log");
		try {
			keepDaemonLogFd = openSync(logPath, "a");
			stdio = ["pipe", keepDaemonLogFd, keepDaemonLogFd];
		} catch {
			console.warn(`AO: keep-daemon log redirect failed; daemon will run with stdio disabled: ${logPath}`);
			keepDaemonLogFd = undefined;
			stdio = ["pipe", "ignore", "ignore"];
		}
	}
	lifecycleLog("daemon_spawn", {
		trigger,
		keepAlive: keep,
		source: launch.source,
		isolation: isolation.kind,
		...(isolation.kind === "scope" ? { scopeUnit: isolation.unit, slice: isolation.slice } : { isolationReason: isolation.reason }),
		...(autoRestartIntent ? { crashedPid: autoRestartIntent.crashedPid } : {}),
	});
	let child: ChildProcess;
	try {
		child = spawn(spawnLaunch.command, spawnLaunch.args, {
			cwd: launch.cwd,
			env: daemonEnv(keep),
			shell: launch.shell,
			detached: true,
			windowsHide: true,
			stdio,
		});
	} catch (err) {
		if (keepDaemonLogFd !== undefined) {
			try {
				closeSync(keepDaemonLogFd);
			} catch {
			}
		}
		lifecycleLog("daemon_spawn_failed", { trigger, stage: "spawn" }, err);
		throw err;
	}
	const oomScoreAdj = isolation.kind === "oom_score_adj" && child.pid !== undefined
		? applyDaemonOomScoreAdj(child.pid, isolation.adjustment)
		: undefined;
	lifecycleLog("daemon_spawned", { trigger, daemonPid: child.pid ?? null, keepAlive: keep, oomScoreAdj });
	if (keepDaemonLogFd !== undefined) {
		try {
			closeSync(keepDaemonLogFd);
		} catch {
		}
		keepDaemonLogFd = undefined;
	}
	if (child.stdin) {
		child.stdin.on("error", () => undefined);
		child.stdin.end(`${browserRuntimeToken}\n`);
	} else {
		console.warn("AO: browser runtime token handoff pipe was unavailable");
	}
	if (keep) child.unref();
	daemonProcess = child;
	daemonAutoRestartStandDown = false;

	const spawnedAtMs = Date.now();
	let portConfirmed = false;
	let runFileTimer: ReturnType<typeof setInterval> | undefined;
	let fallbackTimer: ReturnType<typeof setTimeout> | undefined;

	const stopDiscovery = () => {
		if (runFileTimer) clearInterval(runFileTimer);
		runFileTimer = undefined;
		if (fallbackTimer) clearTimeout(fallbackTimer);
		fallbackTimer = undefined;
	};

	const reportBoundPort = (port: number) => {
		if (portConfirmed || daemonProcess !== child || daemonStoppingProcess === child) return;
		portConfirmed = true;
		stopDiscovery();
		lifecycleLog("daemon_ready", { daemonPid: child.pid ?? null, port, startupMs: Date.now() - spawnedAtMs });
		setDaemonStatus({ state: "ready", port, pid: child.pid });
		void readDaemonProbe(port, "healthz").then((probe) => {
			if (probe?.ipv6Loopback !== true || probe.pid !== child.pid) return;
			if (daemonStatus.state !== "ready" || daemonStatus.port !== port || daemonStatus.pid !== child.pid) return;
			daemonStatus = { ...daemonStatus, ipv6Loopback: true };
			getShellWebContents()?.send("daemon:status", daemonStatus);
		});

		if (!keep) {
			establishSupervisorLink();
		}
	};

	if (!keep) {
		const scanStdout = createListenPortScanner(reportBoundPort);
		const scanStderr = createListenPortScanner(reportBoundPort);

		child.stdout?.on("data", (chunk: Buffer) => {
			const text = chunk.toString("utf8");
			appendDaemonOutput(text);
			console.log(text.trimEnd());
			scanStdout(text);
		});

		child.stderr?.on("data", (chunk: Buffer) => {
			const text = chunk.toString("utf8");
			appendDaemonOutput(text);
			console.error(text.trimEnd());
			scanStderr(text);
		});
	}

	const handshakePath = runFilePath();
	if (handshakePath) {
		runFileTimer = setInterval(() => {
			readFile(handshakePath, "utf8")
				.then((contents) => {
					const info = parseRunFile(contents);
					if (info && info.startedAtMs >= spawnedAtMs - RUN_FILE_FRESHNESS_SKEW_MS) {
						reportBoundPort(info.port);
					}
				})
				.catch(() => undefined);
		}, RUN_FILE_POLL_MS);
	}

	fallbackTimer = setTimeout(() => {
		if (portConfirmed || daemonProcess !== child || daemonStoppingProcess === child) return;
		fallbackTimer = undefined;
		lifecycleLog("daemon_port_unconfirmed", { daemonPid: child.pid ?? null, waitedMs: Date.now() - spawnedAtMs });
		setDaemonStatus(slowDaemonStartupStatus({
			output: daemonOutput,
			executablePath: launch.command,
			workingDirectory: launch.cwd,
			handshakePath,
		}));
	}, PORT_DISCOVERY_TIMEOUT_MS);

	child.once("error", (error) => {
		stopDiscovery();
		lifecycleLog("daemon_spawn_failed", { trigger, stage: "child_error", daemonPid: child.pid ?? null, current: daemonProcess === child }, error);
		if (daemonProcess !== child) return;
		daemonProcess = null;
		if (daemonStoppingProcess === child) daemonStoppingProcess = null;
		setDaemonStatus({
			state: "error",
			message: error.message,
			details: daemonOutput.trim() || undefined,
			code: "spawn_failed",
		});
	});

	child.once("exit", (code, signal) => {
		stopDiscovery();
		lifecycleLog("daemon_exit", {
			daemonPid: child.pid ?? null,
			code,
			signal,
			uptimeMs: Date.now() - spawnedAtMs,
			portConfirmed,
			current: daemonProcess === child,
			requestedStop: daemonStoppingProcess === child,
			restartAfterStop: daemonRestartAfterExitProcess === child,
			quitting: browserQuitRequested,
		});
		if (daemonProcess !== child) return;
		daemonProcess = null;
		if (daemonStoppingProcess === child) {
			daemonStoppingProcess = null;
			if (daemonRestartAfterExitProcess === child) {
				daemonRestartAfterExitProcess = null;
				lifecycleLog("daemon_restart_decision", { daemonPid: child.pid ?? null, code, signal, decision: "restart", cause: "manual_restart_after_stop" });
				void startDaemonForRestart(undefined, "manual_restart");
				return;
			}
			setDaemonStatus({ state: "stopped" });
			return;
		}
		if (isolation.kind === "scope" && !portConfirmed && signal === null && !browserQuitRequested && !daemonScopeLaunchFailed) {
			daemonScopeLaunchFailed = true;
			lifecycleLog("daemon_restart_decision", {
				daemonPid: child.pid ?? null,
				code,
				signal,
				decision: "restart",
				cause: "scope_launch_failed",
				fallback: "oom_score_adj",
			});
			void startDaemonForRestart(autoRestartIntent, "scope_fallback", keep);
			return;
		}
		supervisorLink?.dispose();
		supervisorLink = null;
		daemonAutoRestartStandDown = true;
		setDaemonStatus({
			state: "stopped",
			message: signal ? `Daemon exited with ${signal}` : `Daemon exited with code ${code ?? "unknown"}`,
			details: daemonOutput.trim() || undefined,
			code: "exited",
			exitCode: code,
			signal,
		});
		const exitedPid = child.pid ?? null;
		const generation = daemonAutoRestartGeneration;
		void readRunFileState().then((runFile) => {
			const observed = { daemonPid: exitedPid, code, signal, runFilePresent: runFile.present, runFilePid: runFile.pid };
			if (generation !== daemonAutoRestartGeneration || browserQuitRequested || daemonProcess) {
				lifecycleLog("daemon_restart_decision", {
					...observed,
					decision: "no_restart",
					cause: browserQuitRequested ? "app_quitting" : daemonProcess ? "newer_daemon_running" : "superseded_by_user_action",
				});
				return;
			}
			if (
				daemonExitWasUngraceful({
					runFilePresent: runFile.present,
					runFilePid: runFile.pid,
					childPid: exitedPid,
					code,
					signal,
				})
			) {
				scheduleDaemonAutoRestart(exitedPid, runFile.present, keep, observed);
			} else {
				lifecycleLog("daemon_restart_decision", {
					...observed,
					decision: "no_restart",
					cause: runFile.present
						? "run_file_names_other_daemon"
						: code !== null && code !== 0
							? "nonzero_exit_without_run_file"
							: "graceful_exit",
				});
			}
		});
	});

	return daemonStatus;
}

let daemonScopeProbe: Promise<boolean> | null = null;
let daemonScopeLaunchFailed = false;
function planDaemonSpawnIsolation(launch: DaemonLaunchSpec): Promise<DaemonScopePlan> {
	return planDaemonScope(launch, {
		platform: process.platform,
		env: process.env,
		scopeLaunchFailed: daemonScopeLaunchFailed,
		exists: existsSync,
		findExecutable: (name) => {
			for (const dir of (cachedShellEnv?.PATH ?? process.env.PATH ?? "").split(path.delimiter)) {
				if (dir && existsSync(path.join(dir, name))) return path.join(dir, name);
			}
			return null;
		},
		runProbe: (command, args) => {
			daemonScopeProbe ??= execFileAsync(command, args, { timeout: 5_000 }).then(() => true, () => false);
			return daemonScopeProbe;
		},
	}, { pid: process.pid, nowMs: Date.now() });
}

function applyDaemonOomScoreAdj(pid: number, adjustment: number): number | null {
	return raiseOomScoreAdj(pid, adjustment, {
		read: (file) => readFileSync(file, "utf8"),
		write: (file, value) => writeFileSync(file, value),
	});
}

function killDaemon(child: ChildProcess): void {
	if (child.pid === undefined) return;
	try {
		process.kill(-child.pid, "SIGTERM");
	} catch {
		child.kill("SIGTERM");
	}
}

async function readRunFileState(): Promise<{ present: boolean; pid: number | null }> {
	const rfp = runFilePath();
	if (rfp === null) return { present: true, pid: null };
	try {
		return { present: true, pid: parseRunFile(await readFile(rfp, "utf8"))?.pid ?? null };
	} catch (error) {
		return { present: (error as NodeJS.ErrnoException).code !== "ENOENT", pid: null };
	}
}

function daemonAutoRestartMarkerMatches(intent: DaemonAutoRestartIntent, runFile: { present: boolean; pid: number | null }): boolean {
	return intent.runFilePresent ? runFile.present && runFile.pid === intent.crashedPid : !runFile.present;
}

function cancelDaemonAutoRestart(): void {
	daemonAutoRestartGeneration += 1;
	if (daemonAutoRestartTimer !== null) {
		clearTimeout(daemonAutoRestartTimer);
		daemonAutoRestartTimer = null;
	}
	daemonAutoRestartExits = [];
}

function scheduleDaemonAutoRestart(
	crashedPid: number | null,
	runFilePresent: boolean,
	keepAlive: boolean,
	observed: Record<string, string | number | boolean | null> = {},
): void {
	if (browserQuitRequested || crashedPid === null) {
		lifecycleLog("daemon_restart_decision", { ...observed, decision: "no_restart", cause: browserQuitRequested ? "app_quitting" : "pid_unknown" });
		return;
	}
	if (daemonAutoRestartTimer !== null) clearTimeout(daemonAutoRestartTimer);
	const { plan, recentExits } = planDaemonAutoRestart(daemonAutoRestartExits, Date.now());
	daemonAutoRestartExits = recentExits;
	daemonAutoRestartTimer = null;
	lifecycleLog("daemon_restart_decision", {
		...observed,
		cause: "ungraceful_exit",
		decision: plan.action,
		recentExits: recentExits.length,
		...(plan.action === "restart" ? { attempt: plan.attempt, delayMs: plan.delayMs } : {}),
	});
	if (plan.action === "give_up") return;
	const intent = { generation: daemonAutoRestartGeneration, crashedPid, runFilePresent, keepAlive };
	const cancelCause = () => daemonProcess ? "newer_daemon_running" : browserQuitRequested ? "app_quitting" : "superseded_by_user_action";
	daemonAutoRestartTimer = setTimeout(() => {
		daemonAutoRestartTimer = null;
		if (intent.generation !== daemonAutoRestartGeneration || daemonProcess || browserQuitRequested) {
			lifecycleLog("daemon_restart_cancelled", { crashedPid, cause: cancelCause() });
			return;
		}
		void readRunFileState().then((runFile) => {
			if (!daemonAutoRestartMarkerMatches(intent, runFile)) {
				lifecycleLog("daemon_restart_cancelled", { crashedPid, cause: "run_file_changed", runFilePresent: runFile.present, runFilePid: runFile.pid });
				return;
			}
			if (intent.generation !== daemonAutoRestartGeneration || daemonProcess || browserQuitRequested) {
				lifecycleLog("daemon_restart_cancelled", { crashedPid, cause: cancelCause() });
				return;
			}
			void startDaemonForRestart(intent);
		});
	}, plan.delayMs);
}

async function shutdownDaemonForDesktopQuit(): Promise<void> {
	const accepted = await requestDesktopShutdown({
		keepAlive: keepDaemonAlive(process.env),
		appRunId,
		status: daemonStatus,
		preserveAgents: () => preserveAgentsOnQuit || isUpdateRestartRequested(),
		readRunInfo: async () => {
			const handshakePath = runFilePath();
			if (!handshakePath) throw new Error("Daemon ownership record is unavailable");
			return parseRunFile(await readFile(handshakePath, "utf8"));
		},
		requestShutdown: (port, body) => fetch(`http://127.0.0.1:${port}/shutdown`, {
			method: "POST",
			headers: { "content-type": "application/json" },
			body: JSON.stringify(body),
			signal: AbortSignal.timeout(3000),
		}),
	});
	lifecycleLog("daemon_quit_shutdown", { accepted, daemonPid: daemonProcess?.pid ?? daemonStatus.pid ?? null, updateRestart: isUpdateRestartRequested() });
	if (!accepted) return;
	desktopAgentShutdownAccepted = true;
	if (daemonProcess) daemonStoppingProcess = daemonProcess;
}

function stopDaemon(): DaemonStatus {
	daemonStartEpoch += 1;
	daemonStartPromise = null;
	daemonStartAutoRestartIntent = undefined;
	daemonRestartAfterExitProcess = null;
	cancelDaemonAutoRestart();
	if (!daemonProcess) {
		setDaemonStatus({ state: "stopped" });
		return daemonStatus;
	}

	daemonStoppingProcess = daemonProcess;
	lifecycleLog("daemon_stop_requested", { daemonPid: daemonProcess.pid ?? null });
	supervisorLink?.dispose();
	supervisorLink = null;
	disposeBrowserRuntimeLink();
	killDaemon(daemonProcess);
	setDaemonStatus({ state: "stopped" });
	return daemonStatus;
}

function reportDaemonRestartFailure(error: unknown): DaemonStatus {
	setDaemonStatus({
		state: "error",
		message: `Could not restart the Orchestrator daemon: ${error instanceof Error ? error.message : String(error)}`,
		details: daemonOutput.trim() || undefined,
		code: "spawn_failed",
	});
	return daemonStatus;
}

async function startDaemonForRestart(autoRestartIntent?: DaemonAutoRestartIntent, trigger?: DaemonStartTrigger, keepAlive?: boolean): Promise<DaemonStatus> {
	try {
		return await startDaemon({ autoRestartIntent, trigger: trigger ?? (autoRestartIntent ? "auto_restart" : "manual_restart"), keepAlive });
	} catch (error) {
		if (autoRestartIntent && (autoRestartIntent.generation !== daemonAutoRestartGeneration || browserQuitRequested)) return daemonStatus;
		return reportDaemonRestartFailure(error);
	}
}

async function restartDaemon(): Promise<DaemonStatus> {
	daemonAutoRestartStandDown = false;
	cancelDaemonAutoRestart();
	const child = daemonProcess;
	lifecycleLog("daemon_restart_decision", { daemonPid: child?.pid ?? null, decision: "restart", cause: "user_requested" });
	if (!child) return startDaemonForRestart();
	await captureSideChatState().catch((error) => console.warn("AO: pre-restart side snapshot failed", error));

	const exited = new Promise<boolean>((resolve) => {
		let settled = false;
		const finish = (didExit: boolean) => {
			if (settled) return;
			settled = true;
			clearTimeout(timer);
			child.off("exit", onExit);
			resolve(didExit);
		};
		const onExit = () => finish(true);
		const timer = setTimeout(() => finish(false), DAEMON_RESTART_STOP_TIMEOUT_MS);
		child.once("exit", onExit);
	});

	stopDaemon();
	daemonRestartAfterExitProcess = child;
	if (!(await exited)) {
		setDaemonStatus({
			state: "error",
			message: "Orchestrator daemon is still stopping. It will restart automatically when shutdown completes.",
			details: daemonOutput.trim() || undefined,
			code: "not_ready",
		});
		return daemonStatus;
	}
	return startDaemonForRestart();
}

registerCliLinkIpc({
	isPackaged: () => app.isPackaged,
	resourcesPath: process.resourcesPath,
	appImage: process.env.APPIMAGE,
	dataDir: desktopDataDir,
	appVersion: app.getVersion(),
	shellPath: () => cachedShellEnv?.PATH ?? process.env.PATH,
});

const CLI_SHADOW_SHELL_TIMEOUT_MS = 60_000;

registerCliShadowIpc({
	enabled: () => cliShadowEnabled(process.env, app.isPackaged),
	shellPath: createCliShadowShellPath({
		settleStartupProbes: async () => {
			await ensureShellEnv();
			await retryFailedEarlyShellEnvProbe();
		},
		startupPath: () => cachedShellEnv?.PATH,
		probe: async () => {
			if (process.platform === "win32") return undefined;
			const resolved = await resolveShellEnv(process.env, loginShellRunner(CLI_SHADOW_SHELL_TIMEOUT_MS), configuredLoginShell);
			return resolved?.PATH;
		},
	}),
	bundledPath: () => {
		const launch = resolveDaemonLaunch(
			process.env,
			app.isPackaged,
			process.resourcesPath,
			app.getAppPath(),
			os.homedir(),
			process.platform,
		);
		return launch && launch.source !== "configured" ? launch.command : undefined;
	},
	isPackaged: () => app.isPackaged,
	appImage: process.env.APPIMAGE,
	reveal: (filePath) => shell.showItemInFolder(filePath),
});

ipcMain.handle("daemon:getStatus", () => refreshDaemonStatus());
ipcMain.handle("daemon:start", () => {
	daemonAutoRestartStandDown = false;
	cancelDaemonAutoRestart();
	return startDaemon({ trigger: "manual_start" });
});
ipcMain.handle("daemon:stop", () => stopDaemon());
ipcMain.handle("daemon:restart", async () => {
	try {
		return await restartDaemon();
	} catch (error) {
		return reportDaemonRestartFailure(error);
	}
});
async function captureSideChatState(): Promise<void> {
	const generation = daemonAttachmentGeneration;
	const { attachment } = await sideLaunchRecovery.captureForClose();
	if (generation !== daemonAttachmentGeneration || !sideLaunchRecovery.matches(attachment)) throw new Error("Side chat daemon attachment changed. Retry after reconnecting.");
}

ipcMain.handle("sideChats:capture", async (event, ...legacyCloseArguments: unknown[]) => {
	if (event.sender !== getShellWebContents()) throw new Error("Untrusted side chat capture request.");
	if (legacyCloseArguments.length) throw new Error("Side chat close protocol changed. Reload the desktop app.");
	return captureSideChatState();
});
ipcMain.handle("sideChats:confirmClose", () => {
	throw new Error("Side chat close protocol changed. Reload the desktop app.");
});
ipcMain.handle("sideChats:requestClose", (event, input: SideCloseRequest) => {
	if (event.sender !== getShellWebContents()) throw new Error("Untrusted side chat close request.");
	return sideCloseController.requestClose(input);
});
ipcMain.handle("sideChats:closeState", (event, sessionId: string) => {
	if (event.sender !== getShellWebContents() || typeof sessionId !== "string" || !sessionId || sessionId.length > 255) throw new Error("Invalid side close query.");
	return sideCloseController.summary(sessionId);
});

ipcMain.handle("editorHandoff:pathAction", (event, input) => {
	if (event.sender !== getShellWebContents()) throw new Error("Untrusted editor handoff request.");
	return editorHandoff.pathAction(input && typeof input === "object" ? input : {});
});

ipcMain.handle("editorHandoff:getState", (event, sessionId: string, options) => {
	if (event.sender !== getShellWebContents()) throw new Error("Untrusted editor handoff request.");
	return editorHandoff.getState(typeof sessionId === "string" ? sessionId : "", { refreshTerminals: options?.refreshTerminals === true });
});
ipcMain.handle("editorHandoff:getEditors", (event) => {
	if (event.sender !== getShellWebContents()) throw new Error("Untrusted editor handoff request.");
	return editorHandoff.getEditors();
});
ipcMain.handle("editorHandoff:setPreferredEditor", (event, editorId: unknown) => {
	if (event.sender !== getShellWebContents()) throw new Error("Untrusted editor handoff request.");
	return editorHandoff.setPreferredEditor(editorId);
});
ipcMain.handle("editorHandoff:open", (event, input) => {
	if (event.sender !== getShellWebContents()) throw new Error("Untrusted editor handoff request.");
	return editorHandoff.open(input && typeof input === "object" ? input : { sessionId: "" });
});
ipcMain.handle("editorHandoff:openArtifacts", (event, input) => {
	if (event.sender !== getShellWebContents()) throw new Error("Untrusted editor handoff request.");
	return editorHandoff.openArtifacts(input && typeof input === "object" ? input : { sessionId: "", targetId: "file-manager" });
});
ipcMain.handle("editorHandoff:openArtifactInBrowser", (event, input) => {
	if (event.sender !== getShellWebContents()) throw new Error("Untrusted editor handoff request.");
	return editorHandoff.openArtifactInBrowser(input && typeof input === "object" ? input : { sessionId: "", path: "" });
});
ipcMain.handle("editorHandoff:copyWorkspacePath", (event, sessionId: string) => {
	if (event.sender !== getShellWebContents()) throw new Error("Untrusted editor handoff request.");
	return editorHandoff.copyWorkspacePath(typeof sessionId === "string" ? sessionId : "");
});
ipcMain.handle("app:getVersion", () => app.getVersion());
async function triageEnv(): Promise<NodeJS.ProcessEnv> {
	await ensureShellEnv();
	const env = process.platform === "win32"
		? { ...process.env, ...(cachedShellEnv ?? {}) }
		: buildDaemonEnv(process.env, cachedShellEnv, {}, configuredLoginShell);
	return {
		...env,
		AO_DEV_DAEMON_BINARY: daemonLaunchEnv().AO_DEV_DAEMON_BINARY,
		AO_DATA_DIR: path.resolve(desktopDataDir),
		AO_RUN_FILE: path.resolve(runFilePath() ?? path.join(desktopDataDir, "running.json")),
	};
}
ipcMain.handle("triage.launch", async () => launchTriageInTerminal(await triageEnv()));

const dictation = registerDictationIpc(
	path.join(
		app.isPackaged ? process.resourcesPath : app.getAppPath(),
		"dictation-helper", "Orchestrator.app", "Contents", "MacOS", "Orchestrator",
	),
	getShellWebContents,
);
ipcMain.handle("app:openExternal", async (_event, url: string) => {
	await openAllowedAppExternalURL(url, shell);
});
ipcMain.handle("app:openChromeExtensions", () =>
	openChromeExtensionsPage(process.platform, (file, args) => promisify(execFile)(file, args)),
);

let privacyGuideContents: WebContents | undefined;
const privacyGuideIcons = new Map<string, Electron.NativeImage>();
function orchestratorApp(): PrivacyGuideApp | undefined {
	const bundle = appBundlePath(process.execPath);
	return bundle ? { name: app.getName(), path: bundle } : undefined;
}
const privacyGuide = new PrivacyGuide({
	createWindow: () => {
		const window = new BrowserWindow({
			width: 600,
			height: PRIVACY_GUIDE_HEIGHT,
			type: process.platform === "darwin" ? "panel" : undefined,
			show: false,
			frame: false,
			transparent: true,
			resizable: false,
			movable: false,
			minimizable: false,
			maximizable: false,
			fullscreenable: false,
			skipTaskbar: true,
			alwaysOnTop: true,
			hasShadow: true,
			webPreferences: { preload: privacyGuidePreloadPath(), sandbox: true, contextIsolation: true, nodeIntegration: false },
		});
		window.setAlwaysOnTop(true, "floating");
		window.setVisibleOnAllWorkspaces(true);
		const contents = window.webContents;
		contents.setWindowOpenHandler(() => ({ action: "deny" }));
		privacyGuideContents = contents;
		return {
			loadURL: (url) => window.loadURL(url),
			setBounds: (bounds) => window.setBounds(bounds),
			showInactive: () => window.showInactive(),
			hide: () => window.hide(),
			isVisible: () => window.isVisible(),
			isFocused: () => window.isFocused(),
			close: () => window.close(),
			isDestroyed: () => window.isDestroyed(),
			onNavigate: (handler) => contents.on("will-navigate", (event, url) => {
				event.preventDefault();
				handler(url);
			}),
			onClosed: (handler) => window.on("closed", () => {
				if (privacyGuideContents === contents) privacyGuideContents = undefined;
				handler();
			}),
		};
	},
	openExternal: (url) => openAllowedAppExternalURL(url, shell),
	appFor: (target) => (target === "orchestrator" ? orchestratorApp() : { name: "CuaDriver", path: CUA_DRIVER_APP_PATH }),
	readIcon: async (appPath) => {
		const icon = await nativeImage.createThumbnailFromPath(appPath, { width: 128, height: 128 }).catch(() => undefined);
		const image = icon && !icon.isEmpty() ? icon : await app.getFileIcon(appPath, { size: "normal" });
		privacyGuideIcons.set(appPath, image.resize({ width: 64, height: 64 }));
		return image.toDataURL();
	},
	watchSettings: watchSettingsWindow,
	workAreaFor: (bounds) => screen.getDisplayMatching(bounds).workArea,
	fallbackBounds: () => {
		const { workArea } = screen.getDisplayNearestPoint(screen.getCursorScreenPoint());
		const width = Math.min(600, workArea.width - 32);
		return { x: workArea.x + Math.round((workArea.width - width) / 2), y: workArea.y + workArea.height - PRIVACY_GUIDE_HEIGHT - 24, width, height: PRIVACY_GUIDE_HEIGHT };
	},
	ownPid: process.pid,
});
ipcMain.on("privacyGuide:startDrag", (event) => {
	const target = privacyGuide.currentApp();
	const icon = target && privacyGuideIcons.get(target.path);
	if (!privacyGuideContents || event.sender !== privacyGuideContents || !target || !icon || icon.isEmpty()) return;
	event.sender.startDrag({ file: target.path, icon });
});
const readOrchestratorPrivacy = () => readAppPrivacy(execFileAsync);
ipcMain.handle("app:getAppPrivacy", async (event): Promise<AppPrivacyGrants | undefined> => {
	if (event.sender !== getShellWebContents()) throw new Error("Untrusted app privacy request.");
	if (process.platform !== "darwin") return undefined;
	return readOrchestratorPrivacy();
});
ipcMain.handle("app:requestAppPrivacy", async (event): Promise<AppPrivacyGrants | undefined> => {
	if (event.sender !== getShellWebContents()) throw new Error("Untrusted app privacy request.");
	if (process.platform !== "darwin") return undefined;
	const granted = await readOrchestratorPrivacy();
	if (!granted) return undefined;
	await requestAppPrivacy(granted, {
		accessibility: () => { systemPreferences.isTrustedAccessibilityClient(true); },
		screenRecording: async () => { await desktopCapturer.getSources({ types: ["screen"], thumbnailSize: { width: 0, height: 0 } }).catch(() => undefined); },
	});
	return readOrchestratorPrivacy();
});
ipcMain.handle("app:showPrivacyGuide", async (event, input: unknown) => {
	if (event.sender !== getShellWebContents()) throw new Error("Untrusted privacy guide request.");
	if (process.platform !== "darwin") return;
	const request = coercePrivacyGuideRequest(input);
	if (!request) throw new Error("Invalid privacy guide request.");
	await privacyGuide.show(request);
});
ipcMain.handle("app:updatePrivacyGuide", async (event, target: unknown, granted: Partial<Record<PrivacyPane, unknown>>) => {
	if (event.sender !== getShellWebContents()) throw new Error("Untrusted privacy guide request.");
	if (!isPrivacyGuideTarget(target)) throw new Error("Invalid privacy guide request.");
	return privacyGuide.update(target, { accessibility: granted?.accessibility === true, screenRecording: granted?.screenRecording === true });
});
ipcMain.handle("app:closePrivacyGuide", (event) => {
	if (event.sender !== getShellWebContents()) throw new Error("Untrusted privacy guide request.");
	privacyGuide.close();
});

ipcMain.handle("window:getZoomFactor", () => {
	const shell = getShellWebContents();
	if (process.platform === "darwin" && mainWindow && shell) syncMacWindowButtons(mainWindow, shell);
	return shell?.getZoomFactor() ?? 1;
});
ipcMain.handle("window:isFullScreen", () => mainWindow?.isFullScreen() ?? false);
ipcMain.handle("window:isMaximized", () => mainWindow?.isMaximized() ?? false);

ipcMain.handle("theme:set", (_event, preference: "light" | "dark" | "system") => {
	if (preference === "light" || preference === "dark" || preference === "system") {
		nativeTheme.themeSource = preference;
		syncNativeWindowBackground();
	}
});

ipcMain.handle("theme:set-window-background", (_event, color: unknown) => {
	if (typeof color === "string" && /^#[0-9a-f]{6}$/i.test(color)) {
		rendererWindowBackground = color;
		syncNativeWindowBackground();
	}
});

ipcMain.handle("theme:set-window-translucent", (_event, enabled: unknown) => {
	if (process.platform !== "darwin" || typeof enabled !== "boolean") return;
	if (!mainWindow || mainWindow.isDestroyed()) return;
	windowTranslucent = enabled;
	syncNativeWindowBackground(true);
});

ipcMain.handle("theme:persist-terminal",(_event, scheme: unknown) => {
	if (scheme === "light" || scheme === "dark") {
		persistTerminalThemeHint(scheme);
	}
});

ipcMain.on("shell:focus", () => browserViewHost?.forgetLastFocusedPanel());

ipcMain.handle("browser:runtime:reconnect", (event) => {
		if (event.sender !== getShellWebContents()) throw new Error("Untrusted browser runtime request.");
		reconnectBrowserRuntimeLink();
});

ipcMain.handle("browser:runtime:state", (event): BrowserRuntimeState => {
	if (event.sender !== getShellWebContents()) throw new Error("Untrusted browser runtime request.");
	return browserRuntimeLink?.state ?? BROWSER_RUNTIME_DISCONNECTED;
});

ipcMain.on("browser:overlay", (event, open: unknown) => {
	if (event.sender !== getShellWebContents() || typeof open !== "boolean") return;
	windowComposition?.setOverlayOpen(open);
	if (open && process.platform === "darwin") {
		browserViewHost?.refreshLastFocusedPanelSurface();
	}
});

ipcMain.on(SET_CLOSE_SHELL_TERMINAL_SHORTCUT_ENABLED_CHANNEL, (_event, enabled: unknown) => {
	closeShellTerminalShortcutEnabled = enabled === true;
});

ipcMain.on(SET_APP_MENU_AVAILABILITY_CHANNEL, (event, availability: unknown) => {
	if (event.sender !== getShellWebContents()) return;
	applyAppMenuAvailability(coerceAppMenuAvailability(availability));
});

ipcMain.on(QUIT_CONFIRM_RESULT_CHANNEL, (event, confirmed: unknown) => {
	if (event.sender !== getShellWebContents() || typeof confirmed !== "boolean") return;
	quitConfirm.resolve(confirmed);
});

ipcMain.on(SET_TERMINAL_FOCUSED_CHANNEL, (event, focused: unknown, handlesFontSize: unknown) => {
	if (event.sender !== getShellWebContents() || typeof focused !== "boolean") return;
	terminalFocused = focused;
	terminalFontSizeFocused = focused && handlesFontSize === true;
});

ipcMain.on(SET_ARTIFACT_ZOOM_ENGAGED_CHANNEL, (event, engaged: unknown) => {
	if (event.sender !== getShellWebContents() || typeof engaged !== "boolean") return;
	artifactZoomEngaged = engaged;
});

ipcMain.on(SET_CHAT_DRAFT_RISK_CHANNEL, (event, risks: unknown, dialogCopy: unknown) => {
	if (event.sender !== getShellWebContents()) return;
	const parsed = parseChatDraftBoundaryKinds(risks);
	const parsedCopy = parseChatDraftDialogCopy(dialogCopy);
	if (!parsed || (parsed.length > 0 && !parsedCopy)) return;
	if (
		chatDraftRisks.length === parsed.length &&
		chatDraftRisks.every((risk, index) => risk === parsed[index]) &&
		JSON.stringify(chatDraftDialog) === JSON.stringify(parsedCopy)
	) return;
	chatDraftRisks = [...parsed];
	chatDraftDialog = parsedCopy;
	chatDraftQuitConfirmed = false;
	chatDraftWindowCloseConfirmed = false;
});

ipcMain.handle("menu:action", (event, action: string) => {
	if (event.sender !== getShellWebContents()) return;
	const win = mainWindow;
	if (!win) return;
	const focused = webContents.getFocusedWebContents();
	const shell = getShellWebContents();
	const wc =
		(focused && focused !== shell ? focused : browserViewHost?.getLastFocusedPanelContents()) ?? shell;
	if (!wc) return;
	switch (action) {
		case "edit.undo":
			return wc.undo();
		case "edit.redo":
			return wc.redo();
		case "edit.cut":
			return wc.cut();
		case "edit.copy":
			return wc.copy();
		case "edit.paste":
			return wc.paste();
		case "edit.selectAll":
			return wc.selectAll();
		case "view.reload":
			return reloadAppView(browserViewHost, getShellWebContents, isDev);
		case "view.devtools":
			return toggleAppDevTools(browserViewHost, getShellWebContents, isDev);
		case "view.zoomIn":
			wc.setZoomLevel(wc.getZoomLevel() + 0.5);
			if (process.platform === "darwin" && wc === getShellWebContents()) syncMacWindowButtons(win, wc);
			wc.send("window:zoom", wc.getZoomFactor());
			return;
		case "view.zoomOut":
			wc.setZoomLevel(wc.getZoomLevel() - 0.5);
			if (process.platform === "darwin" && wc === getShellWebContents()) syncMacWindowButtons(win, wc);
			wc.send("window:zoom", wc.getZoomFactor());
			return;
		case "view.zoomReset":
			wc.setZoomLevel(0);
			if (process.platform === "darwin" && wc === getShellWebContents()) syncMacWindowButtons(win, wc);
			wc.send("window:zoom", wc.getZoomFactor());
			return;
		case "view.fullscreen":
			return win.setFullScreen(!win.isFullScreen());
		case "window.minimize":
			return win.minimize();
		case "window.maximize":
			return win.isMaximized() ? win.unmaximize() : win.maximize();
		case "window.close":
			return win.close();
		case "app.quit":
			return app.quit();
		case "help.shortcuts":
			shell?.focus();
			return shell?.send(KEYBOARD_SHORTCUTS_HELP_CHANNEL);
		case "help.about":
			void dialog.showMessageBox(win, {
				type: "info",
				title: `About ${APP_DISPLAY_NAME}`,
				message: APP_DISPLAY_NAME,
				detail: `Version ${app.getVersion()}`,
				buttons: ["OK"],
			});
			return;
	}
});
async function daemonLifetimeSessions(): Promise<number | null> {
	if (daemonStatus.state !== "ready" || !daemonStatus.port) return null;
	try {
		const response = await fetch(`http://127.0.0.1:${daemonStatus.port}/api/v1/telemetry/identity`, { signal: AbortSignal.timeout(1500) });
		const identity = response.ok ? await response.json() as { lifetimeSessions?: unknown } : {};
		return typeof identity.lifetimeSessions === "number" && Number.isSafeInteger(identity.lifetimeSessions) && identity.lifetimeSessions >= 0 ? identity.lifetimeSessions : null;
	} catch {
		return null;
	}
}
ipcMain.handle("telemetry:getBootstrap", async () => {
	if (!telemetryPolicyController) return null;
	if (telemetryIdentity.isOptedOut()) return null;
	const runFile = runFilePath();
	const install = await readTelemetryInstallContext({
		stateDir: runFile ? path.dirname(runFile) : null,
		arch: process.arch,
		osVersion: process.getSystemVersion(),
		env: process.env,
		exists: existsSync,
	});
	const bootstrap = await buildTelemetryBootstrap({ ...process.env, AO_DATA_DIR: desktopDataDir }, app.getVersion(), process.platform, os.homedir(), app.isPackaged, telemetryPolicyController.snapshot(), install);
	if (!bootstrap) return bootstrap;
	const lifetimeSessions = await daemonLifetimeSessions();
	if (lifetimeSessions !== null) bootstrap.lifetimeSessions = lifetimeSessions;
	const stored = await readAuthStore(cloudDataDir()).catch(() => null);
	const cloudUserId = stored?.session?.authProvider === "workos" ? stored.session.user.id : null;
	if (stored && !stored.session) await telemetryIdentity.retireSignedOutBinding();
	if (cloudUserId && cloudUserId === await boundCloudUserId(desktopDataDir)) bootstrap.cloudUserId = cloudUserId;
	return bootstrap;
});
ipcMain.handle("telemetry:getAnalyticsOptOut", () => telemetryIdentity.isOptedOut());
async function flushDaemonUsage(): Promise<void> {
	if (daemonStatus.state !== "ready" || !daemonStatus.port) return;
	try {
		await fetch(`http://127.0.0.1:${daemonStatus.port}/internal/telemetry/usage-flush`, {
			method: "POST", signal: AbortSignal.timeout(3000),
		});
	} catch (error) { console.warn("AO: usage telemetry flush failed", error); }
}

ipcMain.handle("telemetry:setAnalyticsOptOut", async (event, optedOut: unknown) => {
	const trustedSender = trustedShellWebContents.get(event.sender.id);
	if (trustedSender !== event.sender || typeof optedOut !== "boolean") throw new Error("invalid analytics opt-out request");
	const result = await telemetryIdentity.setOptedOut(optedOut);
	if (optedOut) void flushDaemonUsage();
	return result;
});
ipcMain.handle("telemetry:setCloudUser", (event, userId: unknown) => {
	const trustedSender = trustedShellWebContents.get(event.sender.id);
	if (trustedSender !== event.sender || (userId !== null && typeof userId !== "string")) throw new Error("invalid cloud user request");
	return telemetryIdentity.setCloudUser(userId);
});
ipcMain.handle("telemetry:getGithubLogin", () => telemetryIdentity.githubLogin());
ipcMain.handle("telemetry:getLifetimeSessions", () => daemonLifetimeSessions());
ipcMain.handle("telemetry:claimFirstOpen", async () => {
	if (telemetryIdentity.isOptedOut()) return "ineligible";
	let daysSinceInstall: number | undefined;
	try {
		const installedAt = (await stat(path.join(desktopDataDir, "telemetry_install_id"))).mtimeMs;
		if (installedAt > 0 && installedAt <= Date.now()) daysSinceInstall = Math.floor((Date.now() - installedAt) / 86_400_000);
	} catch {
		return "ineligible";
	}
	const lifetimeSessions = await daemonLifetimeSessions();
	return claimFirstOpen(desktopDataDir, { daysSinceInstall, lifetimeSessions: lifetimeSessions ?? undefined });
});
ipcMain.handle("telemetry:getPolicy", () => telemetryPolicyController?.snapshot() ?? failClosedTelemetryPolicyView());
ipcMain.handle("telemetry:setEventsEnabled", (_event, input: { eventsEnabled?: unknown; expectedGeneration?: unknown }) => {
	if (!telemetryPolicyController || typeof input?.eventsEnabled !== "boolean" || typeof input.expectedGeneration !== "string") throw new Error("invalid telemetry policy request");
	return telemetryPolicyController.setEventsEnabled(input.eventsEnabled, input.expectedGeneration);
});
ipcMain.on(TELEMETRY_RENDERER_QUEUES_CLEARED_CHANNEL, (event, input: unknown) => {
	const trustedSender = trustedShellWebContents.get(event.sender.id);
	if (!trustedSender || trustedSender !== event.sender || trustedSender.isDestroyed()) return;
	if (!input || typeof input !== "object" || Array.isArray(input)) return;
	const result = input as Record<string, unknown>;
	if (Object.keys(result).length !== 2 || typeof result.requestId !== "string" || typeof result.ok !== "boolean") return;
	const pending = pendingRendererQueuePurges.get(result.requestId);
	if (!pending || pending.senderId !== event.sender.id) return;
	finishRendererQueuePurge(result.requestId, result.ok ? undefined : new Error("renderer telemetry queue purge failed"));
});
ipcMain.handle("telemetry:capture", (_event, request: RendererTelemetryCapture) => {
	const sanitized = sanitizeRendererCapture(request);
	if (!telemetryPolicyController || !sanitized) return false;
	return telemetryPolicyController.capture(sanitized);
});
function failClosedTelemetryPolicyView(): TelemetryPolicyView {
	return { eventsEnabled: false, consentGeneration: "unavailable", updatedAt: new Date(0).toISOString(), acknowledged: false, state: "cleanup_failed", environmentVeto: true, durabilitySupported: false, reason: "invalid_authority" };
}
async function chooseDirectory(title: string, defaultPath?: string): Promise<string | null> {
	if (defaultPath) await mkdir(defaultPath, { recursive: true });
	const options: OpenDialogOptions = {
		defaultPath,
		properties: ["openDirectory", "createDirectory"],
		title,
	};
	const result = await dialog.showOpenDialog(options);

	if (result.canceled) return null;
	const chosen = result.filePaths[0] ?? null;
	if (chosen) rememberChosenFolder(chosen);
	return chosen;
}

const remoteRegistry = new RemoteRegistry((entry) => {
	const devUrl = typeof MAIN_WINDOW_VITE_DEV_SERVER_URL === "undefined" ? undefined : MAIN_WINDOW_VITE_DEV_SERVER_URL;
	return startRemoteProxy(entry, devUrl ? new URL(devUrl).origin : RENDERER_ORIGIN);
});
setPasswordCipher(() => protectedStorageAvailable() ? {
	encrypt: (text) => safeStorage.encryptString(text).toString("base64"),
	decrypt: (sealed) => safeStorage.decryptString(Buffer.from(sealed, "base64")),
} : null);
void app.whenReady().then(() => sealSavedPasswords(remotesFilePath())).catch((error: unknown) => {
	console.warn("[remotes] could not encrypt saved host passwords", error);
});
registerRemotesIpc(ipcMain, {
	file: remotesFilePath(),
	registry: remoteRegistry,
	requireAccount: async () => {
		if (!await getCloudSession(cloudDataDir())) throw new ExpectedIpcError("Sign in to Orchestrator Cloud to use remote hosts.");
	},
	getAccountId: async () => (await getCloudSession(cloudDataDir()))?.user.id ?? "",
	cloudRequest: async (method, path, { body, signal } = {}) => {
		const baseUrl = await cloudControlPlaneUrl();
		if (!baseUrl) throw new ExpectedIpcError("No Orchestrator Cloud control plane is configured.");
		return cloudCpProxy.request({
			baseUrl, path, method,
			...(body === undefined ? {} : { headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }),
		}, { hostTokens: true, signal });
	},
	localIdentity: createLocalHostIdentity(() => daemonStatus.state === "ready" && daemonStatus.port
		? { port: daemonStatus.port, pid: daemonStatus.pid, generation: daemonAttachmentGeneration }
		: null),
});

ipcMain.handle("app:chooseDirectory", async (_event, input?: string | { title?: string; defaultPath?: string }) => {
	const title = typeof input === "string"
		? input
		: input?.title;
	const defaultPath = typeof input === "object" && input !== null && typeof input.defaultPath === "string"
		? input.defaultPath.trim()
		: "";
	return chooseDirectory(title?.trim() || "Choose a git repository", defaultPath === "~/ao/projects" ? path.join(os.homedir(), "ao", "projects") : undefined);
});
ipcMain.handle("app:checkGitRepository", async (_event, remoteUrl: string) => {
	await ensureShellEnv();
	try {
		await execFileAsync("git", ["ls-remote", "--quiet", remoteUrl, "HEAD"], {
			env: daemonEnv(),
			timeout: 8000,
		});
		return true;
	} catch {
		return false;
	}
});
ipcMain.handle("app:scanImportFolder", async (_event, input: { path: string; mode: "project" | "workspace" }) => {
	await ensureShellEnv();
	return scanImportFolder(input.path, input.mode, { env: daemonEnv(), homeDir: os.homedir() });
});
ipcMain.handle("app:checkAncestorRepo", async (_event, path: string) => {
	await ensureShellEnv();
	return ancestorRepositorySetupWarning(path, { env: daemonEnv(), homeDir: os.homedir() });
});
ipcMain.handle("app:getRepositoryBranch", async (_event, path: string) => {
	await ensureShellEnv();
	return resolveCheckedOutBranch(path, { env: daemonEnv(), homeDir: os.homedir() });
});
ipcMain.handle("app:getGitHubLogin", async (_event, repoPath?: string) => {
	await ensureShellEnv();
	const gitConfig = async (args: string[]) => {
		try {
			const { stdout } = await execFileAsync("git", args, { env: daemonEnv(), timeout: 3000 });
			return stdout.trim();
		} catch {
			return "";
		}
	};
	const candidates = [
		typeof repoPath === "string" && repoPath.trim() ? await gitConfig(["-C", repoPath.trim(), "config", "--get", "github.user"]) : "",
		await gitConfig(["config", "--global", "--get", "github.user"]),
		process.env.AO_GITHUB_LOGIN?.trim() ?? "",
	];
	try {
		const { stdout } = await execFileAsync("gh", ["api", "user", "--jq", ".login"], {
			env: daemonEnv(),
			timeout: 5000,
		});
		candidates.push(stdout.trim());
	} catch {
	}
	const gitNames = [
		typeof repoPath === "string" && repoPath.trim() ? await gitConfig(["-C", repoPath.trim(), "config", "--get", "user.name"]) : "",
		await gitConfig(["config", "--global", "--get", "user.name"]),
	].filter((candidate) => /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$/.test(candidate));
	candidates.push(...gitNames);
	return candidates.find((candidate) => candidate.length > 0) ?? "";
});
type GitHubOwner = { login: string; avatarUrl: string };
let cachedGitHubOwners: GitHubOwner[] = [];

async function refreshGitHubOwners(): Promise<GitHubOwner[]> {
	await ensureShellEnv();
	try {
		const { stdout } = await execFileAsync("gh", ["api", "user", "--jq", "[.login, .avatar_url] | @tsv"], {
			env: daemonEnv(),
			timeout: 5000,
		});
		let organizationOutput = "";
		try {
			({ stdout: organizationOutput } = await execFileAsync("gh", ["api", "user/memberships/orgs", "--paginate", "--jq", ".[] | [.organization.login, .organization.avatar_url] | @tsv"], {
				env: daemonEnv(),
				timeout: 8000,
			}));
		} catch {
		}
		const owners = [stdout, ...organizationOutput.split("\n")].map((line) => {
			const [login, avatarUrl] = line.trim().split("\t");
			return login && avatarUrl ? { login, avatarUrl } : null;
		}).filter((owner): owner is GitHubOwner => owner !== null);
		cachedGitHubOwners = [...new Map(owners.map((owner) => [owner.login, owner])).values()];
		return cachedGitHubOwners;
	} catch {
		return cachedGitHubOwners;
	}
}

type GitHubRepoSummary = { fullName: string; description: string; isPrivate: boolean; url: string; avatarUrl: string };

type GitHubRepoRow = { fullName?: string; full_name?: string; description?: string | null; isPrivate?: boolean; private?: boolean; url?: string; html_url?: string; owner?: { login?: string } };

async function listGitHubRepos(query: string): Promise<GitHubRepoSummary[]> {
	await ensureShellEnv();
	const term = query.trim().slice(0, 100);
	const args = term
		? ["search", "repos", "--limit", "30", "--json", "fullName,description,isPrivate,url,owner", "--", term]
		: ["api", "user/repos?per_page=100&sort=pushed&affiliation=owner,collaborator,organization_member"];
	try {
		const { stdout } = await execFileAsync("gh", args, { env: daemonEnv(), timeout: 15000, maxBuffer: 8 * 1024 * 1024 });
		const rows = JSON.parse(stdout) as GitHubRepoRow[];
		return rows.flatMap((row) => {
			const fullName = row.fullName ?? row.full_name;
			const url = row.html_url ?? row.url;
			if (!fullName || !url) return [];
			return [{
				fullName,
				description: row.description ?? "",
				isPrivate: (row.isPrivate ?? row.private) === true,
				url,
				avatarUrl: `https://github.com/${encodeURIComponent(row.owner?.login ?? fullName.split("/")[0] ?? "")}.png?size=40`,
			}];
		});
	} catch {
		return [];
	}
}

ipcMain.handle("app:listGitHubRepos", (_event, query: unknown) => listGitHubRepos(typeof query === "string" ? query : ""));
ipcMain.handle("app:getCachedGitHubOwners", () => cachedGitHubOwners);
ipcMain.handle("app:refreshGitHubOwners", () => refreshGitHubOwners());
ipcMain.handle("app:checkGitHubRepositoryAvailability", async (_event, input: { owner: string; name: string }) => {
	await ensureShellEnv();
	const owner = input.owner.trim();
	const name = input.name.trim();
	if (!owner || !name) {
		return { available: false, message: "Owner and repository name are required." };
	}
	try {
		await execFileAsync("gh", ["api", `repos/${owner}/${name}`], {
			env: daemonEnv(),
			timeout: 8000,
		});
		return { available: false, message: "Repository name is already in use for this owner." };
	} catch (error) {
		const output = error instanceof Error ? error.message : String(error);
		if (/404|not found/i.test(output)) {
			return { available: true };
		}
		return { available: false, message: "Could not check this repository name. Confirm GitHub CLI is signed in." };
	}
});
ipcMain.handle("clipboard:writeText", async (_event, text: string) => {
	await clipboard.writeText(text);
	if (process.platform === "linux") {
		await clipboard.selection.writeText(text);
	}
});
ipcMain.handle("clipboard:readText", () => clipboard.readText());
registerChatImageIpc({
	ipcMain,
	clipboard: { writeImage: writeClipboardImage },
	createImage: (bytes) => nativeImage.createFromBuffer(bytes),
	dialog,
	downloadsDirectory: () => app.getPath("downloads"),
	appName: app.getName(),
});
const artifactSnapshotRenderer = createArtifactSnapshotRenderer({
	BrowserWindow,
	preload: preloadPath(),
	pageUrl: () => new URL("artifact-snapshot.html", rendererUrl()).href,
	shellOpen: () => {
		const contents = getShellWebContents();
		return contents !== null && !contents.isDestroyed();
	},
});
const artifactSnapshotStore = createArtifactSnapshotStore({
	directory: path.join(desktopDataDir, "artifact-previews"),
	render: artifactSnapshotRenderer.render,
});
registerArtifactSnapshotIpc({ ipcMain, store: artifactSnapshotStore });
registerCloudArtifactIpc({
	ipcMain,
	openInBrowser: (target) => openFileInDefaultBrowser(target),
	openInFileManager: async (target, isFile) => {
		if (isFile) shell.showItemInFolder(target);
		else {
			const error = await shell.openPath(target);
			if (error) throw new Error(error);
		}
	},
});
void pruneCloudArtifactFolders().catch((error) => console.warn("AO: unable to prune old cloud artifact copies", error));

const shareSnapshotDir = path.join(desktopDataDir, "tmp", "share-uploads");
void clearShareSnapshots(shareSnapshotDir).catch(() => undefined);
registerCloudShareIpc({
	ipcMain,
	daemonDataDir: resolvedDaemonDataDir,
	snapshotDir: () => shareSnapshotDir,
	getToken: () => getCloudAccessToken(cloudDataDir()),
	fetch: (url, init) => net.fetch(url, init),
	records: new ShareLinkRecords(path.join(desktopDataDir, "share-links.json")),
});
const sessionShareUrls = new SessionShareUrls(path.join(desktopDataDir, "session-share-links.bin"), {
	available: protectedStorageAvailable,
	encrypt: (text) => safeStorage.encryptString(text),
	decrypt: (value) => safeStorage.decryptString(value),
}, async () => (await readAuthStore(cloudDataDir())).session?.user.id);
registerSessionShareUrlsIpc(ipcMain, sessionShareUrls);
registerAgentLoginIpc({
	ipcMain,
	daemonPort: () => (daemonStatus.state === "ready" ? daemonStatus.port : undefined),
	trustedSender: (sender) => sender === getShellWebContents(),
});

ipcMain.handle("terminal:saveDroppedFile", async (_event, input: { name: string; bytes: Uint8Array }) => {
	const dir = path.join(app.getPath("userData"), "terminal-drops");
	await mkdir(dir, { recursive: true });
	const base = path.basename(input.name || "").replace(/[^\w.-]+/g, "_") || "dropped";
	const target = path.join(dir, `${Date.now()}-${base}`);
	await writeFile(target, Buffer.from(input.bytes));
	return target;
});

ipcMain.handle("appState:getMigration", async (): Promise<MigrationState> => {
	const runFile = runFilePath();
	if (!runFile) return { status: "pending" };
	return readMigrationState(path.dirname(runFile));
});
ipcMain.handle("appState:setMigration", async (_event, migration: MigrationState) => {
	const runFile = runFilePath();
	if (!runFile) return;
	await updateMigration({ stateDir: path.dirname(runFile), migration, now: () => new Date() });
});
ipcMain.handle("appState:getFirstRunProjectPrompt", async (): Promise<"pending" | "seen"> => {
	const runFile = runFilePath();
	return runFile ? readFirstRunProjectPrompt(path.dirname(runFile)) : "seen";
});
ipcMain.handle("appState:getExperimentCohort", async (): Promise<number | null> => {
	return (await loadOrAssignExperimentCohort(desktopDataDir)) ?? null;
});
ipcMain.handle("appState:claimFirstRunExperimentExposure", async (): Promise<boolean> => {
	const runFile = runFilePath();
	return runFile ? claimFirstRunExperimentExposure(path.dirname(runFile)) : false;
});
ipcMain.handle("appState:markFirstRunProjectPromptSeen", async () => {
	const runFile = runFilePath();
	if (!runFile) return;
	await markFirstRunProjectPromptSeen(path.dirname(runFile));
});

ipcMain.handle("updateSettings:get", async (): Promise<UpdateSettings> => {
	const runFile = runFilePath();
	if (!runFile) return { enabled: false, channel: "latest", nightlyAck: false, feature: null, macDifferentialUpdates: false };
	return readUpdateSettings(path.dirname(runFile));
});
ipcMain.handle("updateSettings:set", async (_event, settings: UpdateSettings) => {
	const runFile = runFilePath();
	if (!runFile) return;
	await setUpdateSettings(path.dirname(runFile), settings);
});
ipcMain.handle("updateSettings:setMacDifferentialUpdates", async (_event, enabled: unknown) => {
	if (typeof enabled !== "boolean") return;
	const runFile = runFilePath();
	if (!runFile) return;
	await setMacDifferentialUpdates(path.dirname(runFile), enabled);
});

ipcMain.handle("uiSettings:get", async (): Promise<UiSettings> => {
	const runFile = runFilePath();
	if (!runFile) return { ...DEFAULT_UI_SETTINGS };
	return readUiSettings(path.dirname(runFile));
});
	ipcMain.handle("uiSettings:set", async (_event, settings: Partial<UiSettings>): Promise<UiSettings> => {
		const runFile = runFilePath();
	const result = !runFile ? coerceUiSettings(settings) : await writeUiSettings(path.dirname(runFile), settings);
	trayController?.setLocale(result.locale);
	soundNotificationsEnabled = result.soundNotificationsEnabled;
	terminalShellPreference = result.terminalShell;
	shellEnvPromise = null;
	cachedShellEnv = null;
	return result;
	});

ipcMain.handle("keybindings:get", (): KeybindingOverrides => keybindingOverrides);
ipcMain.handle("keybindings:set", async (_event, overrides: KeybindingOverrides): Promise<KeybindingOverrides> => {
	const runFile = runFilePath();
	if (!runFile) return keybindingOverrides;
	keybindingOverrides = await writeKeybindingOverrides(path.dirname(runFile), overrides);
	if (process.platform === "darwin" && getShellWebContents()) installMacAppMenu();
	return keybindingOverrides;
});
ipcMain.handle("keybindings:setRecording", (event, active: unknown): void => {
	if (event.sender !== getShellWebContents() || typeof active !== "boolean") return;
	keybindingRecordingActive = active;
});

ipcMain.handle("featureBuilds:list", () => listFeatureBuilds());
ipcMain.handle("featureBuilds:getActive", () => getActiveFeatureBuild());

ipcMain.handle("updates:getStatus", (): UpdateStatus => getUpdateStatus());
ipcMain.handle("updates:check", async (_event, options?: UpdateCheckOptions) => {
	const runFile = runFilePath();
	if (!runFile) return;
	await checkForUpdatesNow(path.dirname(runFile), options);
});
ipcMain.handle("updates:returnHome", async (_event, requestId?: string) => {
	const runFile = runFilePath();
	if (!runFile) return;
	await returnToHome(path.dirname(runFile), requestId);
});
ipcMain.handle("updates:download", async (_event, requestId?: string) => {
	await downloadUpdateNow(requestId);
});
ipcMain.handle("updates:install", (_event, confirmedVersion?: string) => quitAndInstallUpdate(confirmedVersion));
ipcMain.handle("updates:relaunch", () => {
	recordUpdateDiagnostic("retry-relaunch-requested");
	preserveAgentsOnQuit = true;
	app.relaunch();
	desktopQuit.quitCompletely();
});

let postUpdateRelaunchPromise: Promise<boolean> | undefined;
function detectPostUpdateRelaunch(): Promise<boolean> {
	if (!postUpdateRelaunchPromise) {
		const runFile = runFilePath();
		postUpdateRelaunchPromise =
			app.isPackaged && runFile
				? consumeUpdateRelaunchFlag({ stateDir: path.dirname(runFile), version: app.getVersion() }).catch(() => false)
				: Promise.resolve(false);
	}
	return postUpdateRelaunchPromise;
}
ipcMain.handle("updates:isPostUpdateRelaunch", () => detectPostUpdateRelaunch());

function cancelDockBounce(): void {
	if (pendingBounce === null) return;
	const { id } = pendingBounce;
	pendingBounce = null;
	app.dock?.cancelBounce(id);
}

ipcMain.handle(
	"notifications:show",
	(_event, notification: { id: string; title: string; body?: string; type?: string; watched?: boolean }) => {
		if (!notification.id || !mainWindow) return;
		const looking = mainWindow.isFocused() || notification.watched === true;
		const playsSound =
			shouldSignalAttention(notification.type) &&
			soundNotificationsEnabled &&
			(process.platform === "linux" || !looking);
		if (playsSound) playNotificationSound();
		if (looking) return;
		if (shouldToast(notification, ElectronNotification.isSupported())) {
			const toast = new ElectronNotification({
				title: notification.title,
				body: notification.body,
				silent: toastSilent(process.platform, soundNotificationsEnabled, playsSound),
				icon: process.platform === "darwin" ? undefined : windowIconPath(),
			});
			toast.on("click", () => {
				if (!mainWindow) return;
				if (mainWindow.isMinimized()) mainWindow.restore();
				mainWindow.show();
				mainWindow.focus();
				getShellWebContents()?.send("notifications:click", notification.id);
			});
			toast.show();
		}

		if (process.platform === "darwin" && app.dock) {
			if (shouldReplaceBounce(pendingBounce)) {
				const hadPendingBounce = pendingBounce !== null;
				cancelDockBounce();
				const bounceType = dockBounceType(notification.type);
				const id = app.dock.bounce(bounceType);
				if (typeof id === "number" && id >= 0) {
					pendingBounce = { id, critical: bounceType === "critical" };
					if (!hadPendingBounce) mainWindow.once("focus", cancelDockBounce);
				}
			}
		} else if ((process.platform === "win32" || process.platform === "linux") && shouldSignalAttention(notification.type)) {
			if (!isFlashing) {
				isFlashing = true;
				mainWindow.flashFrame(true);
				mainWindow.once("focus", () => {
					isFlashing = false;
					mainWindow?.flashFrame(false);
				});
			}
		}
	},
);

let launchTimingLogged = false;
ipcMain.on("app:sessionsShown", (event) => {
	if (event.sender !== getShellWebContents() || launchTimingLogged) return;
	launchTimingLogged = true;
	launchTiming.mark("sessionsShown");
	console.info(launchTiming.line());
});

ipcMain.on("notifications:soundFailed", (event) => {
	if (event.sender !== getShellWebContents()) return;
	shell.beep();
});

if (!app.isPackaged) {
	ipcMain.handle("notifications:devBounce", () => {
		if (!mainWindow) return;
		if (process.platform === "darwin") {
			const id = app.dock?.bounce("critical");
			setTimeout(() => {
				if (id !== undefined) app.dock?.cancelBounce(id);
			}, 2000);
		} else if (process.platform === "win32" || process.platform === "linux") {
			mainWindow.flashFrame(true);
			setTimeout(() => {
				mainWindow?.flashFrame(false);
			}, 2000);
		}
		if (soundNotificationsEnabled) {
			playNotificationSound();
		}
	});
}

ipcMain.handle("notifications:setBadge", (_event, count: number) => {
	if (!mainWindow) return { error: "no mainWindow" };
	const n = Number.isFinite(count) && count > 0 ? Math.floor(count) : 0;
	if (process.platform === "darwin") {
		const dock = app.dock;
		if (!dock) return { error: "no app.dock" };
		try {
			dock.setBadge(n > 0 ? String(n) : "");
		} catch (e) {
			return { error: String(e) };
		}
	} else if (process.platform === "win32") {
		if (n > 0) {
			const badgeDataUrl =
				"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAYAAAAf8/9hAAAAN0lEQVR42mNgoAW4o6b2HxumSDNRhhDSjNcQYjVjNYRUzRiGjBpABQMojkaqJCSqJGWqZCZSAQASKE/UCaPI8AAAAABJRU5ErkJggg==";
			const icon = nativeImage.createFromDataURL(badgeDataUrl);
			mainWindow.setOverlayIcon(icon, `${n} unread`);
		} else {
			mainWindow.setOverlayIcon(null, "");
		}
	} else if (process.platform === "linux") {
		return { ok: app.setBadgeCount(n) };
	}
	return { ok: true };
});

ipcMain.on(TRAY_SET_ATTENTION_STATE_CHANNEL, (event, state) => trayLifecycle.handleSetAttentionState(event, state));

ipcMain.on(TRAY_RENDERER_READY_CHANNEL, (event) => {
	trayLifecycle.handleRendererReady(event);
	if (app.isPackaged && process.platform === "darwin" && event.sender === getShellWebContents()) {
		const runFile = runFilePath();
		if (runFile) void acknowledgeMacUpdateRestart({
			stateDir: path.dirname(runFile),
			appPath: resolveBundlePath(),
			version: app.getVersion(),
		});
	}

	if (pendingFolderPath && event.sender === getShellWebContents()) {
		event.sender.send(OPEN_FOLDER_PATH_CHANNEL, pendingFolderPath);
		pendingFolderPath = null;
	}

	if (pendingSessionDeepLink && event.sender === getShellWebContents()) {
		event.sender.send(OPEN_SESSION_DEEP_LINK_CHANNEL, pendingSessionDeepLink);
		pendingSessionDeepLink = null;
	}

	if (pendingBillingSection && event.sender === getShellWebContents()) {
		event.sender.send(OPEN_SETTINGS_SHORTCUT_CHANNEL, pendingBillingSection);
		pendingBillingSection = null;
	}
});

function cloudDataDir(): string {
	return resolveCloudAuthDir(process.env, os.homedir(), desktopLaunchWorkingDirectory, !isDev);
}

const cloudControlPlaneUrl = createControlPlaneUrlResolver(() =>
	daemonStatus.state === "ready" && daemonStatus.port
		? { baseUrl: `http://127.0.0.1:${daemonStatus.port}`, key: `${daemonStatus.port}:${daemonStatus.pid ?? ""}` }
		: null);

const cloudAgentProxy = createCloudAgentProxy(cloudDataDir, cloudControlPlaneUrl);

const pendingCloudSignIn = createPendingCloudSignIn();

ipcMain.handle("cloud:takePendingSignIn", () => pendingCloudSignIn.take());

let notifiedCloudUserId: string | undefined;

function bindArtifactSnapshotsToCloudAccount(userId: string | null): void {
	artifactSnapshotStore.bindCloudAccount(userId).catch((error) => {
		console.warn("AO: could not delete the cached Cloud artifact previews", error);
	});
}

function notifyRenderersOfCloudSession(
	account: import("./shared/cloud-account").CloudAccount | null,
	signIn?: import("./shared/cloud-account").CloudSignInContext,
): void {
	pendingCloudSignIn.note(account, signIn);
	if (!account || (notifiedCloudUserId !== undefined && account.user.id !== notifiedCloudUserId)) {
		void sessionShareUrls.clear().catch(() => undefined);
	}
	notifiedCloudUserId = account?.user.id;
	bindArtifactSnapshotsToCloudAccount(account?.user.id ?? null);
	if (!account) {
		void remoteRegistry.disconnectAll();
		void cloudAgentProxy.close();
	}
	const contents = getShellWebContents();
	if (!contents || contents.isDestroyed()) return;
	contents.send("cloud:sessionChanged", account);
}

installCloudIPC(cloudDataDir, notifyRenderersOfCloudSession, async () => {
	await artifactSnapshotStore.clearCloud().catch((error) => {
		console.warn("AO: could not delete the cached Cloud artifact previews", error);
	});
	await telemetryIdentity.signOut();
});

installCloudLocalAuthIPC(cloudDataDir, notifyRenderersOfCloudSession);

const cloudCpProxy = installCloudCpProxy(cloudDataDir, cloudControlPlaneUrl);

const folderImportDir = path.join(desktopDataDir, "tmp", "folder-imports");
void clearFolderImports(folderImportDir).catch(() => undefined);
registerCloudFolderPushIpc(ipcMain, {
	controlPlaneUrl: cloudControlPlaneUrl,
	getToken: () => getCloudAccessToken(cloudDataDir()),
	tmpRoot: () => folderImportDir,
	env: () => daemonEnv(),
	homeDir: os.homedir(),
	appStateDirs: () => [desktopDataDir, app.getPath("userData")],
	ready: ensureShellEnv,
	trustedSender: (sender) => {
		const contents = sender as WebContents;
		return trustedShellWebContents.get(contents?.id) === contents;
	},
	chosenFolder: isChosenFolder,
});
ipcMain.handle("cloudCp:agentBrowserUrl", (_event, baseUrl: unknown, orgId: unknown, sessionId: unknown) =>
	cloudAgentProxy.agentBrowserUrl(baseUrl, orgId, sessionId));

function focusCloudWindow(): void {
	const window = BaseWindow.getAllWindows()[0];
	if (!window) return;
	if (window.isMinimized()) window.restore();
	window.show();
	window.focus();
}

let pendingShareLink: string | undefined;

ipcMain.handle("cloud:takePendingShareLink", () => {
	const link = pendingShareLink;
	pendingShareLink = undefined;
	return link ?? null;
});

function isShareDeepLink(raw: string): boolean {
	try {
		const url = new URL(raw);
		return url.protocol === "ao-app:" && url.hostname === "share";
	} catch {
		return false;
	}
}

async function handleCloudDeepLinkAndFocus(url: string): Promise<void> {
	if (!getShellWebContents()) void createWindow();
	focusCloudWindow();
	if (isShareDeepLink(url)) {
		const contents = getShellWebContents();
		if (contents && !contents.isDestroyed() && !contents.isLoading()) contents.send("cloud:openShareLink", url);
		else pendingShareLink = url;
		return;
	}
	if (isBillingDeepLink(url)) {
		const contents = getShellWebContents();
		if (contents && !contents.isDestroyed() && !contents.isLoading()) {
			contents.send(OPEN_SETTINGS_SHORTCUT_CHANNEL, "billing");
		} else {
			pendingBillingSection = "billing";
		}
		return;
	}
	const normalized = normalizeSessionDeepLink(url);
	if (normalized) {
		const contents = getShellWebContents()?.isDestroyed() ? null : getShellWebContents();
		pendingSessionDeepLink = null;
		if (contents && !contents.isLoading()) {
			contents.send(OPEN_SESSION_DEEP_LINK_CHANNEL, normalized);
		} else {
			pendingSessionDeepLink = normalized;
		}
		return;
	}
	try {
		const result = await handleCloudDeepLink(url, cloudDataDir());
		if (!result) return;
		notifyRenderersOfCloudSession(result.account, result.signIn);
	} catch (error) {
		console.error("WorkOS callback failed:", error);
		await showCloudSignInFailure(error);
	}
}

function isBillingDeepLink(raw: string): boolean {
	try {
		const url = new URL(raw);
		return url.protocol === "ao-app:" && url.hostname === "billing";
	} catch {
		return false;
	}
}

app.on("open-url", (event, url) => {
	event.preventDefault();
	void handleCloudDeepLinkAndFocus(url);
});

app.on("second-instance", (_event, argv) => {
	const deepLink = argv.find((value) => value.startsWith("ao-app://"));
	if (deepLink) {
		void handleCloudDeepLinkAndFocus(deepLink);
		return;
	}
	const folderPath = parseOpenFolderPathArg(argv);
	if (folderPath) {
		const window = BaseWindow.getAllWindows()[0];
		if (window) {
			if (window.isMinimized()) window.restore();
			window.show();
			window.focus();
		}
		const contents = getShellWebContents();
		if (contents) {
			contents.send(OPEN_FOLDER_PATH_CHANNEL, folderPath);
		} else {
			pendingFolderPath = folderPath;
		}
		return;
	}
	const window = BaseWindow.getAllWindows()[0];
	if (!window) return;
	if (window.isMinimized()) window.restore();
	window.show();
	window.focus();
});
function initAutoUpdates(): void {
	if (!app.isPackaged) return;
	const runFile = runFilePath();
	if (!runFile) return;
	const stateDir = path.dirname(runFile);
	setRendererSink(() => getShellWebContents());
	void ensureUpdatePrefs(stateDir).then(() => startAutoUpdates(stateDir));
}

function resolveBundlePath(): string {
	if (process.platform === "darwin") {
		return path.resolve(process.execPath, "..", "..", "..");
	}
	return process.execPath;
}

function parseInstalledVia(argv: string[]): string | undefined {
	const flag = argv.find((a) => a.startsWith("--installed-via="));
	return flag ? flag.slice("--installed-via=".length) : undefined;
}

async function writeAppStateOnLaunch(): Promise<void> {
	const runFile = runFilePath();
	if (!runFile) {
		throw new Error("cannot resolve ~/.ao run-file path; skipping app-state marker");
	}
	const stateDir = path.dirname(runFile);
	await writeAppStateMarker({
		stateDir,
		updateRestartProtocol: 1,
		appPath: resolveBundlePath(),
		version: app.getVersion(),
		installedVia: parseInstalledVia(process.argv),
		detectedInstallSource: detectInstallSource({
			platform: process.platform,
			isPackaged: app.isPackaged,
			execPath: process.execPath,
			env: process.env,
			exists: existsSync,
		}),
		now: () => new Date(),
	});
}

app.whenReady().then(async () => {
	applyUserAgentFallback(app);
	launchTiming.mark("appReady");
	void sweepTemporaryBrowserPartitions(app.getPath("userData")).then(({ removed, failed }) => {
		if (removed || failed) console.info(`browser storage: removed ${removed} leftover temporary profiles${failed ? `, ${failed} failed` : ""}`);
	});
	void purgeRootCachesOnce(session.defaultSession, app.getPath("userData")).catch((error: unknown) => {
		console.warn("browser storage: root cache purge failed; retrying next launch:", error);
	});
	powerMonitor.on("suspend", () => recordUpdateDiagnostic("power-suspend"));
	powerMonitor.on("resume", () => recordUpdateDiagnostic("power-resume"));
	void refreshGitHubOwners();
	const authority = new TelemetryPolicyAuthority({ dataDir: desktopDataDir, packagedDefault: app.isPackaged, platform: process.platform });
	const policyController = new DesktopTelemetryController({
		authority,
		environmentAllowsEvents: rendererTelemetryEnabled(process.env, app.isPackaged),
		transportFactory: () => initMainSentry(app.getVersion(), desktopDataDir, app.getPath("userData"), isDev),
		clearTransportCache: () => clearMainSentryCache(app.getPath("userData")),
		clearRendererQueues: clearRendererTelemetryQueues,
		broadcast: (view) => {
			for (const shellContents of trustedShellWebContents.values()) if (!shellContents.isDestroyed()) shellContents.send(TELEMETRY_POLICY_CHANGED_CHANNEL, view);
		},
	});
	telemetryPolicyController = policyController;
	try { await policyController.initialize(); }
	catch (error) { console.error("telemetry policy bootstrap failed; reporting remains disabled:", error); }
	setMainErrorSink((capture) => { policyController.captureMain(capture); });
	setInterval(() => { if (telemetryPolicyRetryable(policyController.snapshot())) void policyController.retryPendingCleanup(); }, 1_000).unref();
	if (parseInstalledVia(process.argv)) {
		try {
			await writeAppStateOnLaunch();
		} catch (err) {
			console.error("failed to write pre-relocation app-state marker:", err);
		}
	}

	if (process.platform === "darwin" && app.isPackaged) {
		const bundlePath = resolveBundlePath();
		const action = decideRelocation({
			inApplicationsFolder: app.isInApplicationsFolder(),
			runningVersion: app.getVersion(),
			...inspectInstalledBundle(bundlePath),
		});
		if (action === "handoff") {
			const installed = installedBundlePath(bundlePath);
			console.info(`newer install at ${installed}; handing off and quitting`);
			await shell.openPath(installed);
			app.quit();
			return;
		}
		if (action === "relocate") {
			try {
				app.moveToApplicationsFolder();
			} catch (err) {
				console.error("relocation to Applications failed:", err);
			}
		}
	}

	try {
		await writeAppStateOnLaunch();
	} catch (err) {
		console.error("failed to write app-state marker:", err);
	}

	try {
		const { formatStaleAppCopies, retireStaleMacAppCopies } = await import("./main/stale-app-copies");
		await retireStaleMacAppCopies({
			platform: process.platform,
			isPackaged: app.isPackaged,
			runningPath: resolveBundlePath(),
			runningVersion: app.getVersion(),
			confirm: async (copies) => {
				const result = await dialog.showMessageBox({
					type: "warning",
					buttons: ["Move old copies to Trash", "Not now"],
					defaultId: 0,
					cancelId: 1,
					title: "Remove old Orchestrator copies",
					message: `Old copies of ${APP_DISPLAY_NAME} can replace your updated app.`,
					detail: `${formatStaleAppCopies(copies)}\n\nMove these copies to Trash to prevent another downgrade. Your Orchestrator projects and sessions will not be removed.`,
					noLink: true,
				});
				return result.response === 0;
			},
			trashItem: (candidate) => shell.trashItem(candidate),
			reportFailures: async (paths) => {
				await dialog.showMessageBox({
					type: "warning",
					buttons: ["OK"],
					defaultId: 0,
					title: "Some old copies could not be removed",
					message: "Move these copies to Trash manually before launching Orchestrator again.",
					detail: paths.join("\n"),
					noLink: true,
				});
			},
		});
	} catch (err) {
		console.warn("stale AO copy cleanup failed:", err);
	}

	const keybindingRunFile = runFilePath();
	if (keybindingRunFile) {
		keybindingOverrides = await readKeybindingOverrides(path.dirname(keybindingRunFile));
	}

	registerRendererProtocol();
	registerArtifactSnapshotProtocol({ protocol, store: artifactSnapshotStore });
	void getCloudSessionCached(cloudDataDir()).then(
		(account) => bindArtifactSnapshotsToCloudAccount(account?.user.id ?? null),
		() => bindArtifactSnapshotsToCloudAccount(null),
	);
	applyRuntimeAppIcon();
	const initialUiSettings = keybindingRunFile
		? await readUiSettings(path.dirname(keybindingRunFile))
		: { ...DEFAULT_UI_SETTINGS };
	soundNotificationsEnabled = initialUiSettings.soundNotificationsEnabled;
	terminalShellPreference = initialUiSettings.terminalShell;
	if (browserQuitRequested) return;
	if (isTrayEnabled(process.platform)) {
		trayController = createTrayController({
			focusWindow: focusMainWindow,
			openSession: trayLifecycle.openSession,
			quitCompletely: desktopQuit.quitCompletely,
			locale: initialUiSettings.locale,
		});
	}
	if (trayController) {
		const awakeTray = createAwakeTrayMonitor({
			baseURL: () => daemonStatus.state === "ready" && daemonStatus.port ? `http://127.0.0.1:${daemonStatus.port}` : null,
			request: (url, init) => fetch(url, init),
			setState: (active) => trayController?.setAwakeState(active),
		});
		app.once("will-quit", () => awakeTray.dispose());
	}
	if (process.platform === "darwin") {
		powerMonitor.on("shutdown", () => desktopQuit.quitCompletely());
	}
	const daemonStartsWithWindow = process.platform !== "win32";
	if (daemonStartsWithWindow) void startDaemon();
	await createWindow();
	if (browserQuitRequested) return;
	if (app.isPackaged) {
		void import("./main/desktop-version-floor")
			.then(({ checkDesktopVersionFloor }) => checkDesktopVersionFloor({
					parent: () => mainWindow,
					quit: () => {
						preserveAgentsOnQuit = true;
						desktopQuit.quitCompletely();
					},
				}))
			.catch((err) => console.warn("desktop version floor check failed:", err));
	}
	if (!daemonStartsWithWindow) void startDaemon();
	initAutoUpdates();

	const deepLinkArg = process.argv.find((a) => a.startsWith("ao-app://"));
	if (deepLinkArg) {
		void handleCloudDeepLinkAndFocus(deepLinkArg);
	}

	const folderPathArg = parseOpenFolderPathArg(process.argv);
	if (folderPathArg) {
		pendingFolderPath = folderPathArg;
	}

	app.on("activate", () => {
		if (BaseWindow.getAllWindows().length === 0) {
			void createWindow().catch((error) => console.error("failed to recreate main window:", error));
		}
	});
});

setUpdateRestartFailureHandler(() => {
	if (!browserQuitRequested) focusMainWindow();
});

let updateQuitDeadlineArmed = false;
app.on("before-quit", (event) => {
	if (desktopQuit.handleBeforeQuit(event)) return;
	if (chatDraftRisks.length > 0 && !chatDraftQuitConfirmed) {
		event.preventDefault();
		if (confirmUnsafeChatDraftLeave(
			chatDraftRisks,
			(options) => dialog.showMessageBoxSync(options),
			chatDraftDialog,
		)) {
			chatDraftQuitConfirmed = true;
			app.quit();
		} else {
			preserveAgentsOnQuit = false;
			clearUpdateRestartRequest();
			desktopQuit.cancelQuit();
		}
		return;
	}
	if (!agentQuitCleanupComplete) {
		event.preventDefault();
		cancelDaemonAutoRestart();
		if (!agentQuitCleanupPromise) {
			agentQuitCleanupPromise = shutdownDaemonForDesktopQuit()
				.catch((error) => {
					console.error("AO: daemon shutdown on desktop quit failed; quitting anyway", error);
				})
				.then(() => {
					agentQuitCleanupComplete = true;
					agentQuitCleanupPromise = null;
					app.quit();
				});
		}
		return;
	}
	browserQuitRequested = true;
	cancelDaemonAutoRestart();
	disposeBrowserRuntimeLink();
	disposeBrowserStreamLink();
	trayLifecycle.dispose();
	trayController = null;
	if (!browserCleanupComplete) {
		event.preventDefault();
		if (!browserQuitCleanupPromise) {
			const retireSides = async () => {
				if (daemonStatus.state !== "ready" || !daemonStatus.port) return;
				try {
					const response = await fetch(`http://127.0.0.1:${daemonStatus.port}/api/v1/side-chats/launch/retire`, {
						method: "POST", headers: { "content-type": "application/json" },
						body: JSON.stringify({ appRunId }), signal: AbortSignal.timeout(3000),
					});
					if (!response.ok) console.warn("AO: side chat cleanup on quit failed", response.status);
				} catch (error) { console.warn("AO: side chat cleanup on quit failed", error); }
				sideLaunchState = undefined;
			};
			const cleanup = Promise.all([
				disposeAllBrowserViewHosts(),
				remoteRegistry.closeAll(),
				cloudAgentProxy.close(),
				telemetryPolicyController?.close() ?? Promise.resolve(),
				retireSides(),
				flushDaemonUsage(),
			]);
			const finishQuit = () => {
				browserCleanupComplete = true;
				browserQuitCleanupPromise = null;
				app.quit();
			};
			browserQuitCleanupPromise = cleanup.then(() => undefined).finally(finishQuit);
		}
		if (isUpdateRestartRequested() && !updateQuitDeadlineArmed) {
			updateQuitDeadlineArmed = true;
			void finishUpdateQuit(browserQuitCleanupPromise, {
				quit: () => undefined,
				exit: () => { if (isUpdateRestartRequested()) app.exit(0); },
				log: (error) => console.error("update shutdown:", error),
			});
		}
		return;
	}
});

process.on("exit", () => {
	if (daemonProcess && !supervisorLink?.connected && !keepDaemonAlive(process.env) && !desktopAgentShutdownAccepted) {
		killDaemon(daemonProcess);
	}
});

app.on("window-all-closed", () => {
	if (process.platform !== "darwin") {
		app.quit();
	}
});
