import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { ChevronDown, ChevronLeft, Copy, FileText, FolderOpen, Globe, Maximize2, Minimize2, SquareTerminal, Trash2 } from "lucide-react";
import type { OpenArtifactsTargetId } from "../../shared/editor-handoff";
import { useOpenSessionArtifacts } from "../hooks/useEditorHandoff";
import { useRestoreArtifactVersion } from "../hooks/useArtifactVersions";
import { useOpenArtifactPreview } from "../hooks/useOpenArtifactPreview";
import { deleteSessionArtifactFile, sessionArtifactFileQueryOptions } from "../hooks/useSessionWorkspaceFiles";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { aoBridge } from "../lib/bridge";
import { isMacPlatform, isWindowsPlatform } from "../lib/platform";
import { formatTimeTerse } from "../lib/format-time";
import { sessionUiKey } from "../lib/hosts";
import { cn } from "../lib/utils";
import type { SessionArtifact, WorkspaceSession } from "../types/workspace";
import { ArtifactFileView } from "./ArtifactFileView";
import { ArtifactHistoryMenu, ArtifactVersionPreview } from "./ArtifactHistory";
import { ConfirmDialog } from "./ConfirmDialog";
import { Button } from "./ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "./ui/dropdown-menu";
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

/**
 * The open artifact's name, as a menu: show it in the file manager or a
 * terminal (local sessions only), copy its contents, or delete it.
 */
