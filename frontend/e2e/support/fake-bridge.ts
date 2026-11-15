import type { Page } from "@playwright/test";

import type { UpdateSettings, UpdateStatus } from "../../src/main/update-settings";
import type { AoBridge } from "../../src/preload";
import type { DaemonStatus } from "../../src/shared/daemon-status";
import { coerceUiSettings, DEFAULT_UI_SETTINGS } from "../../src/shared/ui-locale";

export type FakeBridgeOptions = {
	version?: string;
	daemonState?: "ready" | "starting" | "stopped" | "error";
	daemonPort?: number;
	updateStatus?: UpdateStatus;
	updateSettings?: UpdateSettings;
};

export async function installFakeBridge(page: Page, opts: FakeBridgeOptions = {}): Promise<void> {
	const version = opts.version ?? "9.9.9-test";
	const daemonState = opts.daemonState ?? "ready";
	const daemonPort = opts.daemonPort ?? 8080;
	const updateStatus = opts.updateStatus ?? ({ state: "idle" } satisfies UpdateStatus);
	const updateSettings =
		opts.updateSettings ??
		({ enabled: false, channel: "latest", nightlyAck: false, feature: null } satisfies UpdateSettings);
	await page.addInitScript(
		({ version, daemonState, daemonPort, updateStatus, updateSettings }) => {
			const unsubscribe = () => () => undefined;
			const updateListeners = new Set<(status: UpdateStatus) => void>();
			let currentUpdateSettings = updateSettings;
			let currentUpdateStatus = updateStatus;
			const emitUpdateStatus = (next: UpdateStatus) => {
				currentUpdateStatus = next;
				for (const listener of updateListeners) listener(next);
			};
			const status: DaemonStatus =
				daemonState === "ready" ? { state: "ready", port: daemonPort } : { state: daemonState };
			const navState = (viewId: string) => ({
				viewId,
				url: "",
				title: "",
				canGoBack: false,
				canGoForward: false,
				isLoading: false,
			});

			const ao = {
				triage: {
					launch: async () => ({ ok: true }),
				},
					dictation: {
						start: async () => undefined,
						stop: async () => undefined,
						cancel: async () => undefined,
						onEvent: unsubscribe,
					},
					app: {
						getVersion: async () => version,
						chooseDirectory: async () => null,
						checkGitRepository: async () => true,
						openExternal: async () => undefined,
						openChromeExtensions: async () => undefined,
						scanImportFolder: async ({ path }: { path: string }) => ({ path, repos: [] }),
						checkAncestorRepo: async () => undefined,
						getRepositoryBranch: async () => undefined,
						getGitHubLogin: async () => "",
						listGitHubRepos: async () => [],
		getCachedGitHubOwners: async () => [],
						refreshGitHubOwners: async () => [],
						checkGitHubRepositoryAvailability: async () => ({ available: true }),
						getPathForFile: () => "",
					onOpenFolderPath: () => () => undefined,
					onOpenSessionDeepLink: () => () => undefined,
					onNewSessionShortcut: unsubscribe,
					onKeyboardShortcutsHelp: unsubscribe,
					onQuitConfirmRequest: unsubscribe,
					resolveQuitConfirm: () => undefined,
					onNewShellTerminalShortcut: unsubscribe,
					onCloseShellTerminalShortcut: unsubscribe,
					setCloseShellTerminalShortcutEnabled: () => undefined,
					setChatDraftRisk: () => undefined,
					onOpenSettingsShortcut: unsubscribe,
					onPreviousSessionShortcut: unsubscribe,
					onNextSessionShortcut: unsubscribe,
					onPreviousTabShortcut: unsubscribe,
					onNextTabShortcut: unsubscribe,
					onShortcut: unsubscribe,
					reportSessionsShown: () => undefined,
					setMenuAvailability: () => undefined,
					onMenuAction: unsubscribe,
					onScrollPhase: unsubscribe,
					showPrivacyGuide: async () => undefined,
					updatePrivacyGuide: async () => false,
					closePrivacyGuide: async () => undefined,
					getAppPrivacy: async () => undefined,
					requestAppPrivacy: async () => undefined,
				},
				terminal: {
					saveDroppedFile: async () => "",
					setFocused: () => undefined,
					onFontSizeShortcut: () => () => undefined,
				},
				artifactZoom: {
					setEngaged: () => undefined,
					onShortcut: () => () => undefined,
				},
				window: {
					getZoomFactor: async () => 1,
					onZoomFactor: () => () => undefined,
					isMaximized: async () => false,
					onMaximized: () => () => undefined,
					isFullScreen: async () => false,
					onFullScreen: () => () => undefined,
				},
				theme: {
					set: async () => undefined,
					persistTerminal: async () => undefined,
					setWindowBackground: async () => undefined,
					setWindowTranslucent: async () => undefined,
				},
				menu: { action: async () => undefined, notifyShellFocus: () => undefined },
				clipboard: {
					writeText: async () => undefined,
					readText: async () => "",
					writeImage: async () => undefined,
				},
				chatImage: {
					fetch: async () => ({ bytes: new Uint8Array(), mimeType: "image/png" }),
					save: async () => true,
				},
				sessionShareUrls: {
					remember: async () => undefined,
					recall: async () => ({}),
					forget: async () => undefined,
				},
				cloudShare: {
					available: async () => true,
					inspect: async () => ({ name: "", size: 0, unsupported: false, fingerprint: "" }),
					list: async () => ({ shares: [], limit: 3, used: 0, unlimited: false, privateLinks: false }),
					publish: async () => ({ status: "cancelled" as const }),
					cancel: async () => undefined,
					unpublish: async () => undefined,
					setAccess: async () => {
						throw new Error("Sharing is not faked.");
					},
					grants: async () => [],
					invite: async () => [],
					removeGrant: async () => undefined,
					onProgress: () => () => undefined,
				},
				artifactSnapshot: {
					request: async () => false,
				},
				cloudArtifact: {
					fetch: async () => new Uint8Array(),
					openFolder: async () => undefined,
					onFrameNavigated: () => () => undefined,
				},
				daemon: {
					getStatus: async () => status,
					start: async () => status,
					stop: async () => ({ state: "stopped" }),
					restart: async () => status,
					onStatus: (listener: (s: typeof status) => void) => {
						listener(status);
						return unsubscribe();
					},
				},
				agentLogin: { request: async () => null },
				sideChats: (() => {
					let revision = 0;
					const epoch = "fake-daemon";
					const entries = new Map<string, import("../../src/shared/side-close").SideCloseEntry>();
					const listeners = new Set<(state: import("../../src/shared/side-close").SideCloseState) => void>();
					const state = (sessionId: string | null): import("../../src/shared/side-close").SideCloseState => ({
						protocolVersion: 1, epoch, revision, recovery: "ready", sessionId,
						sides: [...entries.values()].filter(side => sessionId === null || side.sessionId === sessionId).map(side => ({ ...side })),
					});
					const publish = () => { revision++; for (const listener of listeners) listener(state(null)); };
					return {
						protocolVersion: 1 as const, mode: "desktop" as const,
						capture: async () => undefined,
						getCloseState: async (sessionId: string) => state(sessionId),
						onCloseStateChanged: (listener: (value: import("../../src/shared/side-close").SideCloseState) => void) => {
							listeners.add(listener); return () => { listeners.delete(listener); };
						},
						requestClose: async (request: import("../../src/shared/side-close").SideCloseRequest) => {
							const prior = entries.get(request.sideId);
							if (request.epoch !== epoch || request.expectedVersion !== (prior?.version ?? 0) ||
								prior?.status === "closed" || prior?.status === "closing") return state(request.sessionId);
							const entry: import("../../src/shared/side-close").SideCloseEntry = {
								id: request.sideId, sessionId: request.sessionId, label: request.sideId,
								version: (prior?.version ?? 0) + 1, operationId: request.requestId, status: "closing",
							};
							entries.set(entry.id, entry); publish();
							try {
								const response = await fetch(`http://127.0.0.1:${daemonPort}/api/v1/sessions/${encodeURIComponent(request.sessionId)}/conversation/side-chats/${encodeURIComponent(request.sideId)}`,
									{ method: "DELETE", redirect: "error", signal: AbortSignal.timeout(30_000) });
								entry.status = response.status === 204 ? "closed" : "unconfirmed";
							} catch { entry.status = "unconfirmed"; }
							entry.version++; publish(); return state(request.sessionId);
						},
					};
				})(),
				editorHandoff: {
					pathAction: async () => undefined,
					copyWorkspacePath: async () => undefined,
					getState: async () => ({
						targets: [
							{ id: "cursor" as const, name: "Cursor", kind: "editor" as const },
							{ id: "file-manager" as const, name: "File Manager", kind: "file_manager" as const },
							{ id: "terminal" as const, name: "Terminal", kind: "terminal" as const },
						],
						preferredEditorId: "cursor" as const,
						workspaceAvailable: true,
					}),
					getEditors: async () => ({
						editors: [{ id: "cursor" as const, name: "Cursor", kind: "editor" as const }],
						preferredEditorId: "cursor" as const,
					}),
					setPreferredEditor: async (editorId) => editorId,
					open: async () => ({ id: "cursor" as const, name: "Cursor", kind: "editor" as const }),
					openArtifacts: async () => ({ id: "file-manager" as const, name: "File Manager", kind: "file_manager" as const }),
					openArtifactInBrowser: async () => undefined,
				},
				telemetry: {
					getBootstrap: async () => null,
					getAnalyticsOptOut: async () => false,
					setAnalyticsOptOut: async (optedOut: boolean) => optedOut,
					onAnalyticsOptOut: () => () => undefined,
					setCloudUser: async () => false,
					getGithubLogin: async () => null,
					getLifetimeSessions: async () => null,
					claimFirstOpen: async () => "ineligible" as const,
					getPolicy: async () => ({ eventsEnabled: false, consentGeneration: "e2e", updatedAt: new Date(0).toISOString(), acknowledged: false, consentRenewalRequired: false, state: "applied", environmentVeto: true, durabilitySupported: false, reason: "environment_veto" }),
					setEventsEnabled: async () => ({ eventsEnabled: false, consentGeneration: "e2e", updatedAt: new Date(0).toISOString(), acknowledged: false, consentRenewalRequired: false, state: "applied", environmentVeto: true, durabilitySupported: false, reason: "environment_veto" }),
					onPolicy: () => () => false,
					onClearQueues: () => () => false,
					capture: async () => false,
				},
				browser: {
					reconnectRuntime: async () => undefined,
					getRuntimeState: async () => ({ connected: false, connecting: false, rejected: false }),
					nativeCompositionEnabled: true,
					ensure: async (sessionId: string) => navState(`preview:${sessionId}`),
					setBounds: () => undefined,
					onBoundsApplied: () => () => undefined,
					setOverlayOpen: () => undefined,
					navigate: async ({ viewId }: { viewId: string }) => navState(viewId),
					historySuggestions: async () => [],
					historyFavicon: async () => undefined,
					userBrowser: async () => null,
					openInUserBrowser: async () => ({ name: "", chrome: false }),
					clear: async (viewId: string) => navState(viewId),
					goBack: async (viewId: string) => navState(viewId),
					goForward: async (viewId: string) => navState(viewId),
					reload: async (viewId: string) => navState(viewId),
					stop: async (viewId: string) => navState(viewId),
					getFindState: async (viewId: string) => ({
						viewId,
						tabId: "t1",
						query: "",
						activeMatchOrdinal: 0,
						matches: 0,
						finalUpdate: true,
					}),
					findInPage: async ({ viewId, query }: { viewId: string; query: string }) => ({
						viewId,
						tabId: "t1",
						query,
						activeMatchOrdinal: 0,
						matches: 0,
						finalUpdate: true,
					}),
					stopFindInPage: async ({ viewId }: { viewId: string }) => ({
						viewId,
						tabId: "t1",
						query: "",
						activeMatchOrdinal: 0,
						matches: 0,
						finalUpdate: true,
					}),
					getTabs: async (viewId: string) => ({
						viewId,
						activeTabId: "t1",
						tabs: [{ id: "t1", url: "", title: "", active: true }],
					}),
					selectTab: async ({ viewId, tabId }: { viewId: string; tabId: string }) => ({
						viewId,
						activeTabId: tabId,
						tabs: [{ id: tabId, url: "", title: "", active: true }],
					}),
					closeTab: async ({ viewId }: { viewId: string; tabId: string }) => ({
						viewId,
						activeTabId: "t1",
						tabs: [{ id: "t1", url: "", title: "", active: true }],
					}),
					openTab: async ({ viewId }: { viewId: string; url?: string }) => ({
						viewId,
						activeTabId: "t1",
						tabs: [{ id: "t1", url: "", title: "", active: true }],
					}),
					getProfile: async (viewId: string) => ({ viewId, profileId: null, temporary: true }),
					showProfileMenu: async () => undefined,
					selectProfile: async () => undefined,
					captureScreenshot: async () => undefined,
					downloads: {
						list: async () => ({ downloads: [] }),
						action: async () => ({ downloads: [] }),
						clear: async () => ({ downloads: [] }),
						onChanged: unsubscribe,
					},
					notifyPanelUsed: () => undefined,
					notifyPanelBlur: () => undefined,
					onFocusLocation: unsubscribe,
					onFindOpen: unsubscribe,
					onReopenClosedTab: unsubscribe,
					onClosePanel: unsubscribe,
					devtools: async (input: { viewId: string }) => ({ viewId: input.viewId, open: false, activeTabId: "" }),
					destroy: () => undefined,
					setAnnotationMode: async () => undefined,
					completeAnnotation: async () => undefined,
					discardAnnotations: async () => undefined,
					annotationAction: async () => undefined,
					onAnnotationSubmit: unsubscribe,
					onAnnotationCancel: unsubscribe,
					onAnnotationState: unsubscribe,
					onNavState: unsubscribe,
					onFindState: unsubscribe,
					onTabsState: unsubscribe,
					onAgentActivity: unsubscribe,
					onRuntimeState: unsubscribe,
					onDevToolsState: unsubscribe,
					onProfileState: unsubscribe,
					onProfileManage: unsubscribe,
					onPageFocus: unsubscribe,
				},
				browserProfiles: {
					list: async () => ({ profiles: [], defaultProfileId: null }),
					create: async (name: string) => {
						const now = new Date().toISOString();
						return { id: `fake-${name}`, name, createdAt: now, updatedAt: now };
					},
					rename: async ({ id, name }: { id: string; name: string }) => {
						const now = new Date().toISOString();
						return { id, name, createdAt: now, updatedAt: now };
					},
					clear: async () => undefined,
					usage: async () => ({ sessions: [] }),
					delete: async () => ({}),
					setDefault: async () => undefined,
					discoverImportSources: async () => ({ sources: [] }),
					import: async () => ({ sourceName: "", entries: [] }),
					onImportProgress: () => () => undefined,
				},
				notifications: {
					show: async () => undefined,
					setBadge: async (_count: number) => undefined,
					devBounce: async () => undefined,
					onClick: unsubscribe,
					onPlaySound: unsubscribe,
					reportSoundFailure: () => undefined,
				},
				tray: {
					setAttentionState: () => undefined,
					onOpenSession: unsubscribe,
				},
				appState: {
					getMigration: async () => ({ status: "completed" }),
					setMigration: async () => undefined,
					getExperimentCohort: async () => null,
					claimFirstRunExperimentExposure: async () => false,
					getFirstRunProjectPrompt: async () => "seen",
					markFirstRunProjectPromptSeen: async () => undefined,
				},
				cliLink: {
					setEnabled: async () => undefined,
					inspect: async () => ({ ok: false as const, code: "unsupported" as const, message: "unavailable" }),
					link: async () => ({ ok: false as const, code: "unsupported" as const, message: "unavailable" }),
					unlink: async () => ({ ok: false as const, code: "unsupported" as const, message: "unavailable" }),
				},
				cliShadow: {
					inspect: async () => ({ kind: "unknown" as const }),
					replace: async () => ({ ok: false as const, code: "io" as const, message: "Not available in this environment." }),
					reveal: async () => undefined,
				},
				updateSettings: {
					get: async () => currentUpdateSettings,
					set: async (next: UpdateSettings) => {
						currentUpdateSettings = next;
					},
					setMacDifferentialUpdates: async (enabled: boolean) => {
						currentUpdateSettings = { ...currentUpdateSettings, macDifferentialUpdates: enabled };
					},
				},
				uiSettings: {
					get: async () => ({ ...DEFAULT_UI_SETTINGS }),
					set: async (settings) => coerceUiSettings({ ...DEFAULT_UI_SETTINGS, ...settings }),
				},
				keybindings: {
					get: async () => ({}),
					set: async (overrides) => overrides,
					setRecording: async () => undefined,
				},
				updates: {
					getStatus: async () => currentUpdateStatus,
					check: async () => undefined,
					returnHome: async () => undefined,
					download: async () => undefined,
					install: async () => undefined,
					isPostUpdateRelaunch: async () => false,
					relaunch: async () => undefined,
					onStatus: (listener: (status: UpdateStatus) => void) => {
						updateListeners.add(listener);
						return () => {
							updateListeners.delete(listener);
						};
					},
					onTelemetry: unsubscribe,
				},
				featureBuilds: {
					list: async () => [],
					getActive: async () => null,
				},
				remotes: {
					list: async () => [],
					syncAccountHosts: async () => undefined,
					linkAccountHost: async () => undefined,
					add: async () => "offline" as const,
					update: async () => "offline" as const,
					remove: async () => undefined,
					connect: async (url: string) => {
						throw new Error(`No fake remote for ${url}`);
					},
					disconnect: async () => undefined,
					computerName: async () => "E2E computer",
					previewUrl: async (_hostId: string, _sessionId: string, sourceUrl: string) => sourceUrl,
					resolvePreviewUrl: async (_hostId: string, _sessionId: string, viewedUrl: string) => viewedUrl,
					agentBrowserUrl: async () => "",
					externalUrl: async (_hostId: string, url: string) => url,
				},
				cloud: {
					getSession: async () => null,
					signIn: async () => undefined,
					signOut: async () => undefined,
					cancelProviderAuth: async () => undefined,
					connectProviderAuth: async () => undefined,
					localAuthAvailable: async () => false,
					onOpenShareLink: () => () => undefined,
					takePendingShareLink: async () => null,
					pushFolderToRepository: async () => ({ status: "error" as const, code: "invalid_request" as const, message: "not available in e2e" }),
					previewFolderUpload: async () => ({ status: "error" as const, code: "invalid_request" as const, message: "not available in e2e" }),
					takePendingSignIn: async () => null,
					localRegister: async () => {
						throw new Error("local auth is unavailable in e2e");
					},
					localLogin: async () => {
						throw new Error("local auth is unavailable in e2e");
					},
					onSessionChanged: unsubscribe,
				},
				cloudCp: {
					request: async () => ({ status: 401, headers: {}, body: "" }),
					requestBinary: async () => ({ status: 401, headers: {}, body: new Uint8Array() }),
					cancelBinary: () => undefined,
					openStream: async () => ({ streamId: "stream_test" }),
					closeStream: () => undefined,
					onStreamEvent: unsubscribe,
					agentBrowserUrl: async () => "",
				},
			} satisfies AoBridge;
			(window as unknown as { __aoFakeUpdates: { setStatus: (status: UpdateStatus) => void } }).__aoFakeUpdates = {
				setStatus: emitUpdateStatus,
			};
			(window as unknown as { ao: unknown }).ao = ao;
		},
		{ version, daemonState, daemonPort, updateStatus, updateSettings },
	);
}

