import { useQueryClient } from "@tanstack/react-query";
import { RotateCcw } from "@/components/app-icons";
import {
	createContext,
	lazy,
	Suspense,
	useCallback,
	useContext,
	useEffect,
	useLayoutEffect,
	useMemo,
	useRef,
	useState,
	type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { terminalTargetBelongsToSession, type TerminalTarget } from "../types/terminal";
import { sessionAgentExited, sessionIsActive, type WorkspaceSession } from "../types/workspace";
import { ResumeAgentControl } from "./ResumeAgentControl";
import type { Theme } from "../stores/ui-store";
import {
	useTerminalSession,
	type AttachableTerminal,
	type TerminalSessionState,
} from "../hooks/useTerminalSession";
import { useSessionBrowserLink } from "../hooks/useSessionBrowserLink";
import { useSessionLinkNavigation } from "../lib/use-session-link-navigation";
import { getApiBaseUrl } from "../lib/api-client";
import {
	createTerminalMux,
	createTerminalMuxPool,
	muxUrlFromApiBase,
	type TerminalMux,
	type TerminalMuxPool,
} from "../lib/terminal-mux";
import { cn } from "../lib/utils";
import { useWorkspaceQuery, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { useRestoreSession } from "../hooks/useRestoreSession";
import { useShellTerminals } from "../hooks/useShellTerminals";
import { placeCloudAttachment, uploadCloudAttachment } from "../lib/cloud-attachments";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { MAX_CLOUD_CP_BINARY_BYTES } from "../../shared/cloud-binary";
import { terminalResetNonce, useTerminalResetStore } from "../stores/terminal-reset-store";
import { createCloudTerminalMux } from "../lib/cloud-terminal-mux";
import { takeCloudShellInput } from "../lib/cloud-shell-input";
import { LazyChunkBoundary } from "./LazyChunkBoundary";

const loadXtermTerminal = () => import("./XtermTerminal");
const XtermTerminal = lazy(() => loadXtermTerminal().then((module) => ({ default: module.XtermTerminal })));

export function preloadXtermTerminal(): void {
	void loadXtermTerminal().catch(() => undefined);
}
import { RestoreUnavailableDialog } from "./RestoreUnavailableDialog";
import { rememberPaneTerminalGrid } from "../lib/terminal-grid-hint";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { captureRendererException } from "../lib/telemetry";

type TerminalPaneProps = {
	session?: WorkspaceSession;
	terminalGeneration?: string;
	theme: Theme;
	daemonReady: boolean;
	terminalTarget?: TerminalTarget;
	fontSize: number;
	onChangeFontSize?: (delta: number) => void;
	isFullscreen?: boolean;
	onToggleFullscreen?: () => void | Promise<void>;
	inputDisabled?: boolean;
	booting?: boolean;
	switchingLabel?: string;
	focusRequested?: boolean;
	onTerminalStateChange?: (state: TerminalSessionState) => void;
	onTerminalContentReadyChange?: (ready: boolean) => void;
	inputRequest?: { id: number; data: string };
	onInputRequestResult?: (id: number, accepted: boolean) => void;
	createMux?: () => TerminalMux;
};

type TerminalCacheDescriptor = {
	cacheKey: string;
	generation?: string;
	handleId: string;
	kind: "shell" | "worker";
	ownerKey: string;
	sessionId?: string;
};

type CachedTerminalEntry = TerminalCacheDescriptor & {
	activationId: number;
	activationPhase: "parked" | "visible";
	container: HTMLDivElement;
	discardOnDeactivate?: boolean;
	props: TerminalPaneProps;
	terminal?: AttachableTerminal;
};

function isUnlisted(entry: CachedTerminalEntry, listed: Set<string> | null): boolean {
	return Boolean(entry.sessionId) && listed !== null && !listed.has(entry.sessionId ?? "");
}

type TerminalCacheController = {
	activate: (descriptor: TerminalCacheDescriptor, props: TerminalPaneProps, slot: HTMLDivElement) => void;
	deactivate: (cacheKey: string, slot: HTMLDivElement) => void;
	update: (cacheKey: string, props: TerminalPaneProps) => void;
	resolveCreateMux: (session?: WorkspaceSession, terminalTarget?: TerminalTarget) => () => TerminalMux;
};

const TerminalCacheContext = createContext<TerminalCacheController | null>(null);

function terminalTargetMatches(left?: TerminalTarget, right?: TerminalTarget): boolean {
	if (left === right) return true;
	if (!left || !right || left.kind !== right.kind) return false;
	if (left.kind === "worker" && right.kind === "worker") return true;
	if (left.kind === "reviewer" && right.kind === "reviewer") {
		return left.handleId === right.handleId && left.harness === right.harness;
	}
	if (left.kind === "shell" && right.kind === "shell") {
		return (
			left.handleId === right.handleId &&
			left.generation === right.generation &&
			left.title === right.title
		);
	}
	return false;
}

function terminalPropsMatch(left: TerminalPaneProps, right: TerminalPaneProps): boolean {
	return (
		left.session === right.session &&
		left.theme === right.theme &&
		left.daemonReady === right.daemonReady &&
		left.fontSize === right.fontSize &&
		left.onChangeFontSize === right.onChangeFontSize &&
		left.isFullscreen === right.isFullscreen &&
		left.onToggleFullscreen === right.onToggleFullscreen &&
		left.inputDisabled === right.inputDisabled &&
		left.booting === right.booting &&
		left.switchingLabel === right.switchingLabel &&
		left.focusRequested === right.focusRequested &&
		left.onTerminalStateChange === right.onTerminalStateChange &&
		left.onTerminalContentReadyChange === right.onTerminalContentReadyChange &&
		left.inputRequest === right.inputRequest &&
		left.onInputRequestResult === right.onInputRequestResult &&
		left.createMux === right.createMux &&
		terminalTargetMatches(left.terminalTarget, right.terminalTarget)
	);
}

function cacheDescriptor(
	session: WorkspaceSession | undefined,
	terminalTarget: TerminalTarget | undefined,
	terminalGeneration?: string,
): TerminalCacheDescriptor | null {
	if (terminalTarget?.kind === "shell") {
		if (!terminalTargetBelongsToSession(terminalTarget, session?.id)) return null;
		const ownerKey = `shell:${session?.id ?? "standalone"}:${terminalTarget.handleId}`;
		return {
			cacheKey: `${ownerKey}|handle:${terminalTarget.handleId}|generation:${terminalTarget.generation}`,
			generation: terminalTarget.generation,
			handleId: terminalTarget.handleId,
			kind: "shell",
			ownerKey,
			sessionId: session?.id,
		};
	}

	if (terminalTarget?.kind === "reviewer") return null;
	const handleId = session?.terminalHandleId;
	if (!session?.id || !handleId) return null;
	const ownerKey = `session:${session.id}:worker`;
	const generation = terminalGeneration ?? session.terminalGeneration ?? "";
	const nonce = terminalResetNonce(session.id);
	return {
		cacheKey: `${ownerKey}|handle:${handleId}|reset:${nonce}`,
		generation,
		handleId,
		kind: "worker",
		ownerKey,
		sessionId: session.id,
	};
}

export function cloudTerminalKind(terminalTarget?: TerminalTarget): "agent" | "workspace" {
	return terminalTarget?.kind === "shell" ? "workspace" : "agent";
}

function blurTerminal(container: HTMLElement): void {
	const active = document.activeElement;
	if (active instanceof HTMLElement && container.contains(active)) {
		active.blur();
	}
}

function setTerminalPhase(
	entry: CachedTerminalEntry,
	phase: CachedTerminalEntry["activationPhase"],
): void {
	entry.activationPhase = phase;
	entry.container.dataset.terminalActivationPhase = phase;
	const interactive = phase === "visible";
	entry.container.inert = !interactive;
	if (interactive) {
		entry.container.removeAttribute("aria-hidden");
		entry.container.style.pointerEvents = "";
	} else {
		entry.container.setAttribute("aria-hidden", "true");
		entry.container.style.pointerEvents = "none";
	}
	if (interactive) {
		entry.container.style.visibility = "";
		entry.container.style.contentVisibility = "";
	} else {
		entry.container.style.visibility = "hidden";
		entry.container.style.contentVisibility = "hidden";
	}
}

function parkTerminal(entry: CachedTerminalEntry, parking: HTMLDivElement): void {
	entry.activationId += 1;
	blurTerminal(entry.container);
	setTerminalPhase(entry, "parked");
	parking.appendChild(entry.container);
}

function showTerminal(entry: CachedTerminalEntry, slot: HTMLDivElement): void {
	entry.activationId += 1;
	entry.container.style.width = "100%";
	entry.container.style.height = "100%";
	slot.appendChild(entry.container);
	setTerminalPhase(entry, "visible");
}

function CachedTerminalPortal({
	active,
	entry,
	onFatal,
	onTerminalReady,
}: {
	active: boolean;
	entry: CachedTerminalEntry;
	onFatal: (cacheKey: string, message: string) => void;
	onTerminalReady: (cacheKey: string, terminal: AttachableTerminal) => void;
}) {
	const handleFatal = useCallback(
		(message: string) => onFatal(entry.cacheKey, message),
		[entry.cacheKey, onFatal],
	);
	const handleTerminalReady = useCallback(
		(terminal: AttachableTerminal) => {
			onTerminalReady(entry.cacheKey, terminal);
		},
		[entry.cacheKey, onTerminalReady],
	);
	useLayoutEffect(() => {
		const terminal = entry.terminal;
		if (!active || entry.activationPhase !== "visible" || !terminal) return;
		void terminal.prepareForActivation();
		terminal.requestActivationFocus();
	}, [
		active,
		entry,
		entry.activationId,
		entry.activationPhase,
		entry.terminal,
	]);
	return createPortal(
		<AttachedTerminal
			{...entry.props}
			isVisible={active && entry.activationPhase === "visible"}
			onFatal={handleFatal}
			onTerminalReady={handleTerminalReady}
		/>,
		entry.container,
		entry.cacheKey,
	);
}

export function TerminalCacheProvider({
	children,
	daemonReady,
	theme,
}: {
	children: ReactNode;
	daemonReady: boolean;
	theme: Theme;
}) {
	const workspaceQuery = useWorkspaceQuery();
	const shellTerminalsQuery = useShellTerminals();
	const entriesRef = useRef(new Map<string, CachedTerminalEntry>());
	const activeRef = useRef(new Map<string, HTMLDivElement>());
	const parkingRef = useRef<HTMLDivElement | null>(null);
	const listedSessionsRef = useRef<Set<string> | null>(null);
	const muxPoolRef = useRef<TerminalMuxPool | null>(null);
	if (!muxPoolRef.current) {
		muxPoolRef.current = createTerminalMuxPool(() =>
			createTerminalMux(muxUrlFromApiBase(getApiBaseUrl())),
		);
	}
	const muxPool = muxPoolRef.current;
	const { client: cloudClient, baseUrl: cloudBaseUrl } = useCloudCp();
	const cloudCpRef = useRef({ client: cloudClient, baseUrl: cloudBaseUrl });
	cloudCpRef.current = { client: cloudClient, baseUrl: cloudBaseUrl };
	const cloudMuxFactoriesRef = useRef(new Map<string, () => TerminalMux>());
	const resolveCreateMux = useCallback(
		(paneSession?: WorkspaceSession, terminalTarget?: TerminalTarget): (() => TerminalMux) => {
			const cloud = paneSession?.cloud;
			if (!cloud) return muxPool.acquire;
			const kind = cloudTerminalKind(terminalTarget);
			const identity = terminalTarget?.kind === "shell"
				? terminalTarget.handleId
				: terminalTarget?.kind === "reviewer" ? `reviewer:${terminalTarget.handleId}` : "agent";
			const factoryKey = `${paneSession.id}:${kind}:${identity}:${terminalResetNonce(paneSession.id)}`;
			const cached = cloudMuxFactoriesRef.current.get(factoryKey);
			if (cached) return cached;
			const sessionId = paneSession.id;
			const orgId = cloud.orgId;
			const reviewerTerminalID = terminalTarget?.kind === "reviewer" ? terminalTarget.handleId : undefined;
			const cursor = { value: 0 };
			const shellHandle = terminalTarget?.kind === "shell" ? terminalTarget.handleId : undefined;
			const factory = () => {
				const input = shellHandle ? takeCloudShellInput(shellHandle) : undefined;
				return createCloudTerminalMux({
					wsBaseUrl: `${cloudCpRef.current.baseUrl.replace(/^http/i, "ws").replace(/\/+$/, "")}/api/cloud/v1`,
					kind,
					terminalId: reviewerTerminalID,
					cursor,
					initialInput: input,
					mintTicket: async (ticketKind) => {
						const response = await cloudCpRef.current.client.createTerminalTicket(orgId, sessionId, {
							kind: ticketKind,
							...(reviewerTerminalID ? { terminalId: reviewerTerminalID } : {}),
						});
						return response.ticket;
					},
				});
			};
			cloudMuxFactoriesRef.current.set(factoryKey, factory);
			return factory;
		},
		[muxPool],
	);
	const [, setRevision] = useState(0);
	const rerender = useCallback(() => setRevision((current) => current + 1), []);

	const removeEntry = useCallback(
		(cacheKey: string) => {
			const entry = entriesRef.current.get(cacheKey);
			if (!entry) return;
			if (activeRef.current.has(cacheKey)) {
				blurTerminal(entry.container);
				setTerminalPhase(entry, "parked");
				activeRef.current.delete(cacheKey);
			}
			entriesRef.current.delete(cacheKey);
			entry.container.remove();
			rerender();
		},
		[rerender],
	);

	const disposeIfUnlistedOnceParked = useCallback(
		(cacheKey: string) => {
			queueMicrotask(() => {
				const entry = entriesRef.current.get(cacheKey);
				if (!entry || activeRef.current.has(cacheKey)) return;
				if (isUnlisted(entry, listedSessionsRef.current)) removeEntry(cacheKey);
			});
		},
		[removeEntry],
	);

	const activate = useCallback(
		(descriptor: TerminalCacheDescriptor, props: TerminalPaneProps, slot: HTMLDivElement) => {
			const parking = parkingRef.current;
			if (!parking) return;
			const cachedProps = { ...props, createMux: resolveCreateMux(props.session, props.terminalTarget) };

			for (const [previousKey, previousSlot] of [...activeRef.current]) {
				if (previousSlot !== slot || previousKey === descriptor.cacheKey) continue;
				activeRef.current.delete(previousKey);
				const previousEntry = entriesRef.current.get(previousKey);
				if (!previousEntry) continue;
				parkTerminal(previousEntry, parking);
				if (previousEntry.discardOnDeactivate) {
					entriesRef.current.delete(previousKey);
					previousEntry.container.remove();
				} else {
					disposeIfUnlistedOnceParked(previousKey);
				}
			}

			for (const entry of entriesRef.current.values()) {
				if (entry.ownerKey === descriptor.ownerKey && entry.cacheKey !== descriptor.cacheKey) {
					if (activeRef.current.delete(entry.cacheKey)) parkTerminal(entry, parking);
					entriesRef.current.delete(entry.cacheKey);
					entry.container.remove();
				}
			}

			let entry = entriesRef.current.get(descriptor.cacheKey);
			if (!entry) {
				const container = document.createElement("div");
				container.className = "h-full min-h-0 w-full overflow-hidden bg-terminal-opaque";
				container.style.position = "relative";
				container.dataset.terminalCacheKey = descriptor.cacheKey;
				entry = {
					...descriptor,
					activationId: 0,
					activationPhase: "parked",
					container,
					props: cachedProps,
				};
				entriesRef.current.set(entry.cacheKey, entry);
			} else {
				entry.props = cachedProps;
			}
			showTerminal(entry, slot);
			activeRef.current.set(entry.cacheKey, slot);
			rerender();
		},
		[disposeIfUnlistedOnceParked, muxPool, rerender],
	);

	const deactivate = useCallback(
		(cacheKey: string, slot: HTMLDivElement) => {
			if (activeRef.current.get(cacheKey) !== slot) return;
			const entry = entriesRef.current.get(cacheKey);
			const parking = parkingRef.current;
			activeRef.current.delete(cacheKey);
			if (!entry) return;
			if (entry.discardOnDeactivate || !parking) {
				entriesRef.current.delete(cacheKey);
				entry.container.remove();
				rerender();
				return;
			}
			parkTerminal(entry, parking);
			disposeIfUnlistedOnceParked(cacheKey);
			rerender();
		},
		[disposeIfUnlistedOnceParked, rerender],
	);

	const update = useCallback(
		(cacheKey: string, props: TerminalPaneProps) => {
			const entry = entriesRef.current.get(cacheKey);
			const cachedProps = { ...props, createMux: resolveCreateMux(props.session, props.terminalTarget) };
			if (!entry || terminalPropsMatch(entry.props, cachedProps)) return;
			entry.props = cachedProps;
			rerender();
		},
		[muxPool, rerender],
	);

	const markFatal = useCallback(
		(cacheKey: string, _message: string) => {
			const entry = entriesRef.current.get(cacheKey);
			if (!entry) return;
			if (!activeRef.current.has(cacheKey)) {
				removeEntry(cacheKey);
				return;
			}
			entry.discardOnDeactivate = true;
		},
		[removeEntry],
	);

	const markTerminalReady = useCallback(
		(cacheKey: string, terminal: AttachableTerminal) => {
			const entry = entriesRef.current.get(cacheKey);
			if (!entry) return;
			entry.terminal = terminal;
			rerender();
		},
		[rerender],
	);

	useEffect(() => {
		let changed = false;
		for (const entry of entriesRef.current.values()) {
			if (entry.props.daemonReady === daemonReady && entry.props.theme === theme) continue;
			entry.props = { ...entry.props, daemonReady, theme };
			changed = true;
		}
		if (changed) rerender();
	}, [daemonReady, rerender, theme]);

	useEffect(() => {
		if (!workspaceQuery.isSuccess) return;
		const sessions = new Map(
			(workspaceQuery.data ?? []).flatMap((workspace) =>
				workspace.sessions.map((session) => [session.id, session] as const),
			),
		);
		listedSessionsRef.current = new Set(sessions.keys());
		for (const entry of entriesRef.current.values()) {
			const session = entry.sessionId ? sessions.get(entry.sessionId) : undefined;
			if (entry.sessionId && !session) {
				if (activeRef.current.has(entry.cacheKey)) continue;
				removeEntry(entry.cacheKey);
				continue;
			}
			if (entry.kind === "worker" && session?.cloud && session.mode === "chat") {
				removeEntry(entry.cacheKey);
				continue;
			}
			if (entry.kind === "worker" && session) {
				const sessionGen = session.terminalGeneration ?? "";
				const entryGen = entry.generation ?? "";
				if (session.terminalHandleId !== entry.handleId) {
					removeEntry(entry.cacheKey);
					continue;
				}
				const epochReprovisioned = entryGen !== "" && sessionGen !== "" && entryGen !== sessionGen;
				if (epochReprovisioned) {
					entry.generation = sessionGen;
					useTerminalResetStore.getState().advanceEpoch(entry.sessionId ?? session.id);
					continue;
				}
				if (entryGen === "" && sessionGen !== "") {
					entry.generation = sessionGen;
				}
			}
			if (session && entry.props.session !== session) {
				entry.props = { ...entry.props, session };
				rerender();
			}
		}
	}, [removeEntry, workspaceQuery.data, workspaceQuery.isSuccess]);

	useEffect(() => {
		if (!shellTerminalsQuery.isSuccess) return;
		const shells = new Map(
			(shellTerminalsQuery.data ?? []).map((terminal) => [terminal.handleId, terminal] as const),
		);
		for (const entry of entriesRef.current.values()) {
			if (entry.kind !== "shell") continue;
			const shell = shells.get(entry.handleId);
			if (
				!shell ||
				shell.createdAt !== entry.generation ||
				shell.sessionId !== entry.sessionId
			) {
				removeEntry(entry.cacheKey);
				continue;
			}
			const target = entry.props.terminalTarget;
			if (target?.kind === "shell" && target.title !== shell.title) {
				entry.props = {
					...entry.props,
					terminalTarget: { ...target, title: shell.title },
				};
				rerender();
			}
		}
	}, [removeEntry, shellTerminalsQuery.data, shellTerminalsQuery.isSuccess]);

	useEffect(
		() => () => {
			activeRef.current.clear();
			for (const entry of entriesRef.current.values()) entry.container.remove();
			entriesRef.current.clear();
			muxPool.dispose();
		},
		[muxPool],
	);

	const controller = useMemo<TerminalCacheController>(
		() => ({ activate, deactivate, update, resolveCreateMux }),
		[activate, deactivate, update, resolveCreateMux],
	);

	return (
		<TerminalCacheContext.Provider value={controller}>
			<div
				ref={parkingRef}
				aria-hidden="true"
				className="pointer-events-none invisible fixed top-0 -left-[100000px]"
				data-testid="terminal-cache-parking"
			/>
			{children}
			{[...entriesRef.current.values()].map((entry) => (
				<CachedTerminalPortal
					active={activeRef.current.has(entry.cacheKey)}
					entry={entry}
					key={entry.cacheKey}
					onFatal={markFatal}
					onTerminalReady={markTerminalReady}
				/>
			))}
		</TerminalCacheContext.Provider>
	);
}

function CachedTerminalSlot({
	descriptor,
	props,
}: {
	descriptor: TerminalCacheDescriptor;
	props: TerminalPaneProps;
}) {
	const cache = useContext(TerminalCacheContext);
	const slotRef = useRef<HTMLDivElement | null>(null);

	useLayoutEffect(() => {
		const slot = slotRef.current;
		if (!cache || !slot) return;
		cache.activate(descriptor, props, slot);
		return () => cache.deactivate(descriptor.cacheKey, slot);
	}, [cache, descriptor.cacheKey, descriptor.handleId, descriptor.kind, descriptor.ownerKey, descriptor.sessionId]);

	useLayoutEffect(() => {
		cache?.update(descriptor.cacheKey, props);
	}, [cache, descriptor.cacheKey, props]);

	return <div className="h-full min-h-0 w-full" data-testid="session-terminal-slot" ref={slotRef} />;
}

const BOOT_SPINNER_FRAMES = ["|", "/", "-", "\\"];
const BOOT_STATUS_STEPS = [
	"Preparing the {subject}",
	"Clearing a workspace",
	"Warming up the agent",
	"Untangling worktrees",
	"Sharpening pencils",
	"Waking the {subject}",
	"Almost there",
];

function TerminalBootStatus({ subject, label }: { subject: string; label?: string }) {
	const [tick, setTick] = useState(0);
	useEffect(() => {
		const id = window.setInterval(() => setTick((value) => value + 1), 120);
		return () => window.clearInterval(id);
	}, []);
	const step = Math.min(Math.floor(tick / 15), BOOT_STATUS_STEPS.length - 1);
	return (
		<div className="flex items-center gap-2.5 text-terminal-dim" role="status" aria-live="polite">
			<span aria-hidden="true" className="inline-block w-[1ch] shrink-0 text-center font-mono">{BOOT_SPINNER_FRAMES[tick % BOOT_SPINNER_FRAMES.length]}</span>
			<span>{label ?? BOOT_STATUS_STEPS[step].replace("{subject}", subject)}</span>
		</div>
	);
}

export function TerminalPane({
	session,
	terminalGeneration,
	theme,
	daemonReady,
	terminalTarget: requestedTerminalTarget,
	fontSize,
	onChangeFontSize,
	isFullscreen,
	onToggleFullscreen,
	inputDisabled,
	booting,
	switchingLabel,
	focusRequested,
	onTerminalStateChange,
	onTerminalContentReadyChange,
	inputRequest,
	onInputRequestResult,
	createMux,
}: TerminalPaneProps) {
	const { t } = useTranslation();
	const terminalTarget =
		requestedTerminalTarget &&
		terminalTargetBelongsToSession(requestedTerminalTarget, session?.id)
			? requestedTerminalTarget
			: ({ kind: "worker" } satisfies TerminalTarget);
	const isOptimisticShell =
		terminalTarget.kind === "shell" && terminalTarget.handleId.startsWith("pending-shell:");
	const cache = useContext(TerminalCacheContext);
	useTerminalResetStore((state) => (session?.id ? (state.nonces[session.id] ?? 0) : 0));
	const terminalKey =
		terminalTarget?.kind === "reviewer" || terminalTarget?.kind === "shell"
			? terminalTarget.handleId
			: `${session?.terminalHandleId ?? "empty"}:${session?.terminalGeneration ?? ""}`;

	if (!window.ao) {
		if (terminalTarget?.kind === "shell") {
			return (
				<pre
					className="h-full overflow-auto bg-terminal p-4 font-mono leading-relaxed text-terminal"
					style={{ fontSize }}
				>
					<span className="text-terminal-dim">{terminalTarget.title}</span> $ {"\n"}
					<span className="text-terminal-dim">
						{"(standalone shell — a live PTY here in the desktop app)"}
						{"\n"}
					</span>
				</pre>
			);
		}
		const provider = terminalTarget?.kind === "reviewer" ? terminalTarget.harness : (session?.provider ?? "claude");
		const lines =
			terminalTarget?.kind === "reviewer" ? reviewerPreviewLines(session) : workerPreviewLines(session, provider);
		return (
			<pre
				className="h-full overflow-auto bg-terminal p-4 font-mono leading-relaxed text-terminal-foreground"
				data-testid="session-terminal"
				style={{ fontSize }}
			>
				<span className="text-terminal-dim">~/{session?.workspaceName ?? "reverbcode"}</span>{" "}
				{session?.branch ? <span className="text-accent">{session.branch}</span> : null}
				{session?.branch ? " " : ""}$ {provider}
				{"\n"}
				{lines.map((line, index) => (
					<span
						key={`${line}:${index}`}
						className={
							line.startsWith("PASS") || line.startsWith("DONE")
								? "text-success"
								: line.startsWith("WARN") || line.startsWith("TODO")
									? "text-warning"
									: line.startsWith("$") || line.startsWith("▲")
										? "text-accent"
										: "text-terminal-foreground"
						}
					>
						{line}
						{"\n"}
					</span>
				))}
			</pre>
		);
	}
	if (isOptimisticShell) {
		return (
			<div
				aria-label={t("terminal.shellAria")}
				className="terminal-surface h-full"
				data-testid="optimistic-terminal"
			/>
		);
	}

	const props = {
		session,
		theme,
		daemonReady,
		terminalTarget,
		fontSize,
		onChangeFontSize,
		isFullscreen,
		onToggleFullscreen,
		inputDisabled,
		booting,
		switchingLabel,
		focusRequested,
		onTerminalStateChange,
		onTerminalContentReadyChange,
		inputRequest,
		onInputRequestResult,
		createMux,
	};
	const descriptor = cacheDescriptor(session, terminalTarget, terminalGeneration);
	if (cache && descriptor && !createMux) {
		return <CachedTerminalSlot descriptor={descriptor} props={props} />;
	}
	const attachedCreateMux = createMux ?? (cache && session?.cloud && terminalTarget.kind === "reviewer"
		? cache.resolveCreateMux(session, terminalTarget)
		: undefined);

	return (
		<AttachedTerminal
			key={terminalKey}
			session={session}
			theme={theme}
			daemonReady={daemonReady}
			fontSize={fontSize}
			isFullscreen={isFullscreen}
			inputDisabled={inputDisabled}
			booting={booting}
			switchingLabel={switchingLabel}
			onChangeFontSize={onChangeFontSize}
			onToggleFullscreen={onToggleFullscreen}
			focusRequested={focusRequested}
			onTerminalStateChange={onTerminalStateChange}
			onTerminalContentReadyChange={onTerminalContentReadyChange}
			inputRequest={inputRequest}
			onInputRequestResult={onInputRequestResult}
			createMux={attachedCreateMux}
			terminalTarget={terminalTarget}
		/>
	);
}

function workerPreviewLines(session: WorkspaceSession | undefined, provider: string): string[] {
	if (session?.id === "ao-demo-orchestrator") {
		return [
			"> Go through my Linear backlog and let's plan which tasks to spawn off",
			"",
			"Ran 3 shell commands",
			"",
			"Here's the backlog triage. Half of it is already in flight — don't spawn those.",
			"",
			"Already covered — don't spawn (session → PR):",
			"— terminal polish → PR #318, changes requested",
			"— browser preview stack → PRs #319/#320, in review",
			"— README screenshot assets → PR #323, approved and mergeable",
			"",
			"Plan: 3 sessions worth spawning",
			"",
			"┌───┬────────────────────┬──────────────────────────────────────────┬──────────────────────────────────┐",
			"│ # │ Session            │ Scope                                    │ Why now                          │",
			"├───┼────────────────────┼──────────────────────────────────────────┼──────────────────────────────────┤",
			"│ 1 │ new-task-flake     │ NewTaskDialog smoke test flakes on Enter │ Failing PR #324's e2e; small fix │",
			"│ 2 │ checkout-retries   │ e2e retries leak state between runs      │ Flakes 1-in-5 on CI; well scoped │",
			"│ 3 │ session-pr-surface │ PR checks missing on board cards         │ Additive; touches board only     │",
			"└───┴────────────────────┴──────────────────────────────────────────┴──────────────────────────────────┘",
			"",
			"Want me to spawn all three? I'd put #1–2 on codex and #3 on claude-code.",
			"",
			"> yes, spawn all three",
			"",
			"Running 3 shell commands…",
			'└ $ ao spawn --project ao-demo --name "new-task-flake" --agent codex --prompt',
			'  "Fix the flaky NewTaskDialog smoke test: submit is debounced 300ms while the',
			'  e2e check asserts synchronously. Reproduce, fix, and push to update PR #324."',
			"PASS 3 sessions spawned — board updated",
		];
	}
	if (session?.id === "demo-review-stack") {
		return [
			'$ rg "previewUrl|Browser" frontend/src/renderer',
			"frontend/src/renderer/components/SessionInspector.tsx: Browser tab selected after ao preview",
			"frontend/src/renderer/hooks/useBrowserView.ts: preview revision re-navigates the view",
			"$ ao preview http://localhost:5173",
			"DONE preview target set for demo-review-stack",
			"$ npm --prefix frontend run typecheck",
			"PASS TypeScript project references are clean",
			"TODO wait for reviewer on PR #320 before merging the stack",
		];
	}
	if (session?.id === "demo-working") {
		return [
			`$ ${provider} --continue`,
			"Reading renderer board and inspector components...",
			"Updated demo workspace data for README screenshots",
			"$ npm --prefix frontend test -- SessionsBoard SessionInspector",
			"PASS 18 tests passed",
			"DONE board has Working, Needs you, In review, and Ready to merge populated",
		];
	}
	if (session?.id === "demo-needs-input") {
		return [
			"$ git diff --stat",
			"frontend/src/renderer/components/TerminalPane.tsx | 41 +++++++++++++++++",
			"frontend/src/renderer/styles.css                 | 27 +++++++++++",
			"WARN reviewer requested a tighter terminal activity sample",
			"TODO confirm whether to keep the toolbar density change",
		];
	}
	if (session?.id === "demo-ci-failed") {
		return [
			"╭────────────────────────────────────────────╮",
			"│ >_ OpenAI Codex (v0.133.0)                 │",
			"│ model:        gpt-5.5 high  /model to change",
			"│ directory:    ~/ao-demo/demo-new-task-flake",
			"│ permissions:  YOLO mode                    │",
			"╰────────────────────────────────────────────╯",
			"",
			"• I'll start from the failing check output, reproduce the Enter-submit",
			"  path locally, then patch and push.",
			"",
			"• Ran npm test -- NewTaskDialog",
			"└ PASS 12 tests passed",
			"",
			"▲ ao send · CI failed on PR #324. The failing checks are e2e (NewTaskDialog",
			"  submits with Enter). Investigate and push a fix.",
			"",
			'• Ran rg -n "onKeyDown|Enter" src/components/NewTaskDialog.tsx',
			'└ src/components/NewTaskDialog.tsx:88:  onKeyDown={(e) => e.key === "Enter" && scheduleSubmit()}',
			"  src/components/NewTaskDialog.tsx:141: const scheduleSubmit = debounce(submit, 300)",
			"  … +42 lines (ctrl + t to view transcript)",
			"",
			"Found it: submit is debounced 300ms, the check asserts immediately.",
			"Patching the handler and re-running e2e…",
		];
	}
	return [
		`$ ${provider} --status`,
		"Reading task context and local diff...",
		"Running focused validation for the current session",
		"PASS demo terminal is populated for screenshots",
	];
}

function reviewerPreviewLines(session: WorkspaceSession | undefined): string[] {
	return [
		"$ ao review submit --session " + (session?.id ?? "demo-session"),
		"Reviewing PR #319: browser preview rail renders inside AO",
		"PASS implementation matches the requested README screenshot flow",
		"Reviewing PR #320: stacked PR review rows",
		"WARN keep multiple review rows visible before taking the screenshot",
		"DONE submitted batched review results",
	];
}

const KEYBOARD_SCROLL_PROVIDERS = new Set(["opencode", "opencode-v2", "kilocode", "grok", "mimo-code"]);

export function providerScrollsByKeyboard(provider?: string): boolean {
	return provider ? KEYBOARD_SCROLL_PROVIDERS.has(provider) : false;
}

function bannerText(
	state: TerminalSessionState,
	t: TFunction,
	hasAttached: boolean,
	isCloud: boolean,
	error?: string,
): string | undefined {
	if (state === "reattaching") {
		return isCloud && !hasAttached ? undefined : t("terminal.reattaching");
	}
	if (state === "error") return t("terminal.error", { error: error ?? t("terminal.connectionFailed") });
	return undefined;
}

function AttachedTerminal({
	session,
	theme,
	daemonReady,
	terminalTarget,
	fontSize,
	onChangeFontSize,
	isFullscreen,
	onToggleFullscreen,
	inputDisabled,
	booting,
	switchingLabel,
	focusRequested,
	onTerminalStateChange,
	onTerminalContentReadyChange,
	inputRequest,
	onInputRequestResult,
	createMux,
	isVisible = true,
	onFatal,
	onTerminalReady,
}: TerminalPaneProps & {
	isVisible?: boolean;
	onFatal?: (message: string) => void;
	onTerminalReady?: (terminal: AttachableTerminal) => void;
}) {
	const { t } = useTranslation();
	const { baseUrl: cloudBaseUrl } = useCloudCp();
	const cloudBaseUrlRef = useRef(cloudBaseUrl);
	cloudBaseUrlRef.current = cloudBaseUrl;
	const cloudSessionId = session?.cloud ? session.id : undefined;
	const cloudOrgId = session?.cloud?.orgId;
	const cloudAttachmentMaxBytes = useCloudOffering(Boolean(cloudSessionId), cloudOrgId).maxAttachmentBytes;
	const cloudDropsAccepted = cloudAttachmentMaxBytes !== undefined;
	const saveCloudDrop = useMemo(() => {
		if (!cloudSessionId || !cloudOrgId) return undefined;
		if (!cloudDropsAccepted) return null;
		return async (file: { name: string; bytes: Uint8Array }) => {
			const baseUrl = cloudBaseUrlRef.current;
			const attachment = await uploadCloudAttachment(baseUrl, cloudOrgId, cloudSessionId, file);
			return placeCloudAttachment(baseUrl, cloudOrgId, cloudSessionId, attachment.id);
		};
	}, [cloudDropsAccepted, cloudOrgId, cloudSessionId]);
	const attachSession =
		terminalTarget?.kind === "shell"
			? undefined
			: session && terminalTarget?.kind === "reviewer"
			? { ...session, terminalHandleId: terminalTarget.handleId }
			: session;
	const [terminal, setTerminal] = useState<AttachableTerminal | null>(null);
	const [hasVisibleContent, setHasVisibleContent] = useState(false);
	const lastInputRequestIdRef = useRef<number | null>(null);
	const [initFailed, setInitFailed] = useState(false);
	const [isRestoring, setIsRestoring] = useState(false);
	const [restoreError, setRestoreError] = useState<string | undefined>();
	const [restoreUnavailable, setRestoreUnavailable] = useState(false);
	const queryClient = useQueryClient();
	const restoreSessionById = useRestoreSession();
	const shellTerminalHandleId = terminalTarget?.kind === "shell" ? terminalTarget.handleId : undefined;
	const { attach, state, error, replaySettled, hasAttached, syncVisibleSize } = useTerminalSession(attachSession, {
		coverInitialReplay: terminalTarget?.kind !== "reviewer",
		waitForInitialOutput: Boolean(attachSession?.cloud),
		createMux,
		daemonReady,
		exitNotice:
			terminalTarget?.kind === "reviewer"
				? "\r\n\x1b[2m[reviewer terminal finished]\x1b[0m"
				: terminalTarget?.kind === "shell"
					? undefined
					: "",
		inputDisabled,
		isVisible,
		shellTerminalHandleId,
	});
	useEffect(() => {
		onTerminalStateChange?.(state);
	}, [onTerminalStateChange, state]);
	const cloudReviewExitAwaitingStatus = Boolean(
		state === "exited" &&
			session?.cloud &&
			terminalTarget?.kind === "reviewer" &&
			(!terminalTarget.reviewStatus ||
				terminalTarget.reviewStatus === "running" ||
				terminalTarget.reviewStatus === "complete"),
	);
	useEffect(() => {
		if (!cloudReviewExitAwaitingStatus) return;
		void queryClient.invalidateQueries({ queryKey: ["cloud-session-reviews"] });
	}, [cloudReviewExitAwaitingStatus, queryClient]);
	const isReconnecting = useTerminalResetStore((store) =>
		session?.id ? Boolean(store.reconnecting[session.id]) : false,
	);
	const currentEpoch = session?.terminalGeneration ? Number(session.terminalGeneration) || 0 : 0;
	const baselineEpoch = useTerminalResetStore((store) =>
		session?.id ? (store.baselineEpoch[session.id] ?? 0) : 0,
	);
	useEffect(() => {
		if (isReconnecting && session?.id && currentEpoch > baselineEpoch) {
			useTerminalResetStore.getState().markConnected(session.id);
		}
	}, [isReconnecting, currentEpoch, baselineEpoch, session?.id]);
	useEffect(() => {
		if (!terminal || state !== "attached" || !inputRequest) return;
		if (lastInputRequestIdRef.current === inputRequest.id) return;
		const accepted = terminal.sendUserInput(inputRequest.data);
		if (accepted) lastInputRequestIdRef.current = inputRequest.id;
		onInputRequestResult?.(inputRequest.id, accepted);
	}, [inputRequest, onInputRequestResult, state, terminal]);
	const [replayPaintPending, setReplayPaintPending] = useState(!replaySettled);
	useLayoutEffect(() => {
		if (!replaySettled) {
			setReplayPaintPending(true);
			return;
		}
		if (!replayPaintPending || !terminal) return;
		let current = true;
		void terminal.prepareForActivation().then(() => {
			if (current) setReplayPaintPending(false);
		});
		return () => {
			current = false;
		};
	}, [replayPaintPending, replaySettled, terminal]);
	useEffect(() => {
		if (!attachSession?.cloud) return;
		onTerminalContentReadyChange?.(
			(hasAttached && replaySettled && !replayPaintPending && hasVisibleContent)
			|| state === "error" || state === "exited" || initFailed,
		);
	}, [attachSession?.cloud, hasAttached, hasVisibleContent, initFailed, onTerminalContentReadyChange, replayPaintPending, replaySettled, state]);
	const handleId = shellTerminalHandleId ?? attachSession?.terminalHandleId;
	const handleRetry = useCallback(() => {
		if (terminal) attach(terminal);
	}, [attach, terminal]);
	const provider =
		terminalTarget?.kind === "reviewer"
			? terminalTarget.harness
			: terminalTarget?.kind === "shell"
				? undefined
				: session?.provider;
	const isSessionActive = session ? sessionIsActive(session) : false;
	const sharedCloudSession = session?.cloud?.shared === true;
	const canRestoreSession =
		terminalTarget?.kind !== "reviewer" &&
		terminalTarget?.kind !== "shell" &&
		session !== undefined &&
		!isSessionActive &&
		!sharedCloudSession;

	const handleReady = useCallback((handle: AttachableTerminal) => {
		setTerminal(handle);
	}, []);
	useLayoutEffect(() => {
		if (terminal) onTerminalReady?.(terminal);
	}, [onTerminalReady, terminal]);
	const handleInitError = useCallback((err: unknown) => {
		console.error("xterm failed to initialize", err);
		void captureRendererException(err, { source: "terminal-init", operation: "xterm_init" });
		setInitFailed(true);
	}, []);
	useEffect(() => {
		if (initFailed) {
			onFatal?.("renderer initialization failed");
			onTerminalStateChange?.("error");
			return;
		}
	}, [initFailed, onFatal, onTerminalStateChange]);
	const handleLinkOpen = useSessionBrowserLink(session);
	const handleSessionLinkOpen = useSessionLinkNavigation(session?.hostId);
	const restoreSession = useCallback(async () => {
		if (!session?.id || !canRestoreSession || isRestoring) return;
		setIsRestoring(true);
		setRestoreError(undefined);
		try {
			const result = await restoreSessionById(session.id, session.hostId);
			if (result.status === "not_resumable") {
				setRestoreUnavailable(true);
				return;
			}
			if (result.status === "error") {
				setRestoreError(result.message);
			}
		} catch (err) {
			setRestoreError(err instanceof Error ? err.message : t("terminal.unableRestore"));
		} finally {
			setIsRestoring(false);
		}
	}, [canRestoreSession, isRestoring, restoreSessionById, session?.hostId, session?.id, t]);

	useEffect(() => {
		if (!terminal) return;
		let current = true;
		let detach: (() => void) | undefined;
		void terminal.prepareForActivation().then(() => {
			if (!current) return;
			detach = attach(terminal);
		});
		return () => {
			current = false;
			detach?.();
		};
	}, [terminal, handleId, attach, attachSession?.id]);

	if (initFailed) {
		return (
			<div className="terminal-surface grid h-full place-items-center p-4 font-mono text-xs text-muted-foreground">
				{t("terminal.initFailed")}
			</div>
		);
	}

	const isCloudConnectError =
		state === "error" &&
		Boolean(attachSession?.cloud) &&
		!hasAttached &&
		Boolean(error?.includes("WebSocket cannot connect"));
	const banner = isCloudConnectError
		? undefined
		: bannerText(state, t, hasAttached, Boolean(attachSession?.cloud), error);
	const showEmptyState = !handleId;
	const showReplayCover =
		Boolean(handleId) &&
		(!replaySettled || replayPaintPending) &&
		(state === "connecting" || state === "attached");
	const isBoxComingUp = Boolean(session?.cloud) && isReconnecting;
	const cloudTerminalIdentity = `${handleId ?? ""}`;
	const cloudRevealedIdentityRef = useRef(cloudTerminalIdentity);
	const cloudRevealedRef = useRef(false);
	if (cloudRevealedIdentityRef.current !== cloudTerminalIdentity) {
		cloudRevealedIdentityRef.current = cloudTerminalIdentity;
		cloudRevealedRef.current = false;
	}
	if (hasAttached && replaySettled && !replayPaintPending && hasVisibleContent) cloudRevealedRef.current = true;
	const showEndedStatePreview =
		state === "exited" || canRestoreSession || (terminalTarget?.kind === "worker" && sessionAgentExited(session));
	const isCloudConnecting =
		Boolean(attachSession?.cloud) &&
		!isCloudConnectError &&
		!showEmptyState &&
		!showEndedStatePreview &&
		!cloudRevealedRef.current;
	const switchingHeldRef = useRef<string | undefined>(undefined);
	if (switchingLabel && !hasVisibleContent) switchingHeldRef.current = switchingLabel;
	if (hasVisibleContent || state === "exited" || state === "error") switchingHeldRef.current = undefined;
	const coverLabel = switchingLabel ?? switchingHeldRef.current;
	const switchingCover = Boolean(coverLabel) && !hasVisibleContent && state !== "exited" && state !== "error";
	const cloudEnded = Boolean(session?.cloud) && terminalTarget?.kind !== "reviewer" && terminalTarget?.kind !== "shell" &&
		(sessionAgentExited(session) || (!isSessionActive && !sharedCloudSession));
	const showEndedState = showEndedStatePreview && (!session?.cloud || cloudEnded) && !isBoxComingUp && !booting && !coverLabel;

	return (
		<div className="terminal-surface flex h-full min-h-0 flex-col" data-testid="session-terminal">
			{showEndedState && (
				<TerminalEndedStrip
					canRestore={canRestoreSession}
					error={restoreError}
					isRestoring={isRestoring}
					onRestore={restoreSession}
					session={session}
					variant={
						terminalTarget?.kind === "reviewer" ? "reviewer" : terminalTarget?.kind === "shell" ? "shell" : "session"
					}
					reviewStatus={terminalTarget?.kind === "reviewer" ? terminalTarget.reviewStatus : undefined}
				/>
			)}
			<div className="relative min-h-0 flex-1 pl-2">
				<LazyChunkBoundary>
				<Suspense fallback={null}>
				<XtermTerminal
					ariaLabel={terminalTarget?.kind === "shell" ? t("terminal.shellAria") : t("terminal.sessionAria")}
					saveDroppedFile={saveCloudDrop}
					droppedFileMaxBytes={saveCloudDrop && cloudAttachmentMaxBytes !== undefined ? Math.min(cloudAttachmentMaxBytes, MAX_CLOUD_CP_BINARY_BYTES) : undefined}
					fontSize={fontSize}
					focusRequested={focusRequested}
					isFullscreen={isFullscreen}
					isVisible={isVisible}
					onChangeFontSize={onChangeFontSize}
					onError={handleInitError}
					onLinkOpen={handleLinkOpen}
					onSessionLinkOpen={handleSessionLinkOpen}
					onReady={handleReady}
					onVisibleContent={() => setHasVisibleContent(true)}
					onToggleFullscreen={onToggleFullscreen}
					onVisibleSize={syncVisibleSize}
					onGridChange={session ? rememberPaneTerminalGrid : undefined}
					paneScrollsByKeyboard={providerScrollsByKeyboard(provider)}
					supportsCursorColorScheme={provider === "cursor"}
					theme={theme}
				/>
				</Suspense>
				</LazyChunkBoundary>
				{(showEmptyState || booting || switchingCover) && (
					<div className="terminal-surface absolute inset-0 grid place-items-center font-mono text-control">
						{session ? (
							<TerminalBootStatus subject={session.kind === "orchestrator" ? "orchestrator" : "agent"} label={coverLabel} />
						) : (
							<div className="text-center">
								<div className="text-terminal">{"Orchestrator"}</div>
								<div className="mt-2 text-terminal-dim">{t("terminal.noSessionSelected")}</div>
							</div>
						)}
					</div>
				)}
				{isCloudConnecting && (
					<div
						className="terminal-surface absolute inset-0 grid place-items-center font-mono text-control"
						data-testid="terminal-connecting-cover"
					>
						<div className="text-terminal">{t("terminal.connecting")}</div>
					</div>
				)}
				{showReplayCover && !isCloudConnecting && (
					<ReplayCover message={attachSession?.cloud ? t("terminal.connecting") : undefined} />
				)}
				{isCloudConnectError && <CloudConnectError onRetry={handleRetry} />}
				{banner && !showEndedState && (
					<div className="absolute inset-x-3 top-2 rounded-md border border-border bg-surface/95 px-3 py-1.5 font-mono text-caption text-muted-foreground">
						{banner}
					</div>
				)}
			</div>
			{session && (
				<RestoreUnavailableDialog
					open={restoreUnavailable}
					session={session}
					onOpenChange={setRestoreUnavailable}
					onRecreated={async () => {
						await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(session.hostId) });
					}}
				/>
			)}
		</div>
	);
}

