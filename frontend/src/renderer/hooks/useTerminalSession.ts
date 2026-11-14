import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { getApiBaseUrl } from "../lib/api-client";
import { consumeFreshTerminalHandle } from "../lib/fresh-terminal-handles";
import { captureRendererEvent } from "../lib/telemetry";
import { LOCAL_ECHO_ENABLED, withLineBufferedLocalInput } from "../lib/terminal-local-echo";
import { createTerminalMux, muxUrlFromApiBase, type TerminalMux } from "../lib/terminal-mux";
import { sessionIsActive, type WorkspaceSession } from "../types/workspace";
import { workspaceQueryKeyForHost } from "./useWorkspaceQuery";
import { shellTerminalsQueryKey } from "./useShellTerminals";
import { recordDirectWorkerInteraction } from "../lib/session-management-telemetry";

export type TerminalUserInputSource = "keyboard" | "paste" | "composition" | "shortcut" | "wheel" | "mouse" | "hover" | "protocol";
export type TerminalWriteSource = "live" | "replay";

export type AttachableTerminal = {
	cols: number;
	rows: number;
	hasMeasuredGrid: boolean;
	write: (data: Uint8Array, done?: () => void, source?: TerminalWriteSource) => void;
	writeln: (line: string) => void;
	showLatestOutput: () => void;
	prepareForActivation: () => Promise<void>;
	requestActivationFocus: () => void;
	notifyCursorColorScheme: () => void;
	sendUserInput: (data: string, source?: TerminalUserInputSource) => boolean;
	onUserInput: (listener: (data: string, source: TerminalUserInputSource) => boolean | void) => { dispose: () => void };
	onResize: (listener: (size: { cols: number; rows: number }) => void) => { dispose: () => void };
};

export type TerminalSessionState =
	| "idle"
	| "connecting"
	| "attached"
	| "reattaching"
	| "exited"
	| "error";

export type UseTerminalSessionOptions = {
	daemonReady: boolean;
	inputDisabled?: boolean;
	coverInitialReplay?: boolean;
	waitForInitialOutput?: boolean;
	isVisible?: boolean;
	createMux?: () => TerminalMux;
	exitNotice?: string;
	shellTerminalHandleId?: string;
};

const RETRY_BASE_MS = 500;
const RETRY_MAX_MS = 8_000;
const CLOUD_CONNECT_RETRY_MS = 1_000;
const CLOUD_STARTUP_RETRY_MS = 250;
const CLOUD_STARTUP_WINDOW_MS = 15_000;
const CLOUD_CONNECT_MAX_FAILURES = 8;
const OPEN_TIMEOUT_MS = 3_000;
const REPLAY_QUIET_MS = 60;
const REPLAY_CAP_MS = 750;
const REPLAY_TAIL_QUIET_MS = 180;
const REPLAY_TAIL_CAP_MS = 750;
const REPLAY_MAX_BYTES = 1024 * 1024;
const REPLAY_WRITE_BATCH_BYTES = 256 * 1024;
const REPLAY_FIRST_BYTE_MS = 250;

function defaultCreateMux(): TerminalMux {
	return createTerminalMux(muxUrlFromApiBase(getApiBaseUrl()));
}