export type FakeWorker = {
	id: string;
	title: string;
	createdAt?: string;
	mode?: "chat" | "tui";
	provider?: string;
	branch?: string;
	status?: string;
	activity?: "active" | "idle" | "waiting_input" | "exited";
	previewUrl?: string;
	previewRevision?: number;
};

export type FakeAgentOptions = {
	version?: string;
	daemonPort?: number;
	projectId?: string;
	projectName?: string;
	platform?: string;
	workers?: FakeWorker[];
	expandProject?: boolean;
};

export type FakeAgentController = {
	snapshot: () => unknown[];
	createWorker: (worker: FakeWorker) => void;
	createWorkerWithoutEvent: (worker: FakeWorker) => void;
	createWorkerDuringNextSnapshot: (worker: FakeWorker) => void;
	removeWorker: (id: string) => void;
	setStatus: (id: string, status: string, activity?: string) => void;
	setTerminalHandle: (id: string, handleId: string) => void;
	setProvider: (id: string, provider: string) => void;
	setPreview: (id: string, previewUrl: string, previewRevision?: number) => void;
	setBrowserError: (message: string | null) => void;
	reconnectEventsWithReplayBacklog: (count: number) => void;
	replayProgress: () => FakeReplayProgress;
	closeEventsStream: () => void;
	eventStreams: () => Array<{ url: string; readyState: number; openedAt: number | null }>;
	startContinuousWorkspaceEvents: (intervalMs: number, eventsPerTick?: number) => void;
	stopContinuousWorkspaceEvents: () => void;
	continuousWorkspaceEvents: () => FakePressureProgress;
	setSnapshotFailures: (count: number) => void;
	snapshotCallCount: () => number;
	notify: (n: { id: string; type: string; title: string; body?: string; sessionId?: string }) => void;
};

