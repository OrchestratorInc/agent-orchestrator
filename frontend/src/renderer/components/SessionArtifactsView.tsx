import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { ChevronLeft, FolderOpen, Maximize2, Minimize2, SquareTerminal } from "lucide-react";
import { isAgentActivityWorking } from "@aoagents/product-ui";
import type { OpenArtifactsTargetId } from "../../shared/editor-handoff";
import { useOpenSessionArtifacts } from "../hooks/useEditorHandoff";
import { useRestoreArtifactVersion } from "../hooks/useArtifactVersions";
import { useOpenArtifactPreview } from "../hooks/useOpenArtifactPreview";
import { sessionArtifactFileQueryOptions } from "../hooks/useSessionWorkspaceFiles";
import { isMacPlatform, isWindowsPlatform } from "../lib/platform";
import { artifactKey, type ArtifactRow } from "../lib/artifacts";
import { sessionUiKey } from "../lib/hosts";
import { cn } from "../lib/utils";
import { useArtifactsViewStore } from "../stores/artifacts-view-store";
import type { SessionArtifact, WorkspaceSession } from "../types/workspace";
import { ArtifactActionsMenu } from "./ArtifactActionsMenu";
import { ArtifactGalleryCard } from "./ArtifactGalleryCard";
import { ArtifactPreviewModal } from "./ArtifactPreviewModal";
import { ArtifactFileView } from "./ArtifactFileView";
import { ArtifactHistoryMenu, ArtifactVersionPreview } from "./ArtifactHistory";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { PanelMessage } from "./WorkspaceDiffView";

export type ArtifactOpenRequest = { feedback?: boolean; key: number; path: string };

// How long a failed open or copy stays in the header.
const ERROR_CLEAR_MS = 5_000;

/** The session's artifacts, newest first. */
function sortedArtifacts(artifacts: SessionArtifact[]): SessionArtifact[] {
	return [...artifacts].sort(
		(a, b) => (Date.parse(b.updatedAt) || 0) - (Date.parse(a.updatedAt) || 0) || a.path.localeCompare(b.path),
	);
}

function fileManagerName(): string {
	return isMacPlatform() ? "Finder" : isWindowsPlatform() ? "File Explorer" : "File Manager";
}

function terminalName(): string {
	return isWindowsPlatform() ? "Command Prompt" : "Terminal";
}

/** Opens the session's artifact directory in the file manager or a terminal. */
function OpenArtifactsFolderButtons({ sessionId }: { sessionId: string }) {
	const { t } = useTranslation();
	const open = useOpenSessionArtifacts();
	const resetOpen = open.reset;
	useEffect(() => {
		if (!open.error) return;
		const timer = setTimeout(resetOpen, ERROR_CLEAR_MS);
		return () => clearTimeout(timer);
	}, [open.error, resetOpen]);
	const targets: { id: OpenArtifactsTargetId; label: string; icon: typeof FolderOpen }[] = [
		{ id: "file-manager", label: t("editor.openInTarget", { name: fileManagerName() }), icon: FolderOpen },
		{ id: "terminal", label: t("editor.openInTarget", { name: terminalName() }), icon: SquareTerminal },
	];
	return (
		<>
			{open.error ? (
				<span className="min-w-0 truncate text-caption text-destructive" role="alert" title={open.error.message}>
					{open.error.message}
				</span>
			) : null}
			{targets.map(({ id, label, icon: Icon }) => (
				<Tooltip key={id}>
					<TooltipTrigger asChild>
						<Button
							aria-label={label}
							disabled={open.isPending}
							onClick={() => open.mutate({ sessionId, targetId: id })}
							size="icon-sm"
							type="button"
							variant="ghost"
						>
							<Icon aria-hidden="true" className="size-icon-sm" />
						</Button>
					</TooltipTrigger>
					<TooltipContent side="bottom">{label}</TooltipContent>
				</Tooltip>
			))}
		</>
	);
}

// Full screen covers the whole app window. A wide markdown body keeps
// GitHub's reading width, centred on the app's own background; the `!`
// overrides github-markdown-css, which sets both outside Tailwind's layers.
const FULLSCREEN_FRAME_CLASS =
	"fixed inset-0 z-overlay flex flex-col bg-background outline-none [&_.markdown-body]:mx-auto! [&_.markdown-body]:max-w-[980px] [&_.markdown-body]:bg-transparent!";