export function useTerminalSession(session: WorkspaceSession | undefined, options: UseTerminalSessionOptions) {
	const queryClient = useQueryClient();
	const [state, setState] = useState<TerminalSessionState>("idle");
	const [error, setError] = useState<string | undefined>(undefined);
	const [replaySettled, setReplaySettled] = useState(true);
	const [hasAttached, setHasAttached] = useState(false);

	const sessionRef = useRef(session);
	sessionRef.current = session;
	const previousSessionActiveRef = useRef(session ? sessionIsActive(session) : false);
	const previousActivityStateRef = useRef(session?.activity?.state);
	const optionsRef = useRef(options);
	optionsRef.current = options;
	const stateRef = useRef<TerminalSessionState>(state);
	const connectRef = useRef<() => void>(() => undefined);

	const runtime = useRef({
		terminal: null as AttachableTerminal | null,
		mux: null as TerminalMux | null,
		handle: null as string | null,
		disposers: [] as Array<() => void>,
		retryTimer: null as ReturnType<typeof setTimeout> | null,
		openTimer: null as ReturnType<typeof setTimeout> | null,
		lastPublishedGrid: null as { cols: number; rows: number } | null,
		attempts: 0,
		generation: 0,
		inputReady: false,
		hasAttachedOnce: false,
		cloudConnectFailures: 0,
		connectStartedAt: 0,
		detached: true,
		needsVisibleSizeSync: false,
		replayBuffering: false,
		replayChunks: [] as Uint8Array[],
		replayBytes: 0,
		replayQuietTimer: null as ReturnType<typeof setTimeout> | null,
		replayCapTimer: null as ReturnType<typeof setTimeout> | null,
		replayFirstByteTimer: null as ReturnType<typeof setTimeout> | null,
		replayTailQuietTimer: null as ReturnType<typeof setTimeout> | null,
		replayTailCapTimer: null as ReturnType<typeof setTimeout> | null,
		replayTailPending: false,
		queuedProtocolInputs: [] as string[],
		flushReplay: null as ((preserveBeforeTeardown?: boolean) => void) | null,
	});

	const transition = useCallback((next: TerminalSessionState) => {
		stateRef.current = next;
		setState(next);
	}, []);

	const invalidateWorkspaces = useCallback(() => {
		if (optionsRef.current.shellTerminalHandleId) {
			void queryClient.invalidateQueries({ queryKey: shellTerminalsQueryKey });
			void queryClient.invalidateQueries({ queryKey: ["remote-shell-terminals"] });
			return;
		}
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(sessionRef.current?.hostId) });
	}, [queryClient]);

	const clearReplayTimers = useCallback(() => {
		const r = runtime.current;
		if (r.replayQuietTimer) {
			clearTimeout(r.replayQuietTimer);
			r.replayQuietTimer = null;
		}
		if (r.replayCapTimer) {
			clearTimeout(r.replayCapTimer);
			r.replayCapTimer = null;
		}
		if (r.replayFirstByteTimer) {
			clearTimeout(r.replayFirstByteTimer);
			r.replayFirstByteTimer = null;
		}
		if (r.replayTailQuietTimer) {
			clearTimeout(r.replayTailQuietTimer);
			r.replayTailQuietTimer = null;
		}
		if (r.replayTailCapTimer) {
			clearTimeout(r.replayTailCapTimer);
			r.replayTailCapTimer = null;
		}
	}, []);

	const teardownMux = useCallback(() => {
		const r = runtime.current;
		r.flushReplay?.(true);
		r.flushReplay = null;
		clearReplayTimers();
		r.replayBuffering = false;
		r.replayChunks = [];
		r.replayBytes = 0;
		r.replayTailPending = false;
		r.queuedProtocolInputs = [];
		r.lastPublishedGrid = null;
		setReplaySettled(true);
		if (r.retryTimer) {
			clearTimeout(r.retryTimer);
			r.retryTimer = null;
		}
		if (r.openTimer) {
			clearTimeout(r.openTimer);
			r.openTimer = null;
		}
		r.inputReady = false;
		if (r.mux && r.handle) {
			r.mux.close(r.handle);
		}
		r.disposers.forEach((dispose) => dispose());
		r.disposers = [];
		r.mux?.dispose();
		r.mux = null;
	}, [clearReplayTimers]);

	const isCurrentAttachment = useCallback((generation: number, handle: string, mux: TerminalMux) => {
		const r = runtime.current;
		return !r.detached && r.generation === generation && r.handle === handle && r.mux === mux;
	}, []);

	const clearOpenTimer = useCallback((generation: number) => {
		const r = runtime.current;
		if (r.generation !== generation || !r.openTimer) return;
		clearTimeout(r.openTimer);
		r.openTimer = null;
	}, []);

	const scheduleReattach = useCallback((countAsCloudFailure = false) => {
		const r = runtime.current;
		if (r.detached || !r.terminal || !r.handle) {
			return;
		}
		if (stateRef.current === "exited" || stateRef.current === "error") {
			return;
		}
		transition("reattaching");
		if (!optionsRef.current.daemonReady && !sessionRef.current?.cloud) {
			return;
		}
		if (r.retryTimer) {
			return;
		}
		if (countAsCloudFailure && !r.hasAttachedOnce && sessionRef.current?.cloud) {
			r.cloudConnectFailures += 1;
			if (r.cloudConnectFailures >= CLOUD_CONNECT_MAX_FAILURES) {
				setError(
					"Terminal ticket issued but the WebSocket cannot connect. " +
						"Check proxy, firewall, or CSP settings.",
				);
				transition("error");
				return;
			}
		}
		const cloudStartup = !r.hasAttachedOnce && sessionRef.current?.cloud;
		const delay = !cloudStartup
			? Math.min(RETRY_BASE_MS * 2 ** r.attempts, RETRY_MAX_MS)
			: !countAsCloudFailure && Date.now() - r.connectStartedAt < CLOUD_STARTUP_WINDOW_MS
				? CLOUD_STARTUP_RETRY_MS
				: CLOUD_CONNECT_RETRY_MS;
		r.attempts += 1;
		r.retryTimer = setTimeout(() => {
			r.retryTimer = null;
			connectRef.current();
		}, delay);
	}, [transition]);

	const connect = useCallback(() => {
		const r = runtime.current;
		const { terminal, handle } = r;
		if (!terminal || !handle || r.detached) {
			return;
		}
		r.flushReplay?.(true);
		const generation = r.generation + 1;
		r.generation = generation;
		r.inputReady = false;
		teardownMux();

		const baseMux = (optionsRef.current.createMux ?? defaultCreateMux)();
		const localEchoSafeHarness = sessionRef.current?.provider !== "opencode";
		const mux =
			LOCAL_ECHO_ENABLED && sessionRef.current?.cloud && localEchoSafeHarness
				? withLineBufferedLocalInput(baseMux, {})
				: baseMux;
		r.mux = mux;

		let liveChunks: Uint8Array[] = [];
		let liveBytes = 0;
		let liveWriteInFlight = false;
		const takeLiveBatch = (): Uint8Array | null => {
			if (liveChunks.length === 0) return null;
			let batch = liveChunks[0]!;
			if (liveChunks.length > 1) {
				batch = new Uint8Array(liveBytes);
				let offset = 0;
				for (const chunk of liveChunks) {
					batch.set(chunk, offset);
					offset += chunk.length;
				}
			}
			liveChunks = [];
			liveBytes = 0;
			return batch;
		};
		const writeLiveBatch = () => {
			const batch = takeLiveBatch();
			if (!batch) return;
			liveWriteInFlight = true;
			terminal.write(batch, () => {
				liveWriteInFlight = false;
				if (isCurrentAttachment(generation, handle, mux)) writeLiveBatch();
			});
		};
		const writeLiveOutput = (bytes: Uint8Array) => {
			liveChunks.push(bytes);
			liveBytes += bytes.length;
			if (!liveWriteInFlight) writeLiveBatch();
		};
		const drainLiveOutput = () => {
			const batch = takeLiveBatch();
			if (batch) terminal.write(batch);
		};

		let pendingReplayWrites = 0;
		let replayRevealDeadlineReached = false;
		const postReplayWriteQueue: Uint8Array[] = [];
		let postReplayWriteActive = false;
		let replayBatchBytes: Uint8Array | null = null;
		let replayBatchOffset = 0;
		let replayBatchTimer: ReturnType<typeof setTimeout> | null = null;
		let replayBatchDone: (() => void) | null = null;
		let replayWritesPreserved = false;
		const initialWriteSource: TerminalWriteSource = consumeFreshTerminalHandle(handle) ? "live" : "replay";

		const revealReplayTail = () => {
			if (!r.replayTailPending || !isCurrentAttachment(generation, handle, mux)) return;
			if (r.replayTailQuietTimer) {
				clearTimeout(r.replayTailQuietTimer);
				r.replayTailQuietTimer = null;
			}
			if (r.replayTailCapTimer) {
				clearTimeout(r.replayTailCapTimer);
				r.replayTailCapTimer = null;
			}
			if (pendingReplayWrites > 0) {
				replayRevealDeadlineReached = true;
				return;
			}
			r.replayTailPending = false;
			replayRevealDeadlineReached = false;
			terminal.showLatestOutput();
			setReplaySettled(true);
		};
		const scheduleReplayTailReveal = () => {
			if (!r.replayTailPending || !isCurrentAttachment(generation, handle, mux)) return;
			if (pendingReplayWrites > 0) return;
			if (replayRevealDeadlineReached) {
				revealReplayTail();
				return;
			}
			if (r.replayTailQuietTimer) clearTimeout(r.replayTailQuietTimer);
			r.replayTailQuietTimer = setTimeout(revealReplayTail, REPLAY_TAIL_QUIET_MS);
		};
		const holdReplayTail = () => {
			r.replayTailPending = true;
			if (!r.replayTailCapTimer) {
				r.replayTailCapTimer = setTimeout(revealReplayTail, REPLAY_TAIL_CAP_MS);
			}
		};
		const writeReplayBatches = (bytes: Uint8Array, done: () => void) => {
			replayBatchBytes = bytes;
			replayBatchOffset = 0;
			replayBatchDone = done;
			const writeNext = () => {
				replayBatchTimer = null;
				if (replayWritesPreserved) return;
				if (!isCurrentAttachment(generation, handle, mux)) return;
				const current = replayBatchBytes;
				if (!current) return;
				const end = Math.min(current.length, replayBatchOffset + REPLAY_WRITE_BATCH_BYTES);
				const batch = current.subarray(replayBatchOffset, end);
				replayBatchOffset = end;
				terminal.write(batch, () => {
					if (replayWritesPreserved) return;
					if (!isCurrentAttachment(generation, handle, mux)) return;
					if (replayBatchOffset >= current.length) {
						const finished = replayBatchDone;
						replayBatchBytes = null;
						replayBatchOffset = 0;
						replayBatchDone = null;
						finished?.();
						return;
					}
					replayBatchTimer = setTimeout(writeNext, 0);
				}, initialWriteSource);
			};
			writeNext();
		};
		const preservePendingReplayWrites = () => {
			if (replayWritesPreserved) return;
			replayWritesPreserved = true;
			if (replayBatchTimer !== null) {
				clearTimeout(replayBatchTimer);
				replayBatchTimer = null;
			}
			if (replayBatchBytes && replayBatchOffset < replayBatchBytes.length) {
				terminal.write(replayBatchBytes.subarray(replayBatchOffset), undefined, initialWriteSource);
			}
			replayBatchBytes = null;
			replayBatchOffset = 0;
			replayBatchDone = null;
			for (const bytes of postReplayWriteQueue) terminal.write(bytes, undefined, initialWriteSource);
			postReplayWriteQueue.length = 0;
			postReplayWriteActive = false;
			pendingReplayWrites = 0;
			r.replayTailPending = false;
			terminal.showLatestOutput();
			setReplaySettled(true);
		};
		const settleAfterReplayWrites = () => {
			if (pendingReplayWrites > 0 || postReplayWriteActive || postReplayWriteQueue.length > 0) return;
			if (r.replayTailPending) {
				scheduleReplayTailReveal();
				return;
			}
			terminal.showLatestOutput();
			setReplaySettled(true);
		};
		let tailHoldsHistory = false;
		const drainPostReplayWrites = () => {
			if (!isCurrentAttachment(generation, handle, mux)) return;
			if (pendingReplayWrites > 0 || postReplayWriteActive) return;
			const bytes = postReplayWriteQueue.shift();
			if (!bytes) {
				settleAfterReplayWrites();
				return;
			}
			postReplayWriteActive = true;
			pendingReplayWrites += 1;
			terminal.write(bytes, () => {
				if (replayWritesPreserved) return;
				if (!isCurrentAttachment(generation, handle, mux)) return;
				postReplayWriteActive = false;
				pendingReplayWrites = Math.max(0, pendingReplayWrites - 1);
				drainPostReplayWrites();
			}, tailHoldsHistory ? initialWriteSource : "live");
		};

		const flushReplay = (holdTail = false, preserveBeforeTeardown = false) => {
			if (!r.replayBuffering) {
				if (preserveBeforeTeardown) preservePendingReplayWrites();
				return;
			}
			if (!isCurrentAttachment(generation, handle, mux)) return;
			r.replayBuffering = false;
			clearReplayTimers();
			if (holdTail) holdReplayTail();

			const chunks = r.replayChunks;
			r.replayChunks = [];
			r.replayBytes = 0;

			if (chunks.length === 0) {
				if (holdTail) scheduleReplayTailReveal();
				else setReplaySettled(true);
				return;
			}

			let total = 0;
			for (const chunk of chunks) total += chunk.length;
			const replay = new Uint8Array(total);
			let offset = 0;
			for (const chunk of chunks) {
				replay.set(chunk, offset);
				offset += chunk.length;
			}
			if (preserveBeforeTeardown) {
				terminal.write(replay, undefined, initialWriteSource);
				preservePendingReplayWrites();
				return;
			}
			pendingReplayWrites += 1;
			writeReplayBatches(replay, () => {
				if (!isCurrentAttachment(generation, handle, mux)) return;
				pendingReplayWrites = Math.max(0, pendingReplayWrites - 1);
				drainPostReplayWrites();
			});
		};
		r.flushReplay = (preserveBeforeTeardown = false) =>
			flushReplay(false, preserveBeforeTeardown);

		r.disposers.push(
			mux.onData(handle, (bytes) => {
				if (!isCurrentAttachment(generation, handle, mux)) return;
				if (r.replayBuffering) {
					r.replayChunks.push(bytes);
					r.replayBytes += bytes.length;
					if (r.replayBytes >= REPLAY_MAX_BYTES) {
						tailHoldsHistory = initialWriteSource === "replay";
						flushReplay(true);
						return;
					}
					if (r.replayQuietTimer) clearTimeout(r.replayQuietTimer);
					r.replayQuietTimer = setTimeout(() => flushReplay(true), REPLAY_QUIET_MS);
					return;
				}
				if (pendingReplayWrites > 0 || postReplayWriteActive || r.replayTailPending) {
					if (r.replayTailQuietTimer) {
						clearTimeout(r.replayTailQuietTimer);
						r.replayTailQuietTimer = null;
					}
					drainLiveOutput();
					postReplayWriteQueue.push(bytes);
					drainPostReplayWrites();
					return;
				}
				writeLiveOutput(bytes);
			}),
			drainLiveOutput,
			mux.onOpened(handle, () => {
				if (!isCurrentAttachment(generation, handle, mux)) return;
				clearOpenTimer(generation);
				r.inputReady = true;
				r.attempts = 0;
				r.cloudConnectFailures = 0;
				r.hasAttachedOnce = true;
				setError(undefined);
				setHasAttached(true);
				transition("attached");
				terminal.notifyCursorColorScheme();
				flushQueuedProtocolInputs();
				if (r.replayBuffering && !optionsRef.current.waitForInitialOutput && !r.replayCapTimer) {
					r.replayCapTimer = setTimeout(() => flushReplay(true), REPLAY_CAP_MS);
				}
				if (r.replayBuffering && !optionsRef.current.waitForInitialOutput && !r.replayFirstByteTimer) {
					r.replayFirstByteTimer = setTimeout(() => {
						r.replayFirstByteTimer = null;
						if (!isCurrentAttachment(generation, handle, mux)) return;
						if (!r.replayBuffering || r.replayChunks.length > 0) return;
						setReplaySettled(true);
					}, REPLAY_FIRST_BYTE_MS);
				}
			}),
			mux.onExit(handle, () => {
				if (!isCurrentAttachment(generation, handle, mux)) return;
				clearOpenTimer(generation);
				r.inputReady = false;
				flushReplay(false, true);
				terminal.writeln(optionsRef.current.exitNotice ?? "\r\n\x1b[2m[process exited]\x1b[0m");
				transition("exited");
				teardownMux();
				invalidateWorkspaces();
			}),
			mux.onError(handle, (message) => {
				if (!isCurrentAttachment(generation, handle, mux)) return;
				clearOpenTimer(generation);
				r.inputReady = false;
				flushReplay(false, true);
				terminal.writeln(`\r\n\x1b[2m[terminal error] ${message}\x1b[0m`);
				setError(message);
				transition("error");
				teardownMux();
				void captureRendererEvent("ao.terminals.attach_failed", { reason: "pane_error" });
				invalidateWorkspaces();
			}),
			mux.onConnectionChange((connectionState) => {
				if (!isCurrentAttachment(generation, handle, mux)) return;
				if (connectionState === "closed" || connectionState === "waiting") {
					flushReplay(false, true);
					clearOpenTimer(generation);
					r.inputReady = false;
					scheduleReattach(connectionState === "closed");
				}
			}),
		);
		const flushQueuedProtocolInputs = () => {
			if (!isCurrentAttachment(generation, handle, mux) || !r.inputReady) return;
			for (const queued of r.queuedProtocolInputs) {
				mux.sendInput(handle, queued);
			}
			r.queuedProtocolInputs = [];
		};
		const input = terminal.onUserInput((data, source) => {
			if (!isCurrentAttachment(generation, handle, mux)) {
				return false;
			}
			if (source === "protocol") {
				if (!r.inputReady) {
					r.queuedProtocolInputs.push(data);
					return true;
				}
				mux.sendInput(handle, data);
				return true;
			}
			if (!r.inputReady) {
				return false;
			}
			if (optionsRef.current.inputDisabled || optionsRef.current.isVisible === false) {
				return false;
			}
			if (source === "hover") {
				mux.sendInput(handle, data);
				return true;
			}
			if (r.replayBuffering) flushReplay();
			else revealReplayTail();
			if (session) recordDirectWorkerInteraction(session.id, "terminal", session.kind, session.hostId);
			mux.sendInput(handle, data);
			return true;
		});
		const resize = terminal.onResize(({ cols, rows }) => {
			if (!isCurrentAttachment(generation, handle, mux)) return;
			if (optionsRef.current.isVisible === false) return;
			const published = r.lastPublishedGrid;
			if (published?.cols === cols && published.rows === rows) return;
			mux.resize(handle, cols, rows);
			r.lastPublishedGrid = { cols, rows };
		});
		r.disposers.push(
			() => input.dispose(),
			() => resize.dispose(),
		);

		const coverInitialReplay = optionsRef.current.coverInitialReplay !== false && initialWriteSource !== "live";
		r.replayBuffering = coverInitialReplay;
		r.replayChunks = [];
		r.replayBytes = 0;
		setReplaySettled(!coverInitialReplay);

		const claimsSize = optionsRef.current.isVisible !== false && terminal.hasMeasuredGrid;
		r.needsVisibleSizeSync = !claimsSize;
		const openCols = claimsSize ? terminal.cols : 0;
		const openRows = claimsSize ? terminal.rows : 0;
		mux.open(handle, openCols, openRows);
		r.lastPublishedGrid =
			openCols > 0 && openRows > 0 ? { cols: openCols, rows: openRows } : null;
		if (!sessionRef.current?.cloud) {
			r.openTimer = setTimeout(() => {
				if (!isCurrentAttachment(generation, handle, mux)) return;
				r.openTimer = null;
				if (r.attempts === 0) {
					void captureRendererEvent("ao.terminals.attach_failed", { reason: "open_timeout" });
				}
				transition("reattaching");
				teardownMux();
				scheduleReattach();
			}, OPEN_TIMEOUT_MS);
		}
	}, [
		clearOpenTimer,
		clearReplayTimers,
		invalidateWorkspaces,
		isCurrentAttachment,
		scheduleReattach,
		teardownMux,
		transition,
	]);
	connectRef.current = connect;

	const attach = useCallback(
		(terminal: AttachableTerminal) => {
			const r = runtime.current;
			const handle = optionsRef.current.shellTerminalHandleId ?? sessionRef.current?.terminalHandleId ?? null;
			r.terminal = terminal;
			r.handle = handle;
			r.detached = false;
			r.attempts = 0;
			r.cloudConnectFailures = 0;
			r.hasAttachedOnce = false;
			r.connectStartedAt = Date.now();
			setError(undefined);
			setHasAttached(false);
			if (handle) {
				if (optionsRef.current.daemonReady || Boolean(sessionRef.current?.cloud)) {
					transition("connecting");
					connect();
				} else {
					transition("reattaching");
				}
			} else {
				transition("idle");
			}
			return () => {
				r.flushReplay?.(true);
				r.generation += 1;
				r.detached = true;
				teardownMux();
				r.terminal = null;
				r.handle = null;
				r.inputReady = false;
				r.needsVisibleSizeSync = false;
				setError(undefined);
				setReplaySettled(true);
				transition("idle");
			};
		},
		[connect, teardownMux, transition],
	);

	const syncVisibleSize = useCallback((cols: number, rows: number) => {
		const r = runtime.current;
		if (
			optionsRef.current.isVisible === false ||
			r.detached ||
			!r.mux ||
			!r.handle ||
			!r.needsVisibleSizeSync ||
			cols <= 0 ||
			rows <= 0
		) {
			return;
		}
		r.needsVisibleSizeSync = false;
		const published = r.lastPublishedGrid;
		if (published?.cols === cols && published.rows === rows) return;
		r.mux.resize(r.handle, cols, rows);
		r.lastPublishedGrid = { cols, rows };
	}, []);

	const daemonReady = options.daemonReady;
	useEffect(() => {
		const r = runtime.current;
		if (!daemonReady || r.detached) return;
		if (stateRef.current !== "reattaching" || r.retryTimer) return;
		r.attempts = 0;
		connect();
	}, [daemonReady, connect]);

	const parkOffScreen = useCallback(() => {
		const r = runtime.current;
		r.needsVisibleSizeSync = true;
		if (!r.mux || !r.handle || !r.lastPublishedGrid || sessionRef.current?.cloud) return;
		r.mux.resize(r.handle, 0, 0);
		r.lastPublishedGrid = null;
	}, []);

	const isVisible = options.isVisible !== false;
	useLayoutEffect(() => {
		if (!isVisible) parkOffScreen();
	}, [isVisible, parkOffScreen]);

	useEffect(() => {
		const onWindowVisibility = () => {
			const terminal = runtime.current.terminal;
			if (document.visibilityState === "hidden") parkOffScreen();
			else if (terminal?.hasMeasuredGrid) syncVisibleSize(terminal.cols, terminal.rows);
		};
		document.addEventListener("visibilitychange", onWindowVisibility);
		return () => document.removeEventListener("visibilitychange", onWindowVisibility);
	}, [parkOffScreen, syncVisibleSize]);

	useEffect(() => {
		const r = runtime.current;
		const handle = session?.terminalHandleId ?? null;
		const isActive = session ? sessionIsActive(session) : false;
		const wasActive = previousSessionActiveRef.current;
		const previousActivityState = previousActivityStateRef.current;
		previousSessionActiveRef.current = isActive;
		previousActivityStateRef.current = session?.activity?.state;
		const restoredSession = !wasActive && isActive;
		const resumedAgent = previousActivityState === "exited" && session?.activity?.state !== "exited";
		if (!handle || (!restoredSession && !resumedAgent) || r.detached || !r.terminal) {
			return;
		}
		if (r.handle !== handle) return;
		if (stateRef.current !== "exited" && stateRef.current !== "error") return;
		if (optionsRef.current.daemonReady) {
			transition("connecting");
			connect();
		} else {
			transition("reattaching");
		}
	}, [
		connect,
		session?.activity?.state,
		session?.isTerminated,
		session?.status,
		session?.terminalHandleId,
		transition,
	]);

	useEffect(
		() => () => {
			const r = runtime.current;
			r.flushReplay?.(true);
			r.generation += 1;
			r.detached = true;
			r.inputReady = false;
			teardownMux();
		},
		[teardownMux],
	);

	useEffect(() => {
		if (!replaySettled) return;
		runtime.current.terminal?.notifyCursorColorScheme();
	}, [replaySettled]);

	return { attach, state, error, replaySettled, hasAttached, syncVisibleSize };
}