export type FakeReplayProgress = {
	phase: "idle" | "connecting" | "replaying";
	remaining: number;
	delivered: number;
	abandoned: number;
};

export type FakePressureProgress = {
	sent: number;
	delivered: number;
};

export type FakeUpdateController = {
	setStatus: (status: UpdateStatus) => void;
};

declare global {
	interface Window {
		__aoFakeAgent?: FakeAgentController;
		__aoFakeUpdates?: FakeUpdateController;
	}
}

export async function installFakeAgent(page: Page, opts: FakeAgentOptions = {}): Promise<void> {
	const version = opts.version ?? "9.9.9-test";
	const daemonPort = opts.daemonPort ?? 8080;
	const projectId = opts.projectId ?? "fake-proj";
	const projectName = opts.projectName ?? "fake-proj";
	const platform = opts.platform ?? null;
	const workers = opts.workers ?? [];
	const expandProject = opts.expandProject ?? false;
	const nowIso = new Date().toISOString();

	await page.addInitScript(
		({ version, daemonPort, projectId, projectName, platform, workers, nowIso, expandProject }) => {
			try {
				if (expandProject) {
					const key = "ao.sidebar.expanded-projects";
					const expanded = JSON.parse(localStorage.getItem(key) ?? "[]");
					if (Array.isArray(expanded) && !expanded.includes(projectId)) {
						localStorage.setItem(key, JSON.stringify([...expanded, projectId]));
					}
				}
			} catch {
			}
			if (platform) {
				try {
					Object.defineProperty(navigator, "platform", { get: () => platform, configurable: true });
				} catch {
				}
			}

			type Session = Record<string, unknown>;
			const kanbanColumnFor = (status: string): string => {
				switch (status) {
					case "merged":
					case "approved":
					case "mergeable":
						return "ready";
					case "terminated":
						return "archive";
					case "review_pending":
					case "pr_open":
					case "draft":
						return "validating";
					case "needs_input":
					case "exited":
					case "no_signal":
					case "ci_failed":
					case "changes_requested":
						return "needs_review";
					default:
						return "building";
				}
			};
			const makeWorker = (w: (typeof workers)[number]): Session => ({
				id: w.id,
				terminalHandleId: (w.mode ?? "tui") === "tui" ? `${w.id}/terminal_0` : undefined,
				workspaceId: projectId,
				workspaceName: projectName,
				title: w.title,
				provider: w.provider ?? "codex",
				kind: "worker",
				mode: w.mode ?? "tui",
				branch: w.branch ?? `session/${w.id}`,
				status: w.status ?? "working",
				kanbanColumn: kanbanColumnFor(w.status ?? "working"),
				createdAt: w.createdAt ?? nowIso,
				updatedAt: new Date().toISOString(),
				activity: { state: w.activity ?? "active", lastActivityAt: new Date().toISOString() },
				previewUrl: w.previewUrl,
				previewRevision: w.previewRevision,
				prs: [],
			});

			const project: Record<string, unknown> = {
				id: projectId,
				name: projectName,
				kind: "single_repo",
				path: `/repos/${projectName}`,
				type: "main",
				orchestratorAgent: "codex",
				sessions: [
					{
						id: `${projectId}-orchestrator`,
						terminalHandleId: `${projectId}-orchestrator/terminal_0`,
						workspaceId: projectId,
						workspaceName: projectName,
						title: "Project orchestrator",
						provider: "codex",
						kind: "orchestrator",
						branch: "main",
						status: "working",
						kanbanColumn: "building",
						createdAt: nowIso,
						updatedAt: nowIso,
						activity: { state: "active", lastActivityAt: nowIso },
						prs: [],
					},
					...workers.map(makeWorker),
				],
			};

			interface FakeEventSourceLike {
				url: string;
				readyState: number;
				openedAt: number | null;
				onopen: ((ev: unknown) => void) | null;
				onerror: ((ev: unknown) => void) | null;
				onmessage: ((ev: unknown) => void) | null;
				_listeners: Record<string, ((ev: unknown) => void)[]>;
				close: () => void;
				_dispatch: (type: string, data?: string) => void;
			}

			const state = {
				browserError: null as string | null,
				eventSources: [] as FakeEventSourceLike[],
				workspaces: [project],
				replay: {
					phase: "idle" as "idle" | "connecting" | "replaying",
					source: undefined as FakeEventSourceLike | undefined,
					remaining: 0,
					delivered: 0,
					abandoned: 0,
					deferred: [] as Array<{ type: string; data?: string }>,
				},
				raceWorker: null as (typeof workers)[number] | null,
				cdcSeq: 0,
				pressure: {
					timer: undefined as number | undefined,
					startedAt: 0,
					stoppedAt: undefined as number | undefined,
					periodMs: 1,
					perTick: 1,
					delivered: 0,
				},
				snapshotCalls: 0,
				snapshotFailures: 0,
			};

			const findSession = (id: string) => (project.sessions as Session[]).find((s) => s.id === id);
			const addWorker = (worker: (typeof workers)[number]) => {
				if (!findSession(worker.id)) (project.sessions as Session[]).push(makeWorker(worker));
			};
			const touch = (s: Session) => {
				s.updatedAt = new Date().toISOString();
			};

			class FakeEventSource implements FakeEventSourceLike {
				static readonly CONNECTING = 0;
				static readonly OPEN = 1;
				static readonly CLOSED = 2;
				readonly CONNECTING = 0;
				readonly OPEN = 1;
				readonly CLOSED = 2;
				url: string;
				readyState = 1;
				openedAt: number | null = null;
				onopen: ((ev: unknown) => void) | null = null;
				onerror: ((ev: unknown) => void) | null = null;
				onmessage: ((ev: unknown) => void) | null = null;
				_listeners: Record<string, ((ev: unknown) => void)[]> = {};
				constructor(url: string) {
					this.url = String(url);
					state.eventSources.push(this);
					setTimeout(() => {
						if (this.readyState !== 1 || !this.onopen) return;
						this.openedAt = Date.now();
						this.onopen({ type: "open" });
					}, 0);
				}
				addEventListener(t: string, cb: (ev: unknown) => void) {
					(this._listeners[t] ||= []).push(cb);
				}
				removeEventListener(t: string, cb: (ev: unknown) => void) {
					const arr = this._listeners[t];
					if (!arr) return;
					const i = arr.indexOf(cb);
					if (i >= 0) arr.splice(i, 1);
				}
				close() {
					this.readyState = 2;
				}
				_dispatch(type: string, data = "") {
					const ev = { type, data };
					if (type === "message" && this.onmessage) this.onmessage(ev);
					for (const cb of this._listeners[type] ?? []) {
						try {
							cb(ev);
						} catch {
						}
					}
				}
			}
			(window as unknown as { EventSource: unknown }).EventSource = FakeEventSource;

			const emit = (match: string, type: string, data?: string) => {
				for (const es of state.eventSources) {
					if (!es.url.includes(match)) continue;
					if (es === state.replay.source && state.replay.phase !== "idle") {
						state.replay.deferred.push({ type, data });
						continue;
					}
					if (es.readyState !== 1) continue;
					es._dispatch(type, data);
				}
			};
			const pushWorkspaces = (type = "session_updated") => emit("/api/v1/events", type);

			const liveCdcStream = () =>
				[...state.eventSources]
					.reverse()
					.find((source) => source.url.includes("/api/v1/events") && source.readyState !== 2);
			const cdcFrame = (type: string, session: Session) =>
				JSON.stringify({
					seq: (state.cdcSeq += 1),
					projectId,
					sessionId: session.id,
					type,
					payload: { id: session.id, activity: "active", isTerminated: false },
					createdAt: nowIso,
				});
			const BROWSER_RECONNECT_MS = 3_000;
			const REPLAY_PAGE = 512;
			const ACTIVE_RUN = 50;
			const activeSession = (sessions: Session[], n: number) => sessions[Math.floor(n / ACTIVE_RUN) % sessions.length];
			const pressureSent = () => {
				const { startedAt, stoppedAt, periodMs, perTick } = state.pressure;
				if (!startedAt) return 0;
				return Math.floor(((stoppedAt ?? performance.now()) - startedAt) / periodMs) * perTick;
			};
			const stopPressure = () => {
				const pressure = state.pressure;
				if (pressure.timer === undefined) return;
				window.clearInterval(pressure.timer);
				pressure.timer = undefined;
				pressure.stoppedAt = performance.now();
			};

			const reconnectWithReplay = (source: FakeEventSourceLike, count: number) => {
				const replay = state.replay;
				if (replay.phase !== "idle") throw new Error("a CDC replay is already in progress");
				const abandon = () => {
					replay.abandoned += replay.remaining + replay.deferred.length;
					replay.remaining = 0;
					replay.deferred = [];
					replay.phase = "idle";
				};
				const gapSessions = [...(project.sessions as Session[])];
				replay.phase = "connecting";
				replay.source = source;
				replay.remaining = count;
				replay.delivered = 0;
				replay.abandoned = 0;
				source.readyState = 0;
				source.onerror?.({ type: "error" });
				setTimeout(() => {
					if (source.readyState !== 0) return abandon();
					source.readyState = 1;
					replay.phase = "replaying";
					source.openedAt = Date.now();
					source.onopen?.({ type: "open" });
					const drainPage = () => {
						if (source.readyState !== 1) return abandon();
						const frames = Math.min(REPLAY_PAGE, replay.remaining);
						for (let i = 0; i < frames; i += 1) {
							source._dispatch("session_updated", cdcFrame("session_updated", activeSession(gapSessions, replay.delivered + i)));
						}
						replay.remaining -= frames;
						replay.delivered += frames;
						if (replay.remaining > 0) {
							setTimeout(drainPage, 0);
							return;
						}
						for (const frame of replay.deferred) source._dispatch(frame.type, frame.data);
						replay.deferred = [];
						replay.phase = "idle";
					};
					drainPage();
				}, BROWSER_RECONNECT_MS);
			};

			const createWorker = (worker: (typeof workers)[number]) => {
				addWorker(worker);
				emit("/api/v1/events", "session_created", cdcFrame("session_created", findSession(worker.id)!));
			};

			const controller: FakeAgentController = {
				snapshot: () => {
					state.snapshotCalls += 1;
					if (state.snapshotFailures > 0) {
						state.snapshotFailures -= 1;
						throw new Error("simulated workspace sessions response failure");
					}
					const snapshot = JSON.parse(JSON.stringify(state.workspaces));
					const raceWorker = state.raceWorker;
					if (raceWorker) {
						state.raceWorker = null;
						createWorker(raceWorker);
					}
					return snapshot;
				},
				createWorker,
				createWorkerWithoutEvent: addWorker,
				createWorkerDuringNextSnapshot: (w) => { state.raceWorker = w; },
				removeWorker: (id) => {
					const sessions = project.sessions as Session[];
					const index = sessions.findIndex((session) => session.id === id);
					if (index >= 0) sessions.splice(index, 1);
					pushWorkspaces();
				},
				setStatus: (id, status, activity) => {
					const s = findSession(id);
					if (!s) return;
					s.status = status;
					s.kanbanColumn = kanbanColumnFor(status);
					s.displayStatus = undefined;
					if (activity) s.activity = { state: activity, lastActivityAt: new Date().toISOString() };
					touch(s);
					pushWorkspaces();
				},
				setTerminalHandle: (id, handleId) => {
					const s = findSession(id);
					if (!s) return;
					s.terminalHandleId = handleId;
					touch(s);
					pushWorkspaces();
				},
				setProvider: (id, provider) => {
					const s = findSession(id);
					if (!s) return;
					s.provider = provider;
					touch(s);
					pushWorkspaces();
				},
				setPreview: (id, previewUrl, previewRevision) => {
					const s = findSession(id);
					if (!s) return;
					s.previewUrl = previewUrl;
					s.previewRevision = previewRevision ?? (typeof s.previewRevision === "number" ? s.previewRevision : 0) + 1;
					touch(s);
					pushWorkspaces();
				},
				setBrowserError: (message) => {
					state.browserError = message;
				},
				reconnectEventsWithReplayBacklog: (count) => {
					const source = liveCdcStream();
					if (!source) throw new Error("no open CDC stream to reconnect");
					reconnectWithReplay(source, Math.max(0, Math.floor(count)));
				},
				replayProgress: () => {
					const { phase, remaining, delivered, abandoned } = state.replay;
					return { phase, remaining, delivered, abandoned };
				},
				closeEventsStream: () => {
					const source = liveCdcStream();
					if (!source) throw new Error("no open CDC stream to close");
					source.readyState = 2;
					source.onerror?.({ type: "error" });
				},
				eventStreams: () =>
					state.eventSources
						.filter((source) => source.url.includes("/api/v1/events"))
						.map(({ url, readyState, openedAt }) => ({ url, readyState, openedAt })),
				startContinuousWorkspaceEvents: (intervalMs, eventsPerTick = 1) => {
					stopPressure();
					const pressure = state.pressure;
					pressure.startedAt = performance.now();
					pressure.stoppedAt = undefined;
					pressure.periodMs = Math.max(1, Math.floor(intervalMs));
					pressure.perTick = Math.max(1, Math.floor(eventsPerTick));
					pressure.delivered = 0;
					const sessions = [...(project.sessions as Session[])];
					let draining = false;
					const drain = () => {
						if (pressure.timer !== timer) {
							draining = false;
							return;
						}
						const sent = pressureSent();
						const end = Math.min(sent, pressure.delivered + pressure.perTick);
						for (; pressure.delivered < end; pressure.delivered += 1) {
							emit("/api/v1/events", "session_updated", cdcFrame("session_updated", activeSession(sessions, pressure.delivered)));
						}
						if (pressure.delivered < sent) setTimeout(drain, 0);
						else draining = false;
					};
					const timer = window.setInterval(() => {
						if (draining) return;
						draining = true;
						drain();
					}, pressure.periodMs);
					pressure.timer = timer;
				},
				stopContinuousWorkspaceEvents: stopPressure,
				continuousWorkspaceEvents: () => ({ sent: pressureSent(), delivered: state.pressure.delivered }),
				setSnapshotFailures: (count) => { state.snapshotFailures = Math.max(0, Math.floor(count)); },
				snapshotCallCount: () => state.snapshotCalls,
				notify: (n) => {
					const payload = JSON.stringify({
						id: n.id,
						type: n.type,
						title: n.title,
						body: n.body ?? "",
						sessionId: n.sessionId ?? "",
						projectId,
						target: { kind: "session", sessionId: n.sessionId ?? "" },
						status: "unread",
						createdAt: new Date().toISOString(),
					});
					emit("/api/v1/notifications/stream", "notification_created", payload);
				},
			};
			(window as unknown as { __aoFakeAgent: unknown }).__aoFakeAgent = controller;

			const unsubscribe = () => () => undefined;
			const status: DaemonStatus = { state: "ready", port: daemonPort };
			const navState = (viewId: string, url = "", error?: string) => ({
				viewId,
				url,
				title: url ? "AO preview" : "",
				canGoBack: false,
				canGoForward: false,
				isLoading: false,
				...(error ? { error } : {}),
			});
			const ao = {
				triage: {
					launch: async () => ({ ok: true }),
				},
					dictation: {
						start: async () => undefined,
						stop: async () => undefined,
						cancel: async () => undefined,
						onEvent: unsubscribe,
					},
					app: {
						getVersion: async () => version,
						chooseDirectory: async () => null,
						checkGitRepository: async () => true,
						openExternal: async () => undefined,
						openChromeExtensions: async () => undefined,
						scanImportFolder: async ({ path }: { path: string }) => ({ path, repos: [] }),
						checkAncestorRepo: async () => undefined,
						getRepositoryBranch: async () => undefined,
						getGitHubLogin: async () => "",
						listGitHubRepos: async () => [],
		getCachedGitHubOwners: async () => [],
						refreshGitHubOwners: async () => [],
						checkGitHubRepositoryAvailability: async () => ({ available: true }),
						getPathForFile: () => "",
					onOpenFolderPath: () => () => undefined,
					onOpenSessionDeepLink: () => () => undefined,
					onNewSessionShortcut: unsubscribe,
					onKeyboardShortcutsHelp: unsubscribe,
					onQuitConfirmRequest: unsubscribe,
					resolveQuitConfirm: () => undefined,
					onNewShellTerminalShortcut: unsubscribe,
					onCloseShellTerminalShortcut: unsubscribe,
					setCloseShellTerminalShortcutEnabled: () => undefined,
					setChatDraftRisk: () => undefined,
					onOpenSettingsShortcut: unsubscribe,
					onPreviousSessionShortcut: unsubscribe,
					onNextSessionShortcut: unsubscribe,
					onPreviousTabShortcut: unsubscribe,
					onNextTabShortcut: unsubscribe,
					onShortcut: unsubscribe,
					reportSessionsShown: () => undefined,
					setMenuAvailability: () => undefined,
					onMenuAction: unsubscribe,
					onScrollPhase: unsubscribe,
					showPrivacyGuide: async () => undefined,
					updatePrivacyGuide: async () => false,
					closePrivacyGuide: async () => undefined,
					getAppPrivacy: async () => undefined,
					requestAppPrivacy: async () => undefined,
				},
				terminal: {
					saveDroppedFile: async () => "",
					setFocused: () => undefined,
					onFontSizeShortcut: () => () => undefined,
				},
				artifactZoom: {
					setEngaged: () => undefined,
					onShortcut: () => () => undefined,
				},
				window: {
					getZoomFactor: async () => 1,
					onZoomFactor: () => () => undefined,
					isMaximized: async () => false,
					onMaximized: () => () => undefined,
					isFullScreen: async () => false,
					onFullScreen: () => () => undefined,
				},
				theme: {
					set: async () => undefined,
					persistTerminal: async () => undefined,
					setWindowBackground: async () => undefined,
					setWindowTranslucent: async () => undefined,
				},
				menu: { action: async () => undefined, notifyShellFocus: () => undefined },
				clipboard: { writeText: async () => undefined, readText: async () => "", writeImage: async () => undefined },
				chatImage: {
					fetch: async () => ({ bytes: new Uint8Array(), mimeType: "image/png" }),
					save: async () => true,
				},
				sessionShareUrls: {
					remember: async () => undefined,
					recall: async () => ({}),
					forget: async () => undefined,
				},
				cloudShare: {
					available: async () => true,
					inspect: async () => ({ name: "", size: 0, unsupported: false, fingerprint: "" }),
					list: async () => ({ shares: [], limit: 3, used: 0, unlimited: false, privateLinks: false }),
					publish: async () => ({ status: "cancelled" as const }),
					cancel: async () => undefined,
					unpublish: async () => undefined,
					setAccess: async () => {
						throw new Error("Sharing is not faked.");
					},
					grants: async () => [],
					invite: async () => [],
					removeGrant: async () => undefined,
					onProgress: () => () => undefined,
				},
				artifactSnapshot: {
					request: async () => false,
				},
				cloudArtifact: {
					fetch: async () => new Uint8Array(),
					openFolder: async () => undefined,
					onFrameNavigated: () => () => undefined,
				},
				daemon: {
					getStatus: async () => status,
					start: async () => status,
					stop: async () => ({ state: "stopped" }),
					restart: async () => status,
					onStatus: (listener: (s: typeof status) => void) => {
						listener(status);
						return unsubscribe();
					},
				},
				agentLogin: { request: async () => null },
				sideChats: (() => {
					let revision = 0;
					const epoch = "fake-daemon";
					const entries = new Map<string, import("../../src/shared/side-close").SideCloseEntry>();
					const listeners = new Set<(state: import("../../src/shared/side-close").SideCloseState) => void>();
					const state = (sessionId: string | null): import("../../src/shared/side-close").SideCloseState => ({
						protocolVersion: 1, epoch, revision, recovery: "ready", sessionId,
						sides: [...entries.values()].filter(side => sessionId === null || side.sessionId === sessionId).map(side => ({ ...side })),
					});
					const publish = () => { revision++; for (const listener of listeners) listener(state(null)); };
					return {
						protocolVersion: 1 as const, mode: "desktop" as const,
						capture: async () => undefined,
						getCloseState: async (sessionId: string) => state(sessionId),
						onCloseStateChanged: (listener: (value: import("../../src/shared/side-close").SideCloseState) => void) => {
							listeners.add(listener); return () => { listeners.delete(listener); };
						},
						requestClose: async (request: import("../../src/shared/side-close").SideCloseRequest) => {
							const prior = entries.get(request.sideId);
							if (request.epoch !== epoch || request.expectedVersion !== (prior?.version ?? 0) ||
								prior?.status === "closed" || prior?.status === "closing") return state(request.sessionId);
							const entry: import("../../src/shared/side-close").SideCloseEntry = {
								id: request.sideId, sessionId: request.sessionId, label: request.sideId,
								version: (prior?.version ?? 0) + 1, operationId: request.requestId, status: "closing",
							};
							entries.set(entry.id, entry); publish();
							try {
								const response = await fetch(`http://127.0.0.1:${daemonPort}/api/v1/sessions/${encodeURIComponent(request.sessionId)}/conversation/side-chats/${encodeURIComponent(request.sideId)}`,
									{ method: "DELETE", redirect: "error", signal: AbortSignal.timeout(30_000) });
								entry.status = response.status === 204 ? "closed" : "unconfirmed";
							} catch { entry.status = "unconfirmed"; }
							entry.version++; publish(); return state(request.sessionId);
						},
					};
				})(),
				editorHandoff: {
					pathAction: async () => undefined,
					copyWorkspacePath: async () => undefined,
					getState: async () => ({
						targets: [
							{ id: "cursor" as const, name: "Cursor", kind: "editor" as const },
							{ id: "file-manager" as const, name: "File Manager", kind: "file_manager" as const },
							{ id: "terminal" as const, name: "Terminal", kind: "terminal" as const },
						],
						preferredEditorId: "cursor" as const,
						workspaceAvailable: true,
					}),
					getEditors: async () => ({
						editors: [{ id: "cursor" as const, name: "Cursor", kind: "editor" as const }],
						preferredEditorId: "cursor" as const,
					}),
					setPreferredEditor: async (editorId) => editorId,
					open: async () => ({ id: "cursor" as const, name: "Cursor", kind: "editor" as const }),
					openArtifacts: async () => ({ id: "file-manager" as const, name: "File Manager", kind: "file_manager" as const }),
					openArtifactInBrowser: async () => undefined,
				},
				telemetry: {
					getBootstrap: async () => null,
					getAnalyticsOptOut: async () => false,
					setAnalyticsOptOut: async (optedOut: boolean) => optedOut,
					onAnalyticsOptOut: () => () => undefined,
					setCloudUser: async () => false,
					getGithubLogin: async () => null,
					getLifetimeSessions: async () => null,
					claimFirstOpen: async () => "ineligible" as const,
					getPolicy: async () => ({ eventsEnabled: false, consentGeneration: "e2e", updatedAt: new Date(0).toISOString(), acknowledged: false, consentRenewalRequired: false, state: "applied", environmentVeto: true, durabilitySupported: false, reason: "environment_veto" }),
					setEventsEnabled: async () => ({ eventsEnabled: false, consentGeneration: "e2e", updatedAt: new Date(0).toISOString(), acknowledged: false, consentRenewalRequired: false, state: "applied", environmentVeto: true, durabilitySupported: false, reason: "environment_veto" }),
					onPolicy: () => () => false,
					onClearQueues: () => () => false,
					capture: async () => false,
				},
				browser: {
					reconnectRuntime: async () => undefined,
					getRuntimeState: async () => ({ connected: false, connecting: false, rejected: false }),
					nativeCompositionEnabled: true,
					ensure: async (sessionId: string) => navState(`preview:${sessionId}`),
					setBounds: () => undefined,
					onBoundsApplied: () => () => undefined,
					setOverlayOpen: () => undefined,
					navigate: async ({ viewId, url }: { viewId: string; url: string }) =>
						state.browserError ? navState(viewId, "", state.browserError) : navState(viewId, url),
					historySuggestions: async () => [],
					historyFavicon: async () => undefined,
					userBrowser: async () => null,
					openInUserBrowser: async () => ({ name: "", chrome: false }),
					clear: async (viewId: string) => navState(viewId),
					goBack: async (viewId: string) => navState(viewId),
					goForward: async (viewId: string) => navState(viewId),
					reload: async (viewId: string) => navState(viewId),
					stop: async (viewId: string) => navState(viewId),
					getFindState: async (viewId: string) => ({
						viewId,
						tabId: "t1",
						query: "",
						activeMatchOrdinal: 0,
						matches: 0,
						finalUpdate: true,
					}),
					findInPage: async ({ viewId, query }: { viewId: string; query: string }) => ({
						viewId,
						tabId: "t1",
						query,
						activeMatchOrdinal: 0,
						matches: 0,
						finalUpdate: true,
					}),
					stopFindInPage: async ({ viewId }: { viewId: string }) => ({
						viewId,
						tabId: "t1",
						query: "",
						activeMatchOrdinal: 0,
						matches: 0,
						finalUpdate: true,
					}),
					getTabs: async (viewId: string) => ({
						viewId,
						activeTabId: "t1",
						tabs: [{ id: "t1", url: "", title: "", active: true }],
					}),
					selectTab: async ({ viewId, tabId }: { viewId: string; tabId: string }) => ({
						viewId,
						activeTabId: tabId,
						tabs: [{ id: tabId, url: "", title: "", active: true }],
					}),
					closeTab: async ({ viewId }: { viewId: string; tabId: string }) => ({
						viewId,
						activeTabId: "t1",
						tabs: [{ id: "t1", url: "", title: "", active: true }],
					}),
					openTab: async ({ viewId }: { viewId: string; url?: string }) => ({
						viewId,
						activeTabId: "t1",
						tabs: [{ id: "t1", url: "", title: "", active: true }],
					}),
					getProfile: async (viewId: string) => ({ viewId, profileId: null, temporary: true }),
					showProfileMenu: async () => undefined,
					selectProfile: async () => undefined,
					captureScreenshot: async () => undefined,
					downloads: {
						list: async () => ({ downloads: [] }),
						action: async () => ({ downloads: [] }),
						clear: async () => ({ downloads: [] }),
						onChanged: unsubscribe,
					},
					notifyPanelUsed: () => undefined,
					notifyPanelBlur: () => undefined,
					onFocusLocation: unsubscribe,
					onFindOpen: unsubscribe,
					onReopenClosedTab: unsubscribe,
					onClosePanel: unsubscribe,
					devtools: async (input: { viewId: string }) => ({ viewId: input.viewId, open: false, activeTabId: "" }),
					destroy: () => undefined,
					setAnnotationMode: async () => undefined,
					completeAnnotation: async () => undefined,
					discardAnnotations: async () => undefined,
					annotationAction: async () => undefined,
					onAnnotationSubmit: unsubscribe,
					onAnnotationCancel: unsubscribe,
					onAnnotationState: unsubscribe,
					onNavState: unsubscribe,
					onFindState: unsubscribe,
					onTabsState: unsubscribe,
					onAgentActivity: unsubscribe,
					onRuntimeState: unsubscribe,
					onDevToolsState: unsubscribe,
					onProfileState: unsubscribe,
					onProfileManage: unsubscribe,
					onPageFocus: unsubscribe,
				},
				browserProfiles: {
					list: async () => ({ profiles: [], defaultProfileId: null }),
					create: async (name: string) => {
						const now = new Date().toISOString();
						return { id: `fake-${name}`, name, createdAt: now, updatedAt: now };
					},
					rename: async ({ id, name }: { id: string; name: string }) => {
						const now = new Date().toISOString();
						return { id, name, createdAt: now, updatedAt: now };
					},
					clear: async () => undefined,
					usage: async () => ({ sessions: [] }),
					delete: async () => ({}),
					setDefault: async () => undefined,
					discoverImportSources: async () => ({ sources: [] }),
					import: async () => ({ sourceName: "", entries: [] }),
					onImportProgress: () => () => undefined,
				},
				notifications: {
					show: async () => undefined,
					setBadge: async (_count: number) => undefined,
					devBounce: async () => undefined,
					onClick: unsubscribe,
					onPlaySound: unsubscribe,
					reportSoundFailure: () => undefined,
				},
				tray: { setAttentionState: () => undefined, onOpenSession: unsubscribe },
				appState: { getMigration: async () => ({ status: "completed" }), setMigration: async () => undefined, getExperimentCohort: async () => null, claimFirstRunExperimentExposure: async () => false, getFirstRunProjectPrompt: async () => "seen", markFirstRunProjectPromptSeen: async () => undefined },
				cliLink: {
					setEnabled: async () => undefined,
					inspect: async () => ({ ok: false as const, code: "unsupported" as const, message: "unavailable" }),
					link: async () => ({ ok: false as const, code: "unsupported" as const, message: "unavailable" }),
					unlink: async () => ({ ok: false as const, code: "unsupported" as const, message: "unavailable" }),
				},
				cliShadow: {
					inspect: async () => ({ kind: "unknown" as const }),
					replace: async () => ({ ok: false as const, code: "io" as const, message: "Not available in this environment." }),
					reveal: async () => undefined,
				},
				updateSettings: {
					get: async () => ({
						enabled: false,
						channel: "latest",
						nightlyAck: false,
						feature: null,
						macDifferentialUpdates: false,
					}),
					set: async () => undefined,
					setMacDifferentialUpdates: async () => undefined,
				},
				uiSettings: {
					get: async () => ({ ...DEFAULT_UI_SETTINGS }),
					set: async (settings) => coerceUiSettings({ ...DEFAULT_UI_SETTINGS, ...settings }),
				},
				keybindings: {
					get: async () => ({}),
					set: async (overrides) => overrides,
					setRecording: async () => undefined,
				},
				updates: {
					getStatus: async () => ({ state: "idle" }),
					check: async () => undefined,
					returnHome: async () => undefined,
					download: async () => undefined,
					install: async () => undefined,
					isPostUpdateRelaunch: async () => false,
					relaunch: async () => undefined,
					onStatus: unsubscribe,
					onTelemetry: unsubscribe,
				},
				featureBuilds: {
					list: async () => [],
					getActive: async () => null,
				},
				remotes: {
					list: async () => [],
					syncAccountHosts: async () => undefined,
					linkAccountHost: async () => undefined,
					add: async () => "offline" as const,
					update: async () => "offline" as const,
					remove: async () => undefined,
					connect: async (url: string) => {
						throw new Error(`No fake remote for ${url}`);
					},
					disconnect: async () => undefined,
					computerName: async () => "E2E computer",
					previewUrl: async (_hostId: string, _sessionId: string, sourceUrl: string) => sourceUrl,
					resolvePreviewUrl: async (_hostId: string, _sessionId: string, viewedUrl: string) => viewedUrl,
					agentBrowserUrl: async () => "",
					externalUrl: async (_hostId: string, url: string) => url,
				},
				cloud: {
					getSession: async () => null,
					signIn: async () => undefined,
					signOut: async () => undefined,
					cancelProviderAuth: async () => undefined,
					connectProviderAuth: async () => undefined,
					localAuthAvailable: async () => false,
					onOpenShareLink: () => () => undefined,
					takePendingShareLink: async () => null,
					pushFolderToRepository: async () => ({ status: "error" as const, code: "invalid_request" as const, message: "not available in e2e" }),
					previewFolderUpload: async () => ({ status: "error" as const, code: "invalid_request" as const, message: "not available in e2e" }),
					takePendingSignIn: async () => null,
					localRegister: async () => {
						throw new Error("local auth is unavailable in e2e");
					},
					localLogin: async () => {
						throw new Error("local auth is unavailable in e2e");
					},
					onSessionChanged: unsubscribe,
				},
				cloudCp: {
					request: async () => ({ status: 401, headers: {}, body: "" }),
					requestBinary: async () => ({ status: 401, headers: {}, body: new Uint8Array() }),
					cancelBinary: () => undefined,
					openStream: async () => ({ streamId: "stream_test" }),
					closeStream: () => undefined,
					onStreamEvent: unsubscribe,
					agentBrowserUrl: async () => "",
				},
			} satisfies AoBridge;
			(window as unknown as { ao: unknown }).ao = ao;
		},
		{ version, daemonPort, projectId, projectName, platform, workers, nowIso, expandProject },
	);
}