// The window's macOS traffic lights sit over the overlay's header unless the
// window itself is full screen.
const FULLSCREEN_MAC_HEADER_CLASS = "pl-(--size-titlebar-cluster-left) [html:has(.native-fullscreen)_&]:pl-1";
const INLINE_FRAME_CLASS = "flex h-full min-h-0 flex-col outline-none";

/**
 * The open artifact's viewer, in the inspector or full screen. It renders
 * into one node that moves between its inspector slot and a window overlay,
 * so an edit in progress survives the switch. Escape leaves full screen.
 */
function ArtifactViewerFrame({
	children,
	fullscreen,
	label,
	onExitFullscreen,
}: {
	children: ReactNode;
	fullscreen: boolean;
	label: string;
	onExitFullscreen: () => void;
}) {
	const [frame] = useState(() => {
		const element = document.createElement("div");
		element.tabIndex = -1;
		return element;
	});
	const slotRef = useRef<HTMLDivElement>(null);
	const switchedRef = useRef(false);
	useLayoutEffect(() => () => frame.remove(), [frame]);
	useLayoutEffect(() => {
		frame.className = fullscreen ? FULLSCREEN_FRAME_CLASS : INLINE_FRAME_CLASS;
		if (fullscreen) {
			// A dialog role also hides the native Browser view and gives the
			// overlay solid surfaces in a translucent window.
			frame.setAttribute("role", "dialog");
			frame.setAttribute("aria-modal", "true");
			frame.setAttribute("aria-label", label);
			frame.dataset.state = "open";
			document.body.append(frame);
		} else {
			for (const name of ["role", "aria-modal", "aria-label", "data-state"]) frame.removeAttribute(name);
			slotRef.current?.append(frame);
		}
		// Moving the node drops focus; after a switch it lands on the frame.
		if (switchedRef.current || fullscreen) frame.focus({ preventScroll: true });
		switchedRef.current = true;
	}, [frame, fullscreen, label]);
	useEffect(() => {
		if (!fullscreen) return;
		// Escape from a menu or dialog opened over the artifact is theirs.
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key !== "Escape" || event.defaultPrevented || !frame.contains(event.target as Node)) return;
			event.preventDefault();
			onExitFullscreen();
		};
		document.addEventListener("keydown", onKeyDown);
		return () => document.removeEventListener("keydown", onKeyDown);
	}, [frame, fullscreen, onExitFullscreen]);
	return (
		<div className="h-full min-h-0" ref={slotRef} role="tabpanel">
			{createPortal(children, frame)}
		</div>
	);
}

/**
 * The open artifact's content: the live file, or a saved version in its place
 * while a saved version is being previewed. The live file stays mounted underneath, so
 * an edit in progress survives a look at an earlier version.
 */
function ArtifactViewerBody({
	artifact,
	feedbackRequestKey,
	hostId,
	onFeedbackRequestConsumed,
	onPreviewVersion,
	previewVersion,
	readOnly,
	sessionId,
	toolbarHost,
}: {
	artifact: SessionArtifact;
	feedbackRequestKey?: number;
	hostId?: string;
	onFeedbackRequestConsumed?: (key: number) => void;
	onPreviewVersion: (version: number | null) => void;
	previewVersion: number | null;
	readOnly: boolean;
	sessionId: string;
	toolbarHost: HTMLElement | null;
}) {
	const restore = useRestoreArtifactVersion({ hostId, path: artifact.path, rawUrl: artifact.rawUrl, sessionId });
	const previewing = previewVersion !== null;
	// An error from one version's restore does not belong to the next one looked at.
	const resetRestore = restore.reset;
	useEffect(() => resetRestore(), [previewVersion, resetRestore]);
	const artifactOrigin = useMemo(() => {
		try {
			return artifact.rawUrl ? new URL(artifact.rawUrl).origin : undefined;
		} catch {
			return undefined;
		}
	}, [artifact.rawUrl]);
	return (
		<div className="flex h-full min-h-0">
			<div className="relative min-h-0 min-w-0 flex-1">
				<div className={cn("h-full", previewing && "hidden")}>
					<ArtifactFileView
						artifactName={artifact.name}
						feedbackRequestKey={feedbackRequestKey}
						hostId={hostId}
						onFeedbackRequestConsumed={onFeedbackRequestConsumed}
						path={artifact.path}
						rawUrl={artifact.rawUrl}
						readOnly={readOnly}
						sessionId={sessionId}
						toolbarHost={previewing ? null : toolbarHost}
					/>
				</div>
				{previewing ? (
					<ArtifactVersionPreview
						artifactOrigin={artifactOrigin}
						hostId={hostId}
						onBack={() => onPreviewVersion(null)}
						onRestore={() => {
							if (previewVersion !== null) restore.mutate({ version: previewVersion }, { onSuccess: () => onPreviewVersion(null) });
						}}
						path={artifact.path}
						restoreError={restore.isError ? restore.error.message : undefined}
						restoring={restore.isPending}
						sessionId={sessionId}
						version={previewVersion}
					/>
				) : null}
			</div>
		</div>
	);
}

