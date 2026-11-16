import { Fragment, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { ChevronLeft, Code2, Eye, FolderOpen, Maximize2, MessageSquarePlus, Minimize2, SquareTerminal, ZoomIn, ZoomOut } from "@/components/app-icons";
import { isInlineArtifactUrl } from "../../shared/agent-page-url";
import type { OpenArtifactsTargetId } from "../../shared/editor-handoff";
import { useOpenSessionArtifacts } from "../hooks/useEditorHandoff";
import { cloudArtifactFileUrl, useCloudArtifactAccess, usePinnedArtifactUrl } from "../hooks/useCloudArtifacts";
import { useHostServesMediaVersions, useRestoreArtifactVersion, type CloudArtifactVersions } from "../hooks/useArtifactVersions";
import { useArtifactFileChanges } from "../hooks/useArtifactFileChanges";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudMeUserId } from "../hooks/useCloudMeUserId";
import { useFileAnnotation } from "../hooks/useFileAnnotation";
import { CloudArtifactAccessError, sessionArtifactFileQueryOptions, type ArtifactSave } from "../hooks/useSessionWorkspaceFiles";
import { cloudSessionsQueryKey } from "../hooks/useWorkspaceQuery";
import type { ArtifactMenuAction, ArtifactMenuActions } from "../lib/artifact-menu";
import { ArtifactZoomContext, ArtifactZoomController, MAX_ARTIFACT_ZOOM, MIN_ARTIFACT_ZOOM, ZOOM_CONTROLS_REVEAL_PX, applyZoomShortcut, useArtifactZoomState, useArtifactZoomTarget, wheelZoomFactor, zoomShortcut } from "../lib/artifact-zoom";
import { aoBridge } from "../lib/bridge";
import { fileMediaKind } from "../lib/file-media";
import { isMacPlatform, isWindowsPlatform } from "../lib/platform";
import { artifactChangeError, canChangeSessionArtifacts, type ArtifactRow } from "../lib/artifacts";
import { sessionUiKey } from "../lib/hosts";
import { cn } from "../lib/utils";
import type { SessionArtifact, WorkspaceSession } from "../types/workspace";
import { AppBrowserLinkContext } from "./AppLink";
import { ArtifactActionsMenu, openCloudArtifactsFolder } from "./ArtifactActionsMenu";
import { artifactAtPath, deliverableArtifacts } from "../../../../packages/shared/artifacts/deliverables";
import { ShareArtifactButton, useReconciledShareLinks } from "./ShareArtifactPopover";
import { ArtifactGalleryCard } from "./ArtifactGalleryCard";
import { ArtifactFileView } from "./ArtifactFileView";
import { CloudArtifactFrame } from "./CloudArtifactFrame";
import { ArtifactHistoryMenu, ArtifactVersionPreview } from "./ArtifactHistory";
import { ArtifactPageFrame } from "./ArtifactPageFrame";
import { Button } from "./ui/button";
import { DropdownMenuItem, DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuSeparator } from "./ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { BrowserIcon } from "./icons";
import { FileAnnotationComposer, PanelMessage, RetryButton } from "./WorkspaceDiffView";
import { GridSpacer, useVirtualGridRows } from "../hooks/useVirtualGridRows";

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

/** A cloud session's artifacts copied to its folder under ~/.ao and opened there. */
function OpenCloudArtifactsFolderButton({ orgId, sessionId }: { orgId: string; sessionId: string }) {
	const { t } = useTranslation();
	const { client, baseUrl } = useCloudCp();
	const open = useMutation({ mutationFn: () => openCloudArtifactsFolder(client, baseUrl, orgId, sessionId) });
	const resetOpen = open.reset;
	useEffect(() => {
		if (!open.error) return;
		const timer = setTimeout(resetOpen, ERROR_CLEAR_MS);
		return () => clearTimeout(timer);
	}, [open.error, resetOpen]);
	const label = t("editor.openInTarget", { name: fileManagerName() });
	return (
		<>
			{open.error ? (
				<span className="min-w-0 truncate text-caption text-destructive" role="alert" title={open.error.message}>
					{open.error.message}
				</span>
			) : null}
			<Tooltip>
				<TooltipTrigger asChild>
					<Button aria-label={label} disabled={open.isPending} onClick={() => open.mutate()} size="icon-sm" type="button" variant="ghost">
						<FolderOpen aria-hidden="true" className="size-icon-sm" />
					</Button>
				</TooltipTrigger>
				<TooltipContent side="bottom">{label}</TooltipContent>
			</Tooltip>
		</>
	);
}

// The zoom toolbar fades out this long after the last zoom or pointer visit.
const ZOOM_CONTROLS_IDLE_MS = 1_500;

/**
 * The open artifact's zoom, floating over the bottom of the preview: out, the
 * level (which resets to fit), and in. Shown while the artifact can zoom, and
 * only while in use: it appears when a file opens, on any zoom (pinch, its
 * buttons or the shortcuts), when the pointer nears the bottom edge (over a
 * local framed page, the page reports that through the controller), and when
 * the pointer reaches its own spot, then fades out once idle. Hover or
 * keyboard focus keeps it up.
 */
