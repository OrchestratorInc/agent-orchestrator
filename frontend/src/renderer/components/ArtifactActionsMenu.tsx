import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { ArrowUpRight, ChevronDown, Copy, Download, Ellipsis, FolderOpen, Pin, PinOff, SquareTerminal, Trash2 } from "lucide-react";
import type { OpenArtifactsTargetId } from "../../shared/editor-handoff";
import { useOpenSessionArtifacts } from "../hooks/useEditorHandoff";
import { CLOUD_TEXT_ARTIFACT, deleteSessionArtifactFile, sessionArtifactFileQueryOptions } from "../hooks/useSessionWorkspaceFiles";
import { cloudSessionsQueryKey, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { useCloudCp } from "../hooks/useCloudCp";
import { cloudArtifactFileUrl, useCloudArtifactAccess } from "../hooks/useCloudArtifacts";
import { aoBridge } from "../lib/bridge";
import { artifactKey } from "../lib/artifacts";
import { fileMediaKind } from "../lib/file-media";
import { isMacPlatform, isWindowsPlatform } from "../lib/platform";
import { cn } from "../lib/utils";
import { useArtifactsViewStore } from "../stores/artifacts-view-store";
import type { SessionArtifact, WorkspaceSession } from "../types/workspace";
import { ConfirmDialog } from "./ConfirmDialog";
import { Button } from "./ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "./ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

const ERROR_CLEAR_MS = 5_000;

function fileManagerName(): string {
	return isMacPlatform() ? "Finder" : isWindowsPlatform() ? "File Explorer" : "File Manager";
}

function terminalName(): string {
	return isWindowsPlatform() ? "Command Prompt" : "Terminal";
}

/**
 * The open artifact's name, as a menu: show it in the file manager or a
 * terminal (local sessions only), copy its contents, or delete it.
 */
export function ArtifactActionsMenu({
	artifact,
	downloadUrl,
	hostId,
	session,
	iconOnly = false,
	pinInMenu = false,
	pinned = false,
	onTogglePin,
	onOpenInSession,
	onDeleted,
	errorClassName,
	className,
}: {
	artifact: Pick<SessionArtifact, "name" | "path" | "rawUrl" | "updatedAt">;
	downloadUrl?: string;
	hostId?: string;
	session: Pick<WorkspaceSession, "id" | "cloud">;
	iconOnly?: boolean;
	pinInMenu?: boolean;
	pinned?: boolean;
	onTogglePin?: () => void;
	onOpenInSession?: () => void;
	onDeleted?: () => void;
	errorClassName?: string;
	className?: string;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const open = useOpenSessionArtifacts();
	const [copyError, setCopyError] = useState("");
	const [confirmingDelete, setConfirmingDelete] = useState(false);
	// The artifact directory is on this machine only for a local session.
	const local = !hostId && !session.cloud;
	const { client: cloudClient } = useCloudCp(Boolean(session.cloud));
	const access = useCloudArtifactAccess(session, Boolean(session.cloud) && !downloadUrl);
	const cloudFileUrl = session.cloud
		? downloadUrl ?? (access.data ? cloudArtifactFileUrl(access.data, artifact.path, artifact.updatedAt) : undefined)
		: undefined;
	// The deleted file leaves the session's artifact list on the next read.
	const remove = useMutation({
		mutationFn: () => session.cloud
			? cloudClient.deleteSessionArtifact(session.cloud.orgId, session.id, artifact.path)
			: deleteSessionArtifactFile({ path: artifact.path, sessionId: session.id, hostId }),
		onSuccess: async () => {
			setConfirmingDelete(false);
			if (!hostId) useArtifactsViewStore.getState().unpinArtifact(artifactKey({ sessionId: session.id, path: artifact.path }));
			onDeleted?.();
			await queryClient.invalidateQueries({ queryKey: session.cloud ? cloudSessionsQueryKey : workspaceQueryKeyForHost(hostId) });
		},
	});
	const markdown = /\.(md|markdown)$/i.test(artifact.path);
	const confirmDelete = () => {
		remove.reset();
		setConfirmingDelete(true);
	};
	const reveal = (targetId: OpenArtifactsTargetId) => {
		setCopyError("");
		open.mutate({ sessionId: session.id, targetId, path: artifact.path });
	};
	const copy = async () => {
		open.reset();
		setCopyError("");
		try {
			const detail = await queryClient.fetchQuery(sessionArtifactFileQueryOptions(session.id, artifact.path, artifact.rawUrl ?? cloudFileUrl, t("files.error.loadArtifact"), Boolean(session.cloud)));
			await aoBridge.clipboard.writeText(detail.content);
		} catch (error) {
			setCopyError(error instanceof Error ? error.message : String(error));
		}
	};
	const download = async () => {
		open.reset();
		setCopyError("");
		try {
			if (!cloudFileUrl) throw new Error(t("artifacts.accessError"));
			const bytes = await aoBridge.cloudArtifact.fetch(cloudFileUrl);
			await aoBridge.chatImage.save({ name: artifact.name, bytes });
		} catch (error) {
			setCopyError(error instanceof Error ? error.message : String(error));
		}
	};
	const copyable = !fileMediaKind(artifact.path) && (!session.cloud || CLOUD_TEXT_ARTIFACT.test(artifact.path));

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
	const menuTrigger = (
		<DropdownMenuTrigger asChild>
			<Button
				aria-label={iconOnly ? `${t("files.moreFileActions")}: ${artifact.name}` : undefined}
				className={cn(
					iconOnly
						? "text-muted-foreground hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/60"
						: "min-w-0 gap-1 px-1.5 font-normal",
					className,
				)}
				size={iconOnly ? "icon-sm" : "sm"}
				title={iconOnly ? undefined : artifact.path}
				type="button"
				variant="ghost"
			>
				{iconOnly ? <Ellipsis aria-hidden="true" className="size-icon-sm" /> : (
					<>
						<span className="truncate">{artifact.name}</span>
						<ChevronDown aria-hidden="true" className="size-icon-xs shrink-0 text-muted-foreground" />
					</>
				)}
			</Button>
		</DropdownMenuTrigger>
	);
	return (
		<>
			{onTogglePin && !pinInMenu ? (
				<Tooltip>
					<TooltipTrigger asChild>
						<Button
							aria-label={`${t(pinned ? "artifacts.unpin" : "artifacts.pin")}: ${artifact.name}`}
							aria-pressed={pinned}
							className={cn("text-muted-foreground hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/60", className, pinned && "text-foreground")}
							onClick={onTogglePin}
							size="icon-sm"
							type="button"
							variant="ghost"
						>
							{pinned ? <PinOff aria-hidden="true" className="size-icon-sm" /> : <Pin aria-hidden="true" className="size-icon-sm" />}
						</Button>
					</TooltipTrigger>
					<TooltipContent side="bottom">{t(pinned ? "artifacts.unpin" : "artifacts.pin")}</TooltipContent>
				</Tooltip>
			) : null}
			{onOpenInSession ? (
				<Tooltip>
					<TooltipTrigger asChild>
						<Button
							aria-label={`${t("artifacts.openInSession")}: ${artifact.name}`}
							className={cn("text-muted-foreground hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/60", className)}
							onClick={onOpenInSession}
							size="icon-sm"
							type="button"
							variant="ghost"
						>
							<ArrowUpRight aria-hidden="true" className="size-icon-sm" />
						</Button>
					</TooltipTrigger>
					<TooltipContent side="bottom">{t("artifacts.openInSession")}</TooltipContent>
				</Tooltip>
			) : null}
			<DropdownMenu>
				{iconOnly ? (
					<Tooltip>
						<TooltipTrigger asChild>
							<span className="inline-flex">{menuTrigger}</span>
						</TooltipTrigger>
						<TooltipContent side="bottom">{t("files.moreFileActions")}</TooltipContent>
					</Tooltip>
				) : menuTrigger}
				<DropdownMenuContent align={iconOnly ? "end" : "start"} className="min-w-44" data-browser-native-overlay="true">
					{onTogglePin || onOpenInSession ? (
						<>
							{onTogglePin ? (
								<DropdownMenuItem onSelect={onTogglePin}>
									{pinned ? <PinOff aria-hidden="true" /> : <Pin aria-hidden="true" />}
									{t(pinned ? "artifacts.unpin" : "artifacts.pin")}
								</DropdownMenuItem>
							) : null}
							{onOpenInSession ? (
								<DropdownMenuItem onSelect={onOpenInSession}>
									<ArrowUpRight aria-hidden="true" />
									{t("artifacts.openInSession")}
								</DropdownMenuItem>
							) : null}
							<DropdownMenuSeparator />
						</>
					) : null}
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
							{copyable || session.cloud ? <DropdownMenuSeparator /> : null}
						</>
					) : null}
					{copyable ? (
						<DropdownMenuItem onSelect={() => void copy()}>
							<Copy aria-hidden="true" />
							{t(markdown ? "artifacts.copyMarkdown" : "artifacts.copyContents")}
						</DropdownMenuItem>
					) : null}
					{session.cloud ? (
						<DropdownMenuItem onSelect={() => void download()}>
							<Download aria-hidden="true" />
							{t("artifacts.download")}
						</DropdownMenuItem>
					) : null}
					{local || copyable || session.cloud ? <DropdownMenuSeparator /> : null}
					<DropdownMenuItem
						className="text-destructive focus:text-destructive [&_svg]:text-destructive focus:[&_svg]:text-destructive"
						onSelect={confirmDelete}
					>
						<Trash2 aria-hidden="true" />
						{t("artifacts.delete")}
					</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>
			<ConfirmDialog
				busy={remove.isPending}
				confirmLabel={t("artifacts.delete")}
				description={t(session.cloud ? "artifacts.deleteCloudDescription" : "artifacts.deleteDescription", { name: artifact.name })}
				destructive
				error={remove.error?.message ?? null}
				onConfirm={() => remove.mutate()}
				onOpenChange={setConfirmingDelete}
				open={confirmingDelete}
				title={t("artifacts.deleteTitle")}
			/>
			{error ? (
				<span className={cn(
					"text-caption text-destructive",
					iconOnly
						? "absolute bottom-full right-0 mb-2 w-64 max-w-[calc(100vw-2rem)] rounded-md bg-popover p-2 shadow-md"
						: "min-w-0 truncate",
					errorClassName,
				)} role="alert" title={error}>
					{error}
				</span>
			) : null}
		</>
	);
}