function ArtifactMenu({ artifact, hostId, session }: { artifact: SessionArtifact; hostId?: string; session: WorkspaceSession }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const open = useOpenSessionArtifacts();
	const [copyError, setCopyError] = useState("");
	const [confirmingDelete, setConfirmingDelete] = useState(false);
	// The artifact directory is on this machine only for a local session.
	const local = !hostId && !session.cloud;
	// The deleted file leaves the session's artifact list on the next read.
	const remove = useMutation({
		mutationFn: () => deleteSessionArtifactFile({ path: artifact.path, sessionId: session.id, hostId }),
		onSuccess: async () => {
			setConfirmingDelete(false);
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
		},
	});
	const markdown = /\.(md|markdown)$/i.test(artifact.path);
	const reveal = (targetId: OpenArtifactsTargetId) => {
		setCopyError("");
		open.mutate({ sessionId: session.id, targetId, path: artifact.path });
	};
	const copy = async () => {
		open.reset();
		setCopyError("");
		try {
			const detail = await queryClient.fetchQuery(sessionArtifactFileQueryOptions(session.id, artifact.path, artifact.rawUrl, t("files.error.loadArtifact")));
			await aoBridge.clipboard.writeText(detail.content);
		} catch (error) {
			setCopyError(error instanceof Error ? error.message : String(error));
		}
	};
	const error = open.error?.message || copyError;
	const resetOpen = open.reset;
	useEffect(() => {
		if (!error) return;
		const timer = setTimeout(() => {
			resetOpen();
			setCopyError("");
		}, ERROR_CLEAR_MS);
		return () => clearTimeout(timer);
	}, [error, resetOpen]);
	return (
		<>
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<Button className="min-w-0 gap-1 px-1.5 font-normal" size="sm" title={artifact.path} type="button" variant="ghost">
						<span className="truncate">{artifact.name}</span>
						<ChevronDown aria-hidden="true" className="size-icon-xs shrink-0 text-muted-foreground" />
					</Button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="start" className="min-w-44" data-browser-native-overlay="true">
					{local ? (
						<>
							<DropdownMenuItem onSelect={() => reveal("file-manager")}>
								<FolderOpen aria-hidden="true" />
								{t("artifacts.showIn", { name: fileManagerName() })}
							</DropdownMenuItem>
							<DropdownMenuItem onSelect={() => reveal("terminal")}>
								<SquareTerminal aria-hidden="true" />
								{t("artifacts.showIn", { name: terminalName() })}
							</DropdownMenuItem>
							<DropdownMenuSeparator />
						</>
					) : null}
					<DropdownMenuItem onSelect={() => void copy()}>
						<Copy aria-hidden="true" />
						{t(markdown ? "artifacts.copyMarkdown" : "artifacts.copyContents")}
					</DropdownMenuItem>
					{/* Cloud sessions keep their artifacts behind the cloud control plane. */}
					{session.cloud ? null : (
						<>
							<DropdownMenuSeparator />
							<DropdownMenuItem
								className="text-destructive focus:text-destructive [&_svg]:text-destructive focus:[&_svg]:text-destructive"
								onSelect={() => {
									remove.reset();
									setConfirmingDelete(true);
								}}
							>
								<Trash2 aria-hidden="true" />
								{t("artifacts.delete")}
							</DropdownMenuItem>
						</>
					)}
				</DropdownMenuContent>
			</DropdownMenu>
			<ConfirmDialog
				busy={remove.isPending}
				confirmLabel={t("artifacts.delete")}
				description={t("artifacts.deleteDescription", { name: artifact.name })}
				destructive
				error={remove.error?.message ?? null}
				onConfirm={() => remove.mutate()}
				onOpenChange={setConfirmingDelete}
				open={confirmingDelete}
				title={t("artifacts.deleteTitle")}
			/>
			{error ? (
				<span className="min-w-0 truncate text-caption text-destructive" role="alert" title={error}>
					{error}
				</span>
			) : null}
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
	const [toolbarHost, setToolbarHost] = useState<HTMLElement | null>(null);
	const [fullscreen, setFullscreen] = useState(false);
	const [previewVersion, setPreviewVersion] = useState<number | null>(null);
	const openArtifactPreview = useOpenArtifactPreview(session.id, hostId);
	const uiKey = sessionUiKey(session.id, hostId);

	useEffect(() => setSelectedPath(null), [uiKey]);
	// History is per file: opening another one starts on its live content.
	useEffect(() => setPreviewVersion(null), [selectedPath, uiKey]);
	const exitFullscreen = useMemo(() => () => setFullscreen(false), []);
	useEffect(() => {
		if (request) setSelectedPath(request.path);
	}, [request]);

	const selected = artifacts.find((artifact) => artifact.path === selectedPath);
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
					<ArtifactMenu artifact={selected} hostId={hostId} key={selected.path} session={session} />
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
		<div className="flex h-full min-h-0 flex-col" role="tabpanel">
			<div className="flex h-8 shrink-0 items-center gap-0.5 pl-3 pr-1 text-sm text-muted-foreground">
				<span className="min-w-0 flex-1 truncate">{t("artifacts.title")}</span>
				{/* The artifact directory is on this machine only for a local session. */}
				{!hostId && !session.cloud && artifacts.length > 0 ? <OpenArtifactsFolderButtons sessionId={session.id} /> : null}
			</div>
			{artifacts.length === 0 ? (
				<PanelMessage>{t("artifacts.sessionEmpty")}</PanelMessage>
			) : (
				<ul className="board-scrollbar flex min-h-0 flex-1 flex-col gap-1.5 overflow-y-auto px-3 pb-3 pt-1">
					{artifacts.map((artifact) => {
						const html = artifact.kind === "html" && Boolean(artifact.previewUrl);
						const Icon = html ? Globe : FileText;
						return (
							<li key={artifact.path}>
								<button
									className="flex w-full min-w-0 items-center gap-2 rounded-md border border-(--color-border-settings-input) px-2.5 py-1.5 text-left text-sm outline-none transition-colors hover:bg-interactive-hover focus-visible:bg-interactive-hover focus-visible:ring-1 focus-visible:ring-ring"
									onClick={() => (html ? openArtifactPreview(artifact.previewUrl!) : setSelectedPath(artifact.path))}
									title={artifact.path}
									type="button"
								>
									<Icon aria-hidden="true" className="size-icon-sm shrink-0 text-settings-muted" />
									<span className="min-w-0 flex-1 truncate">{artifact.name}</span>
									{Date.parse(artifact.updatedAt) ? (
										<span className="shrink-0 text-caption text-passive">{formatTimeTerse(artifact.updatedAt)}</span>
									) : null}
								</button>
							</li>
						);
					})}
				</ul>
			)}
		</div>
	);
}