function ArtifactZoomControls({ controller }: { controller: ArtifactZoomController }) {
	const { t } = useTranslation();
	const { available, zoom } = useArtifactZoomState(controller);
	const toolbarRef = useRef<HTMLDivElement>(null);
	const hideTimer = useRef<number | undefined>(undefined);
	const [shown, setShown] = useState(true);
	const [held, setHeld] = useState(false);
	const reveal = useCallback(() => {
		setShown(true);
		window.clearTimeout(hideTimer.current);
		hideTimer.current = window.setTimeout(() => setShown(false), ZOOM_CONTROLS_IDLE_MS);
	}, []);
	useEffect(() => () => window.clearTimeout(hideTimer.current), []);
	useEffect(() => {
		if (available) reveal();
	}, [available, reveal, zoom]);
	useEffect(() => {
		const area = toolbarRef.current?.parentElement;
		if (!available || !area) return;
		const onPointerMove = (event: PointerEvent) => {
			if (area.getBoundingClientRect().bottom - event.clientY <= ZOOM_CONTROLS_REVEAL_PX) reveal();
		};
		area.addEventListener("pointermove", onPointerMove);
		const unsubscribe = controller.onReveal(reveal);
		return () => {
			area.removeEventListener("pointermove", onPointerMove);
			unsubscribe();
		};
	}, [available, controller, reveal]);
	if (!available) return null;
	const visible = shown || held;
	const release = () => {
		setHeld(false);
		reveal();
	};
	const controls = [
		{ label: t("artifacts.zoomOut"), icon: ZoomOut, disabled: zoom <= MIN_ARTIFACT_ZOOM, onClick: () => controller.step(-1) },
		{ label: t("artifacts.zoomIn"), icon: ZoomIn, disabled: zoom >= MAX_ARTIFACT_ZOOM, onClick: () => controller.step(1) },
	];
	const button = ({ label, icon: Icon, disabled, onClick }: (typeof controls)[number]) => (
		<Tooltip key={label}>
			<TooltipTrigger asChild>
				<Button aria-label={label} disabled={disabled} onClick={onClick} size="icon-sm" type="button" variant="quiet">
					<Icon aria-hidden="true" className="size-icon-sm" />
				</Button>
			</TooltipTrigger>
			<TooltipContent side="top">{label}</TooltipContent>
		</Tooltip>
	);
	return (
		<div
			aria-label={t("artifacts.zoom")}
			className={cn(
				"absolute bottom-3 left-1/2 z-10 flex -translate-x-1/2 items-center gap-px rounded-lg border border-(--color-border-overlay) bg-overlay p-0.5 text-popover-foreground shadow-(--shadow-popover)",
				"transition-opacity duration-(--duration-normal) ease-out motion-reduce:transition-none",
				// Hidden, it still takes the pointer: hovering where it sits brings it
				// back, even over a framed page whose own pointer moves never reach here.
				!visible && "opacity-0",
			)}
			data-state={visible ? "visible" : "hidden"}
			onBlur={(event) => {
				if (!event.currentTarget.contains(event.relatedTarget as Node | null)) release();
			}}
			onFocus={() => setHeld(true)}
			onPointerEnter={() => setHeld(true)}
			onPointerLeave={release}
			ref={toolbarRef}
			role="group"
		>
			{button(controls[0])}
			<Tooltip>
				<TooltipTrigger asChild>
					<Button aria-label={t("artifacts.resetZoom")} className="min-w-10 px-1 tabular-nums" onClick={() => controller.reset()} size="sm" type="button" variant="quiet">
						{Math.round(zoom * 100)}%
					</Button>
				</TooltipTrigger>
				<TooltipContent side="top">{t("artifacts.resetZoom")}</TooltipContent>
			</Tooltip>
			{button(controls[1])}
		</div>
	);
}

/**
 * The open file's view actions, in its name menu ahead of the file actions:
 * an HTML artifact's page or source, the file view's display modes and
 * actions (Edit, Add feedback), Open in AO Browser, and History. The menu
 * mounts this each time it opens, so it reads the file view's current actions.
 * The file actions always follow, so every group ends with a separator.
 */
function ArtifactViewMenuItems({ fileActions, history, onOpenInAOBrowser, page }: {
	fileActions: ArtifactMenuActions;
	history: ReactNode;
	onOpenInAOBrowser?: () => void;
	page?: { showingSource: boolean; onShowSource: (source: boolean) => void };
}) {
	const { t } = useTranslation();
	const actions = fileActions.current;
	const modes = actions.filter((action) => action.mode);
	const others = actions.filter((action) => !action.mode);
	const groups: { key: string; node: ReactNode }[] = [];
	if (page) {
		groups.push({ key: "page", node: (
			<DropdownMenuRadioGroup onValueChange={(value) => page.onShowSource(value === "source")} value={page.showingSource ? "source" : "page"}>
				<DropdownMenuRadioItem value="page"><Eye aria-hidden="true" />{t("files.rendered")}</DropdownMenuRadioItem>
				<DropdownMenuRadioItem value="source"><Code2 aria-hidden="true" />{t("chat.render.viewSource")}</DropdownMenuRadioItem>
			</DropdownMenuRadioGroup>
		) });
	}
	if (modes.length > 1) {
		groups.push({ key: "modes", node: (
			<DropdownMenuRadioGroup
				onValueChange={(value) => modes.find((mode) => mode.id === value)?.onSelect()}
				value={modes.find((mode) => mode.mode?.checked)?.id ?? ""}
			>
				{modes.map((mode) => (
					<DropdownMenuRadioItem disabled={mode.disabled} key={mode.id} value={mode.id}>{mode.icon}{mode.label}</DropdownMenuRadioItem>
				))}
			</DropdownMenuRadioGroup>
		) });
	}
	if (others.length > 0 || onOpenInAOBrowser) {
		groups.push({ key: "actions", node: (
			<>
				{others.map((action: ArtifactMenuAction) => (
					<DropdownMenuItem disabled={action.disabled} key={action.id} onSelect={action.onSelect}>{action.icon}{action.label}</DropdownMenuItem>
				))}
				{onOpenInAOBrowser ? (
					<DropdownMenuItem onSelect={onOpenInAOBrowser}><BrowserIcon aria-hidden="true" />{t("inspector.openInAOBrowser")}</DropdownMenuItem>
				) : null}
			</>
		) });
	}
	if (history) groups.push({ key: "history", node: history });
	// Each group ends with a separator from the file actions after it, so a menu
	// with none of these starts straight at the file actions.
	return groups.map(({ key, node }) => (
		<Fragment key={key}>
			{node}
			<DropdownMenuSeparator />
		</Fragment>
	));
}