function CloudConnectError({ onRetry }: { onRetry: () => void }) {
	const { t } = useTranslation();
	return (
		<div className="absolute inset-0 z-10 grid place-items-center bg-terminal-opaque pointer-events-auto">
			<div className="flex max-w-sm flex-col items-center gap-4 rounded-lg border border-border bg-surface p-6 text-center shadow-lg">
				<div className="font-mono text-sm font-medium text-foreground">{t("terminal.cloudConnectTitle")}</div>
				<div className="text-xs leading-relaxed text-muted-foreground">{t("terminal.cloudConnectDescription")}</div>
				<Button
					type="button"
					id="cloud-terminal-retry"
					className="font-mono"
					onClick={onRetry}
					size="sm"
					variant="secondary"
				>
					<RotateCcw className="size-3" aria-hidden="true" />
					{t("terminal.cloudConnectRetry")}
				</Button>
			</div>
		</div>
	);
}

function ReplayCover({ message }: { message?: string }) {
	return (
		<div
			aria-hidden={message ? undefined : "true"}
			className="bg-terminal-opaque pointer-events-none absolute inset-0 grid place-items-center"
			data-testid="terminal-replay-cover"
		>
			{message ? <span className="font-mono text-sm text-terminal-dim">{message}</span> : null}
		</div>
	);
}

