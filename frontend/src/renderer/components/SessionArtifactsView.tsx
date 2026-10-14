import { useEffect, useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { ChevronDown, ChevronLeft, Copy, FileText, FolderOpen, Globe, SquareTerminal, Trash2 } from "lucide-react";
import type { OpenArtifactsTargetId } from "../../shared/editor-handoff";
import { useOpenSessionArtifacts } from "../hooks/useEditorHandoff";
import { useOpenArtifactPreview } from "../hooks/useOpenArtifactPreview";
import { deleteSessionArtifactFile, sessionArtifactFileQueryOptions } from "../hooks/useSessionWorkspaceFiles";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { aoBridge } from "../lib/bridge";
import { isMacPlatform, isWindowsPlatform } from "../lib/platform";
import { formatTimeTerse } from "../lib/format-time";
import { sessionUiKey } from "../lib/hosts";
import type { SessionArtifact, WorkspaceSession } from "../types/workspace";
import { ArtifactFileView } from "./ArtifactFileView";
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

/**
 * The inspector's Artifacts view (its own inspector tab): every file in
 * the session's artifact directory. HTML artifacts open in the Browser panel;
 * markdown and other files open in a viewer in place of the list.
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
	const openArtifactPreview = useOpenArtifactPreview(session.id, hostId);
	const uiKey = sessionUiKey(session.id, hostId);

	useEffect(() => setSelectedPath(null), [uiKey]);
	useEffect(() => {
		if (request) setSelectedPath(request.path);
	}, [request]);

	const selected = artifacts.find((artifact) => artifact.path === selectedPath);
	if (selected) {
		return (
			<div className="flex h-full min-h-0 flex-col" role="tabpanel">
				{/* One header row: back, the file's menu, and the viewer's mode and action buttons. */}
				<div className="flex min-h-9 shrink-0 items-center gap-1 border-b border-border bg-background px-1">
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
				</div>
				<div className="min-h-0 flex-1">
					<ArtifactFileView
						artifactName={selected.name}
						feedbackRequestKey={request?.feedback && request.path === selected.path ? request.key : undefined}
						hostId={hostId}
						onFeedbackRequestConsumed={onRequestConsumed}
						path={selected.path}
						rawUrl={selected.rawUrl}
						sessionId={session.id}
						toolbarHost={toolbarHost}
					/>
				</div>
			</div>
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