// Full screen covers the whole app window; Markdown keeps its shared reading layout.
const FULLSCREEN_FRAME_CLASS =
	"fixed inset-0 z-overlay flex flex-col bg-background outline-none";
// The window's macOS traffic lights sit over the overlay's header unless the
// window itself is full screen.
const FULLSCREEN_MAC_HEADER_CLASS = "min-h-traffic-light-clearance pl-(--size-titlebar-cluster-left) [html:has(.native-fullscreen)_&]:min-h-9 [html:has(.native-fullscreen)_&]:pl-1";
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
	onEngagedChange,
	onExitFullscreen,
	onKeyDown,
}: {
	children: ReactNode;
	fullscreen: boolean;
	label: string;
	/** Told whether the viewer is under the pointer or holds focus, also inside a framed page. */
	onEngagedChange?: (engaged: boolean) => void;
	onExitFullscreen: () => void;
	/** Keys pressed with focus in the viewer, which stays the same node in and out of full screen. */
	onKeyDown?: (event: KeyboardEvent) => void;
}) {
	const [frame] = useState(() => {
		const element = document.createElement("div");
		element.tabIndex = -1;
		return element;
	});
	const slotRef = useRef<HTMLDivElement>(null);
	const switchedRef = useRef(false);
	useLayoutEffect(() => () => frame.remove(), [frame]);
	useEffect(() => {
		if (!onKeyDown) return;
		frame.addEventListener("keydown", onKeyDown);
		return () => frame.removeEventListener("keydown", onKeyDown);
	}, [frame, onKeyDown]);
	useEffect(() => {
		if (!onEngagedChange) return;
		let hovered = false;
		try {
			hovered = frame.matches(":hover");
		} catch {
			// A DOM without :hover support starts unhovered.
		}
		let reported: boolean | undefined;
		// A framed page that takes focus leaves the iframe as this document's activeElement.
		const update = () => {
			const engaged = hovered || frame.contains(document.activeElement);
			if (engaged === reported) return;
			reported = engaged;
			onEngagedChange(engaged);
		};
		const enter = () => { hovered = true; update(); };
		const leave = () => { hovered = false; update(); };
		// Focus settles after focusout and window blur, so read it a tick later.
		const later = () => window.setTimeout(update, 0);
		frame.addEventListener("pointerenter", enter);
		frame.addEventListener("pointerleave", leave);
		frame.addEventListener("focusin", update);
		frame.addEventListener("focusout", later);
		window.addEventListener("blur", later);
		update();
		return () => {
			frame.removeEventListener("pointerenter", enter);
			frame.removeEventListener("pointerleave", leave);
			frame.removeEventListener("focusin", update);
			frame.removeEventListener("focusout", later);
			window.removeEventListener("blur", later);
			onEngagedChange(false);
		};
	}, [frame, onEngagedChange]);
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
	artifactRoot,
	cloud = false,
	cloudVersions,
	onAccessRefused,
	feedbackRequestKey,
	initialEditing,
	htmlSource,
	hostId,
	onFeedbackRequestConsumed,
	onPreviewVersion,
	previewVersion,
	readOnly,
	save,
	sendMessage,
	sessionId,
	toolbarHost,
	onEditingChange,
	menuActions,
	zoomController,
}: {
	artifact: SessionArtifact;
	/** A cloud file's access prefix, which relative links resolve against. */
	artifactRoot?: string;
	/** Reads the file through the cloud bridge. */
	cloud?: boolean;
	/** A cloud file's versions, kept by the control plane. */
	cloudVersions?: CloudArtifactVersions;
	/** Saves an edit to the control plane, for a cloud file. */
	save?: ArtifactSave;
	/** Takes a fresh access link for a cloud file whose link was refused. */
	onAccessRefused?: () => void;
	feedbackRequestKey?: number;
	initialEditing?: boolean;
	htmlSource: boolean;
	hostId?: string;
	onFeedbackRequestConsumed?: (key: number) => void;
	onPreviewVersion: (version: number | null) => void;
	previewVersion: number | null;
	readOnly: boolean;
	/** Sends feedback to a cloud session's worker. */
	sendMessage?: (message: string) => Promise<void>;
	sessionId: string;
	toolbarHost: HTMLElement | null;
	onEditingChange?: (editing: boolean) => void;
	/** The view's zoom, applied by whichever of the file or its page is showing. */
	zoomController: ArtifactZoomController;
	/** The header's file menu, which the shown file view fills with its actions. */
	menuActions: ArtifactMenuActions;
}) {
	const restore = useRestoreArtifactVersion({ cloud: cloudVersions, hostId, path: artifact.path, rawUrl: artifact.rawUrl, sessionId });
	const previewing = previewVersion !== null;
	const media = fileMediaKind(artifact.path);
	const page = artifact.kind === "html" && artifact.inlineUrl && isInlineArtifactUrl(artifact.inlineUrl) ? artifact.inlineUrl : undefined;
	const showingPage = Boolean(page) && !htmlSource && !previewing;
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
				{/* A hidden video or audio file would go on playing under the version shown. */}
				{previewing && media ? null : <div className={cn("h-full", (previewing || showingPage) && "hidden")}>
					<ArtifactZoomContext.Provider value={previewing || showingPage ? null : zoomController}>
					<ArtifactFileView
						artifactName={artifact.name}
						initialEditing={initialEditing}
						artifactRoot={artifactRoot}
						cloud={cloud}
						onAccessRefused={onAccessRefused}
						feedbackRequestKey={showingPage ? undefined : feedbackRequestKey}
						hostId={hostId}
						onFeedbackRequestConsumed={onFeedbackRequestConsumed}
						path={artifact.path}
						rawUrl={artifact.rawUrl}
						readOnly={readOnly}
						save={save}
						sendMessage={sendMessage}
						sessionId={sessionId}
						toolbarHost={previewing || showingPage ? null : toolbarHost}
						menuActions={previewing || showingPage ? undefined : menuActions}
						onEditingChange={onEditingChange}
					/>
					</ArtifactZoomContext.Provider>
				</div>}
				{showingPage && page ? (
					<ArtifactZoomContext.Provider value={zoomController}>
						<ArtifactPageFeedback
							feedbackRequestKey={feedbackRequestKey}
							hostId={hostId}
							menuActions={menuActions}
							name={artifact.name}
							onFeedbackRequestConsumed={onFeedbackRequestConsumed}
							path={artifact.path}
							sendMessage={sendMessage}
							sessionId={sessionId}
						>
							<ArtifactPageFrame key={`${page}:${artifact.updatedAt}`} src={page} title={artifact.name} />
						</ArtifactPageFeedback>
					</ArtifactZoomContext.Provider>
				) : null}
				{previewing ? (
					<ArtifactVersionPreview
						artifactOrigin={artifactOrigin}
						cloud={cloudVersions}
						hostId={hostId}
						onBack={() => onPreviewVersion(null)}
						onRestore={readOnly ? undefined : () => {
							if (previewVersion !== null) restore.mutate({ version: previewVersion }, { onSuccess: () => onPreviewVersion(null) });
						}}
						path={artifact.path}
						rawUrl={artifact.rawUrl}
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
 * A rendered page with whole-file feedback for its worker, for an HTML
 * artifact shown as its page. The framed page is in another origin, so
 * feedback is on the file, not a selection in it.
 */
function ArtifactPageFeedback({
	children,
	feedbackRequestKey,
	hostId,
	menuActions,
	name,
	onFeedbackRequestConsumed,
	path,
	sendMessage,
	sessionId,
}: {
	children: ReactNode;
	feedbackRequestKey?: number;
	hostId?: string;
	/** The header's file menu, which takes the feedback action. */
	menuActions: ArtifactMenuActions;
	name: string;
	onFeedbackRequestConsumed?: (key: number) => void;
	path: string;
	/** Sends feedback to the worker; without it feedback goes through the daemon. */
	sendMessage?: (message: string) => Promise<void>;
	sessionId: string;
}) {
	const { t } = useTranslation();
	const annotation = useFileAnnotation(sessionId, { hostId, source: name, sendMessage });
	const begin = annotation.begin;
	useEffect(() => {
		if (feedbackRequestKey === undefined) return;
		begin({ path, side: "file", surface: "focused" });
		onFeedbackRequestConsumed?.(feedbackRequestKey);
	}, [feedbackRequestKey, onFeedbackRequestConsumed, path]);
	const feedbackLabel = t("files.addFeedback");
	useLayoutEffect(() => {
		menuActions.current = [{ id: "feedback", label: feedbackLabel, icon: <MessageSquarePlus aria-hidden="true" />, onSelect: () => begin({ path, side: "file", surface: "focused" }) }];
		return () => { menuActions.current = []; };
	}, [begin, feedbackLabel, menuActions, path]);
	const target = annotation.targets.find((item) => item.surface !== "review" && item.path === path && item.side === "file" && item.line == null && item.quote == null);
	return (
		<div className="flex h-full min-h-0 flex-col">
			{target ? <FileAnnotationComposer annotation={annotation} target={target} /> : null}
			<div className="relative min-h-0 flex-1 overflow-hidden">{children}</div>
		</div>
	);
}

/** A cloud session's HTML artifact, framed from the control plane. */
function CloudHtmlArtifactView({
	artifact,
	feedbackRequestKey,
	menuActions,
	onAccessRefused,
	onFeedbackRequestConsumed,
	onNavigate,
	sendMessage,
	sessionId,
	url,
}: {
	artifact: SessionArtifact;
	feedbackRequestKey?: number;
	/** The header's file menu, which takes the feedback action. */
	menuActions: ArtifactMenuActions;
	onAccessRefused: () => void;
	onFeedbackRequestConsumed?: (key: number) => void;
	/** Called with the path of another file the page opened. */
	onNavigate: (path: string) => void;
	sendMessage: (message: string) => Promise<void>;
	sessionId: string;
	url: string;
}) {
	// The page is in the control plane's opaque origin, so the frame itself
	// zooms: from the viewer's buttons and shortcuts, not a pinch inside it.
	const { zoom } = useArtifactZoomTarget();
	return (
		<ArtifactPageFeedback
			feedbackRequestKey={feedbackRequestKey}
			menuActions={menuActions}
			name={artifact.name}
			onFeedbackRequestConsumed={onFeedbackRequestConsumed}
			path={artifact.path}
			sendMessage={sendMessage}
			sessionId={sessionId}
		>
			<CloudArtifactFrame onAccessRefused={onAccessRefused} onNavigate={onNavigate} title={artifact.name} url={url} zoom={zoom} />
		</ArtifactPageFeedback>
	);
}

/**
 * The inspector's Artifacts view (its own inspector tab): every file in
 * the session's artifact directory. Files open in place of the gallery,
 * with isolated inline HTML previews and an optional full-screen view.
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
	const openInBrowser = useContext(AppBrowserLinkContext);
	const cloud = Boolean(session.cloud);
	const cloudAccess = useCloudArtifactAccess(session, cloud && (session.artifactFiles?.length ?? 0) > 0);
	const cloudRoot = cloudAccess.data;
	// A cloud file's URL comes from the session's short-lived access prefix.
	const artifacts = useMemo(
		() =>
			sortedArtifacts(session.artifactFiles ?? []).map((artifact) =>
				cloud && cloudRoot ? { ...artifact, rawUrl: cloudArtifactFileUrl(cloudRoot, artifact.path, artifact.updatedAt) } : artifact,
			),
		[cloud, cloudRoot, session.artifactFiles],
	);
	// The grid lists deliverables only. `artifacts` stays whole, so a link to any
	// other file of the session still opens below.
	const listedArtifacts = useMemo(() => deliverableArtifacts(artifacts), [artifacts]);
	const livePreviewPaths = useMemo(() => new Set(
		listedArtifacts.filter((artifact) =>
			(artifact.kind === "html" && (cloud || (artifact.inlineUrl && artifact.rawUrl)))
			|| (/\.pdf$/i.test(artifact.path) && (cloud || artifact.rawUrl)),
		).slice(0, 4).map((artifact) => artifact.path),
	), [listedArtifacts, cloud]);
	const [selectedPath, setSelectedPath] = useState<string | null>(null);
	// A file a request opened before the session listed it, until the list has
	// it. Only that file gets the not-listed-yet note: one that leaves the list
	// later (deleted here or by the agent) is gone, not on its way.
	const [awaitedPath, setAwaitedPath] = useState<string | null>(null);
	const [editingPath, setEditingPath] = useState<string | null>(null);
	// Share hides while the open file is being edited.
	const [editingFile, setEditingFile] = useState(false);
	const [htmlSourcePath, setHtmlSourcePath] = useState<string | null>(null);
	const [toolbarHost, setToolbarHost] = useState<HTMLElement | null>(null);
	const [fullscreen, setFullscreen] = useState(false);
	const [previewVersion, setPreviewVersion] = useState<number | null>(null);
	const [zoomController] = useState(() => new ArtifactZoomController());
	// The shown file view's actions, which its name menu lists.
	const fileMenuActions = useRef<ArtifactMenuAction[]>([]);
	const contentRef = useRef<HTMLDivElement>(null);
	const uiKey = sessionUiKey(session.id, hostId);
	// Who each published card's link opens for, so it is marked as on the Artifacts page.
	const shareAccess = useReconciledShareLinks();
	const { client: cloudClient } = useCloudCp(cloud);
	// Only the person who started a cloud session shares its files.
	const cloudUserId = useCloudMeUserId(cloud);
	const cloudOwner = Boolean(cloudUserId) && session.cloud?.createdByUserId === cloudUserId;
	const cloudOrgId = session.cloud?.orgId;
	// Another member's cloud session files open view only: no edit, restore,
	// delete or copy of them (the control plane refuses the changes too).
	const owner = canChangeSessionArtifacts(session.cloud, cloudUserId);
	const queryClient = useQueryClient();
	// A cloud session's artifacts are edited and versioned in the control plane,
	// which hands a save to the session's worker.
	const cloudVersions = useMemo<CloudArtifactVersions | undefined>(
		() => (cloudOrgId ? { client: cloudClient, orgId: cloudOrgId } : undefined),
		[cloudClient, cloudOrgId],
	);
	const saveCloudArtifact = useCallback<ArtifactSave>(async ({ content, expectedFileFingerprint, path }) => {
		if (!cloudOrgId) throw new Error(t("files.saveError"));
		const { artifact } = await cloudClient.saveSessionArtifact(cloudOrgId, session.id, { path, content, expectedSha256: expectedFileFingerprint })
			.catch((error: unknown) => { throw artifactChangeError(error, t("artifacts.viewOnly")); });
		// The file's link changes with it, so the list's refresh reads it again.
		await queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
		return { fileFingerprint: artifact.sha256, size: artifact.size };
	}, [cloudClient, cloudOrgId, queryClient, session.id, t]);
	// A cloud session's worker gets feedback from the control plane, not the daemon.
	const sendCloudFeedback = useCallback(async (message: string) => {
		if (!cloudOrgId) throw new Error(t("files.feedbackError"));
		await cloudClient.sendSessionMessage(cloudOrgId, session.id, { text: message });
	}, [cloudClient, cloudOrgId, session.id, t]);

	useEffect(() => { setSelectedPath(null); setAwaitedPath(null); setEditingPath(null); setHtmlSourcePath(null); }, [uiKey]);
	// History is per file: opening another one starts on its live content.
	useEffect(() => setPreviewVersion(null), [selectedPath, uiKey]);
	// Every file, and each look at its page or source, opens at fit.
	useEffect(() => zoomController.reset(), [htmlSourcePath, previewVersion, selectedPath, uiKey, zoomController]);
	const onViewerKeyDown = useCallback((event: KeyboardEvent) => {
		const action = zoomShortcut(event, isMacPlatform());
		if (!action || event.defaultPrevented || !zoomController.hasTarget()) return;
		// Ahead of the app menu's own zoom, which would scale the whole window.
		// In the desktop app main claims the chord first while the viewer is
		// engaged (below); this covers the same keys anywhere else.
		event.preventDefault();
		applyZoomShortcut(zoomController, action);
	}, [zoomController]);
	// While a viewer that can zoom is under the pointer or holds focus (also
	// inside a framed page, whose keys never reach this view), main claims
	// Cmd+=/-/0 ahead of the app menu and sends them here.
	const viewerEngaged = useRef(false);
	const reportEngaged = useCallback(() => {
		aoBridge.artifactZoom.setEngaged(viewerEngaged.current && zoomController.hasTarget());
	}, [zoomController]);
	const onViewerEngagedChange = useCallback((engaged: boolean) => {
		viewerEngaged.current = engaged;
		reportEngaged();
	}, [reportEngaged]);
	useEffect(() => {
		const unsubscribe = zoomController.subscribe(reportEngaged);
		const stopShortcuts = aoBridge.artifactZoom.onShortcut((action) => {
			if (zoomController.hasTarget()) applyZoomShortcut(zoomController, action);
		});
		return () => {
			unsubscribe();
			stopShortcuts();
			aoBridge.artifactZoom.setEngaged(false);
		};
	}, [reportEngaged, zoomController]);
	const exitFullscreen = useMemo(() => () => setFullscreen(false), []);
	useEffect(() => {
		if (request) {
			setEditingPath(null);
			setHtmlSourcePath(null);
			setSelectedPath(request.path);
			setAwaitedPath(request.path);
		}
	}, [request]);

	// A file renamed while it is open, or opened by a link from before its
	// rename, is found by the path it had.
	const listed = selectedPath === null ? undefined : artifactAtPath(artifacts, selectedPath);
	// The awaited file is listed once the lookup finds it, under its own path
	// or, renamed since the link was written, under a new one.
	if (awaitedPath !== null && listed) setAwaitedPath(null);
	useEffect(() => {
		if (listed && listed.path !== selectedPath) setSelectedPath(listed.path);
	}, [listed, selectedPath]);
	// An open cloud file keeps the URL it opened with while the access prefix
	// is refreshed, so the refresh does not reload it.
	const pinned = usePinnedArtifactUrl(cloud ? cloudRoot : undefined, cloud ? listed?.path : undefined, listed?.updatedAt);
	const selected = listed && cloud ? { ...listed, rawUrl: pinned.url } : listed;
	const artifactRow = (artifact: SessionArtifact): ArtifactRow => ({
		...artifact,
		sessionId: session.id,
		sessionTitle: session.title ?? "",
		cloud: session.cloud,
		hostId,
		...(artifact.shareUrl && shareAccess.has(artifact.shareUrl) ? { shareAccess: shareAccess.get(artifact.shareUrl) } : {}),
	});
	const openArtifact = (artifact: Pick<SessionArtifact, "path">) => {
		setEditingPath(null);
		setHtmlSourcePath(null);
		setSelectedPath(artifact.path);
	};
	// A file the agent rewrites while it is open shows the new content.
	useArtifactFileChanges(session, Boolean(selected) && !hostId);
	// Versions are read back as text or, from a daemon that can, by the media
	// viewers, so a cloud media file and any other binary file, such as an
	// archive, get no History. This shares the viewer's cached read of the file, which for a
	// cloud file also shows when its access link was refused.
	const selectedFile = useQuery({
		...sessionArtifactFileQueryOptions(session.id, selected?.path ?? "", selected?.rawUrl, t("files.error.loadArtifact"), cloud),
		// A cloud page is framed, not read, until its source is shown.
		enabled: Boolean(selected) && (!cloud || (Boolean(selected?.rawUrl) && (selected?.kind !== "html" || htmlSourcePath === selected?.path))),
	});
	const hostServesMediaVersions = useHostServesMediaVersions({
		enabled: Boolean(hostId && !cloud && selected && fileMediaKind(selected.path)),
		hostId,
		path: selected?.path ?? "",
		sessionId: session.id,
	});
	const refetchAccess = cloudAccess.refetch;
	const repin = pinned.repin;
	const accessRefused = useCallback(() => {
		void refetchAccess();
		repin();
	}, [refetchAccess, repin]);
	// A page that opens another file of its session selects it, so the header
	// names the file shown.
	const followFrame = useCallback((path: string) => {
		const artifact = artifactAtPath(session.artifactFiles, path);
		if (artifact) setSelectedPath(artifact.path);
	}, [session.artifactFiles]);
	const readRefused = selectedFile.error instanceof CloudArtifactAccessError;
	useEffect(() => {
		if (readRefused) accessRefused();
	}, [readRefused, accessRefused]);
	useEffect(() => {
		if (!selected) setFullscreen(false);
	}, [selected]);
	// A trackpad pinch arrives as a wheel event with ctrlKey: it zooms the
	// artifact, never the app. A page's own pinch comes through its frame.
	const hasSelection = Boolean(selected);
	useEffect(() => {
		const content = contentRef.current;
		if (!content) return;
		const onWheel = (event: WheelEvent) => {
			if (!event.ctrlKey) return;
			event.preventDefault();
			if (zoomController.hasTarget()) zoomController.zoomBy(wheelZoomFactor(event), { clientX: event.clientX, clientY: event.clientY });
		};
		content.addEventListener("wheel", onWheel, { passive: false });
		return () => content.removeEventListener("wheel", onWheel);
	}, [hasSelection, zoomController]);
	// Only the cards in view and near it are built; the list itself scrolls.
	const grid = useVirtualGridRows(listedArtifacts.length);
	if (selected) {
		const fullscreenLabel = t(fullscreen ? "common.returnToPanel" : "common.maximize");
		const FullscreenIcon = fullscreen ? Minimize2 : Maximize2;
		const hasHtmlPreview = selected.kind === "html" && (cloud || (selected.inlineUrl && isInlineArtifactUrl(selected.inlineUrl)));
		const showingHtmlSource = htmlSourcePath === selected.path;
		// The page also opens in the session's Browser, the same target as
		// `ao preview` on its path; full screen is left first since it covers it.
		const browserUrl = openInBrowser && !cloud && previewVersion === null ? selected.previewUrl : undefined;
		return (
			<ArtifactViewerFrame fullscreen={fullscreen} label={selected.name} onEngagedChange={onViewerEngagedChange} onExitFullscreen={exitFullscreen} onKeyDown={onViewerKeyDown}>
				{/* One header row: back, the file's name menu (its view and file actions), an edit's Save, Share, and full screen. */}
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
					<ArtifactActionsMenu artifact={selected} downloadUrl={listed?.rawUrl} className="w-fit min-w-0 max-w-full shrink justify-start" hostId={hostId} key={selected.path} session={session}>
						<ArtifactViewMenuItems
							fileActions={fileMenuActions}
							history={(fileMediaKind(selected.path) ? session.cloud || (hostId && !hostServesMediaVersions) : selectedFile.data?.binary) ? null : (
								<ArtifactHistoryMenu cloud={cloudVersions} fallbackHarness={session.provider} hostId={hostId} onSelect={setPreviewVersion} path={selected.path} selectedVersion={previewVersion} sessionId={session.id} />
							)}
							onOpenInAOBrowser={browserUrl ? () => { setFullscreen(false); openInBrowser?.(browserUrl); } : undefined}
							page={hasHtmlPreview && previewVersion === null ? { showingSource: showingHtmlSource, onShowSource: (source) => setHtmlSourcePath(source ? selected.path : null) } : undefined}
						/>
					</ArtifactActionsMenu>
					{/* The name sizes to its text; the rest of the row stays empty. */}
					<div aria-hidden="true" className="min-w-0 flex-1" />
					<div className="flex shrink-0 items-center gap-0.5">
						<div className="flex shrink-0 items-center" ref={setToolbarHost} />
						{/* The artifact's public link. Only the person who started a cloud
						    session publishes its files. */}
						{editingFile || session.cloud?.shared ? null : session.cloud ? (!cloudOwner ? null : (
							<ShareArtifactButton
								file={{ source: "cloud", orgId: session.cloud.orgId, sessionId: session.id, path: selected.path, projectId: "", ...(selected.id ? { artifactId: selected.id } : {}), ...(selected.sha256 ? { fingerprint: selected.sha256 } : {}) }}
								name={selected.name}
								published={Boolean(selected.shareUrl)}
								revision={listed?.updatedAt}
							/>
						)) : !hostId ? (
							<ShareArtifactButton
								file={{ sessionId: session.id, path: selected.path, projectId: session.workspaceId, ...(selected.id ? { artifactId: selected.id } : {}) }}
								name={selected.name}
								published={Boolean(selected.shareUrl)}
								revision={listed?.updatedAt}
							/>
						) : null}
						<Tooltip>
							<TooltipTrigger asChild>
								<Button
									aria-label={fullscreenLabel}
									aria-pressed={fullscreen}
									onClick={() => { if (cloud) pinned.refresh(); setFullscreen((current) => !current); }}
									size="icon-sm"
									type="button"
									variant="quiet"
								>
									<FullscreenIcon aria-hidden="true" className="size-icon-sm" />
								</Button>
							</TooltipTrigger>
							<TooltipContent side="bottom">{fullscreenLabel}</TooltipContent>
						</Tooltip>
					</div>
				</div>
				<div className="relative min-h-0 flex-1" ref={contentRef}>
					{cloud && !selected.rawUrl ? (
						cloudAccess.isError ? (
							<PanelMessage action={<RetryButton onClick={() => void cloudAccess.refetch()} />}>{t("artifacts.accessError")}</PanelMessage>
						) : (
							<PanelMessage>{t("files.loading")}</PanelMessage>
						)
					) : cloud && selected.kind === "html" && !showingHtmlSource && previewVersion === null ? (
						<ArtifactZoomContext.Provider value={zoomController}>
							<CloudHtmlArtifactView
								artifact={selected}
								feedbackRequestKey={request?.feedback && request.path === selected.path ? request.key : undefined}
								menuActions={fileMenuActions}
								onAccessRefused={accessRefused}
								onFeedbackRequestConsumed={onRequestConsumed}
								onNavigate={followFrame}
								sendMessage={sendCloudFeedback}
								sessionId={session.id}
								url={selected.rawUrl!}
							/>
						</ArtifactZoomContext.Provider>
					) : (
						<ArtifactViewerBody
							artifact={selected}
							htmlSource={showingHtmlSource}
							initialEditing={editingPath === selected.path && Boolean(selectedFile.data?.editable && selectedFile.data?.fileFingerprint) && !selectedFile.data?.binary && !selectedFile.data?.contentTruncated}
							artifactRoot={cloud ? pinned.root : undefined}
							cloud={cloud}
							cloudVersions={cloud ? cloudVersions : undefined}
							onAccessRefused={cloud ? accessRefused : undefined}
							feedbackRequestKey={request?.feedback && request.path === selected.path ? request.key : undefined}
							hostId={hostId}
							onFeedbackRequestConsumed={onRequestConsumed}
							onPreviewVersion={setPreviewVersion}
							previewVersion={previewVersion}
							readOnly={!owner}
							save={cloud && owner ? saveCloudArtifact : undefined}
							sendMessage={cloud ? sendCloudFeedback : undefined}
							sessionId={session.id}
							toolbarHost={toolbarHost}
							onEditingChange={setEditingFile}
							zoomController={zoomController}
							menuActions={fileMenuActions}
						/>
					)}
					<ArtifactZoomControls controller={zoomController} />
				</div>
			</ArtifactViewerFrame>
		);
	}

	return (
		<div className="flex h-full min-h-0 min-w-0 flex-col" role="tabpanel">
			<div className="flex h-8 shrink-0 items-center gap-0.5 pl-3 pr-1 text-sm text-muted-foreground">
				<span className="min-w-0 flex-1 truncate">{t("artifacts.title")}</span>
				{/* The artifact directory is on this machine only for a local session. */}
				{!hostId && !session.cloud && artifacts.length > 0 ? <OpenArtifactsFolderButtons sessionId={session.id} /> : null}
				{session.cloud && owner && artifacts.length > 0 ? <OpenCloudArtifactsFolderButton orgId={session.cloud.orgId} sessionId={session.id} /> : null}
			</div>
			{/* A file opened by its path (a chat link to one the agent just wrote)
			    shows once the session lists it; until then, say so rather than
			    leave the gallery as if nothing was asked. */}
			{selectedPath !== null && selectedPath === awaitedPath ? (
				<PanelMessage action={<Button onClick={() => setSelectedPath(null)} size="sm" type="button" variant="secondary">{t("artifacts.back")}</Button>}>
					{t("artifacts.notListedYet", { name: selectedPath })}
				</PanelMessage>
			) : artifacts.length === 0 ? (
				<PanelMessage>{t("artifacts.sessionEmpty")}</PanelMessage>
			) : (
				<ul ref={grid.listRef} className="board-scrollbar grid min-h-0 flex-1 grid-cols-2 content-start gap-3 overflow-y-auto px-3 pb-3 pt-1">
					<GridSpacer height={grid.before} />
					{listedArtifacts.slice(grid.start, grid.end).map((artifact) => {
						const row = artifactRow(artifact);
						return (
							<ArtifactGalleryCard
								key={artifact.path}
								row={row}
								previewLive={livePreviewPaths.has(artifact.path)}
								// Every card here belongs to this session, so only its time shows.
								context=""
								onPreview={() => openArtifact(artifact)}
								// File actions would reach only the one file a bundle's row opens.
								actions={artifact.bundle ? null : <ArtifactActionsMenu
									artifact={artifact}
									hostId={hostId}
									session={session}
									iconOnly
									variant="sidebar"
									onEdit={() => { setEditingPath(artifact.path); setHtmlSourcePath(artifact.path); setSelectedPath(artifact.path); }}
									className="bg-[var(--solid-bg-secondary,var(--color-bg-secondary))] hover:bg-[var(--solid-accent,var(--accent))] dark:hover:bg-[var(--solid-accent,var(--accent))] transition-none duration-0 [@media(hover:hover)]:opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 data-[state=open]:opacity-100"
								/>}
							/>
						);
					})}
					<GridSpacer height={grid.after} />
				</ul>
			)}
		</div>
	);
}