/**
 * The inspector's Artifacts view (its own inspector tab): every file in
 * the session's artifact directory. HTML artifacts open in the Browser panel;
 * markdown and other files open in a viewer in place of the list, which can
 * go full screen.
 */
export function SessionArtifactsView({
	hostId,
	onRequestConsumed,
	request,
	session,
}: {
	hostId?: string;
	onRequestConsumed?: (key: number) => void;
	request?: ArtifactOpenRequest | null;
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	const artifacts = useMemo(() => sortedArtifacts(session.artifactFiles ?? []), [session.artifactFiles]);
	const [selectedPath, setSelectedPath] = useState<string | null>(null);
	const [previewPath, setPreviewPath] = useState<string | null>(null);
	const pinnedArtifactIds = useArtifactsViewStore((state) => state.pinnedArtifactIds);
	const togglePinnedArtifact = useArtifactsViewStore((state) => state.togglePinnedArtifact);
	const [toolbarHost, setToolbarHost] = useState<HTMLElement | null>(null);
	const [fullscreen, setFullscreen] = useState(false);
	const [previewVersion, setPreviewVersion] = useState<number | null>(null);
	const openArtifactPreview = useOpenArtifactPreview(session.id, hostId);
	const uiKey = sessionUiKey(session.id, hostId);

	useEffect(() => { setSelectedPath(null); setPreviewPath(null); }, [uiKey]);
	// History is per file: opening another one starts on its live content.
	useEffect(() => setPreviewVersion(null), [selectedPath, uiKey]);
	const exitFullscreen = useMemo(() => () => setFullscreen(false), []);
	useEffect(() => {
		if (request) {
			setPreviewPath(null);
			setSelectedPath(request.path);
		}
	}, [request]);

	const selected = artifacts.find((artifact) => artifact.path === selectedPath);
	const preview = artifacts.find((artifact) => artifact.path === previewPath);
	const artifactRow = (artifact: SessionArtifact): ArtifactRow => ({
		...artifact,
		sessionId: session.id,
		sessionTitle: session.title ?? "",
		generating: isAgentActivityWorking(session.activity),
	});
	const openInSession = (artifact: Pick<SessionArtifact, "kind" | "previewUrl" | "path">) => {
		if (artifact.kind === "html" && artifact.previewUrl) openArtifactPreview(artifact.previewUrl);
		else setSelectedPath(artifact.path);
	};
	// Versions are read back as text, so a binary file such as an image gets no
	// History. This shares the viewer's cached read of the file.
	const selectedFile = useQuery({
		...sessionArtifactFileQueryOptions(session.id, selected?.path ?? "", selected?.rawUrl, t("files.error.loadArtifact")),
		enabled: Boolean(selected) && !session.cloud,
	});
	useEffect(() => {
		if (!selected) setFullscreen(false);
	}, [selected]);
	if (selected) {
		const fullscreenLabel = t(fullscreen ? "artifacts.exitFullscreen" : "artifacts.fullscreen");
		const FullscreenIcon = fullscreen ? Minimize2 : Maximize2;
		return (
			<ArtifactViewerFrame fullscreen={fullscreen} label={selected.name} onExitFullscreen={exitFullscreen}>
				{/* One header row: back, the file's menu, the viewer's action buttons, and full screen. */}
				<div className={cn("flex min-h-9 shrink-0 items-center gap-1 border-b border-border bg-background px-1", fullscreen && isMacPlatform() && FULLSCREEN_MAC_HEADER_CLASS)}>
					<Button
						aria-label={t("artifacts.back")}
						onClick={() => setSelectedPath(null)}
						size="icon-sm"
						type="button"
						variant="ghost"
					>
						<ChevronLeft aria-hidden="true" className="size-icon-sm" />
					</Button>
					<ArtifactActionsMenu artifact={selected} hostId={hostId} key={selected.path} session={session} />
					<div className="ml-auto flex shrink-0 items-center" ref={setToolbarHost} />
					{session.cloud || selectedFile.data?.binary ? null : (
						<ArtifactHistoryMenu fallbackHarness={session.provider} hostId={hostId} onSelect={setPreviewVersion} path={selected.path} selectedVersion={previewVersion} sessionId={session.id} />
					)}
					<Tooltip>
						<TooltipTrigger asChild>
							<Button
								aria-label={fullscreenLabel}
								aria-pressed={fullscreen}
								className="text-muted-foreground hover:text-foreground"
								onClick={() => setFullscreen((current) => !current)}
								size="icon-sm"
								type="button"
								variant="ghost"
							>
								<FullscreenIcon aria-hidden="true" className="size-icon-sm" />
							</Button>
						</TooltipTrigger>
						<TooltipContent side="bottom">{fullscreenLabel}</TooltipContent>
					</Tooltip>
				</div>
				<div className="min-h-0 flex-1">
					<ArtifactViewerBody
						artifact={selected}
						feedbackRequestKey={request?.feedback && request.path === selected.path ? request.key : undefined}
						hostId={hostId}
						onFeedbackRequestConsumed={onRequestConsumed}
						onPreviewVersion={setPreviewVersion}
						previewVersion={previewVersion}
						readOnly={Boolean(session.cloud)}
						sessionId={session.id}
						toolbarHost={toolbarHost}
					/>
				</div>
			</ArtifactViewerFrame>
		);
	}

	return (
		<div className="@container/session-artifacts flex h-full min-h-0 min-w-0 flex-col" role="tabpanel">
			<div className="flex h-8 shrink-0 items-center gap-0.5 pl-3 pr-1 text-sm text-muted-foreground">
				<span className="min-w-0 flex-1 truncate">{t("artifacts.title")}</span>
				{/* The artifact directory is on this machine only for a local session. */}
				{!hostId && !session.cloud && artifacts.length > 0 ? <OpenArtifactsFolderButtons sessionId={session.id} /> : null}
			</div>
			{artifacts.length === 0 ? (
				<PanelMessage>{t("artifacts.sessionEmpty")}</PanelMessage>
			) : (
				<ul className="board-scrollbar grid min-h-0 flex-1 grid-cols-1 content-start gap-4 overflow-y-auto px-3 pb-3 pt-1 @[30rem]/session-artifacts:grid-cols-2">
					{artifacts.map((artifact) => {
						const row = artifactRow(artifact);
						const id = artifactKey(row);
						return (
							<ArtifactGalleryCard
								key={artifact.path}
								row={row}
								context={session.title ?? ""}
								onPreview={() => setPreviewPath(artifact.path)}
								actions={<ArtifactActionsMenu
									artifact={artifact}
									hostId={hostId}
									session={session}
									iconOnly
									pinned={pinnedArtifactIds.includes(id)}
									onTogglePin={!hostId && !session.cloud ? () => togglePinnedArtifact(id) : undefined}
									onOpenInSession={() => openInSession(artifact)}
									className="bg-[var(--solid-bg-secondary,var(--color-bg-secondary))] hover:bg-[var(--solid-accent,var(--accent))] dark:hover:bg-[var(--solid-accent,var(--accent))] transition-none duration-0 [@media(hover:hover)]:opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 data-[state=open]:opacity-100"
								/>}
							/>
						);
					})}
				</ul>
			)}
			<ArtifactPreviewModal
				artifact={preview ? artifactRow(preview) : null}
				projectName={session.workspaceName ?? ""}
				hostId={hostId}
				cloud={session.cloud}
				onOpenInSession={openInSession}
				onClose={() => setPreviewPath(null)}
			/>
		</div>
	);
}