type TerminalEndedStripProps = {
	canRestore: boolean;
	error?: string;
	isRestoring: boolean;
	onRestore: () => void;
	reviewStatus?: "running" | "complete" | "delivered" | "failed" | "cancelled";
	session?: WorkspaceSession;
	variant: "reviewer" | "session" | "shell";
};

function TerminalEndedStrip({ canRestore, error, isRestoring, onRestore, reviewStatus, session, variant }: TerminalEndedStripProps) {
	const { t } = useTranslation();
	const reviewDelivered = variant === "reviewer" && reviewStatus === "delivered";
	const terminated = variant === "session" && session && !sessionIsActive(session);
	const agentExited = variant === "session" && sessionAgentExited(session);
	const startupFailed = variant === "session" && session?.provisionState === "failed";
	const heading = reviewDelivered
		? t("terminal.reviewCompleted")
		: startupFailed
			? t("terminal.startupFailed")
			: terminated
				? t("terminal.sessionTerminated")
				: agentExited
					? t("terminal.agentExited")
					: t("terminal.ended");
	const message = reviewDelivered
		? t("terminal.reviewerCompleted")
		: canRestore
		? t("terminal.restoreToContinue")
		: variant === "reviewer"
			? t("terminal.reviewerEnded")
			: variant === "shell"
				? t("terminal.shellExited")
				: t("terminal.sessionEndedNotTerminated");

	return (
		<div className="shrink-0 border-b border-border bg-surface/80 px-4 py-2">
			<div className="flex min-h-control-board flex-wrap items-center gap-3">
				<div className="min-w-0">
					<div
						className={cn(
							"font-mono text-caption font-medium uppercase tracking-wide-md",
							terminated || startupFailed ? "text-destructive" : "text-muted-foreground",
						)}
					>
						{heading}
					</div>
					<div className="mt-0.5 text-xs text-muted-foreground">
						{startupFailed && session?.provisionError ? session.provisionError : agentExited ? t("terminal.agentExitedHelp") : message}
					</div>
				</div>
				{error && <div className="max-w-content-max truncate text-xs text-destructive">{error}</div>}
				{variant === "session" && session ? <ResumeAgentControl session={session} /> : null}
				{canRestore && (
					<Tooltip>
						<TooltipTrigger asChild>
							<span className="inline-flex">
								<Button
									type="button"
									aria-label={t("terminal.restoreSession")}
									disabled={isRestoring}
									onClick={onRestore}
									variant="secondary"
								>
									<RotateCcw className={cn("size-icon-base", isRestoring && "animate-spin")} aria-hidden="true" />
									{t("terminal.restoreSession")}
								</Button>
							</span>
						</TooltipTrigger>
						<TooltipContent side="bottom">{t("terminal.restoreSession")}</TooltipContent>
					</Tooltip>
				)}
			</div>
		</div>
	);
}
