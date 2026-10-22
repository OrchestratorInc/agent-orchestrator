import { useQuery } from "@tanstack/react-query";
import { ArrowUpRight, X } from "lucide-react";
import { useRef } from "react";
import { useTranslation } from "react-i18next";
import { isInlineArtifactUrl } from "../../shared/agent-page-url";
import { useRestoreFocusOnClose } from "../hooks/useRestoreFocusOnClose";
import { sessionArtifactFileQueryOptions } from "../hooks/useSessionWorkspaceFiles";
import { artifactContentUrl } from "../lib/artifact-content-url";
import { artifactKey, type ArtifactRow } from "../lib/artifacts";
import { fileMediaKind } from "../lib/file-media";
import { isSvgPath } from "../lib/image-files";
import { useNavigateToSession } from "../lib/navigate-to-session";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import { useArtifactsViewStore } from "../stores/artifacts-view-store";
import { ArtifactActionsMenu } from "./ArtifactActionsMenu";
import { FileMediaPreview } from "./FileMediaPreview";
import { SvgFileView } from "./SvgFileView";
import { PanelMessage, RetryButton } from "./WorkspaceDiffView";
import { MarkdownFileView } from "./markdown/MarkdownFileView";
import { Button } from "./ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle, settingsDialogContentClass } from "./ui/dialog";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

export function ArtifactPreviewModal({ artifact, projectName, onClose }: {
	artifact: ArtifactRow | null;
	projectName: string;
	onClose: () => void;
}) {
	const { t } = useTranslation();
	const navigateToSession = useNavigateToSession();
	const requestWorkspaceFileOpen = useUiStore((state) => state.requestWorkspaceFileOpen);
	const restoreFocusOnClose = useRestoreFocusOnClose(Boolean(artifact));
	const titleRef = useRef<HTMLHeadingElement>(null);
	const id = artifact ? artifactKey(artifact) : "";
	const pinned = useArtifactsViewStore((state) => state.pinnedArtifactIds.includes(id));
	const togglePinnedArtifact = useArtifactsViewStore((state) => state.togglePinnedArtifact);
	const openInSession = () => {
		if (!artifact) return;
		onClose();
		requestWorkspaceFileOpen(artifact.sessionId, artifact.path, undefined, "artifact");
		navigateToSession(artifact.projectId, artifact.sessionId);
	};
	return (
		<Dialog open={Boolean(artifact)} onOpenChange={(open) => { if (!open) onClose(); }}>
			{artifact ? (
				<DialogContent
					className={cn(settingsDialogContentClass, "h-[min(52rem,calc(100svh_-_var(--size-toolbar)_-_var(--size-toolbar)_-_var(--space-8)))] max-h-none w-[min(72rem,calc(100vw-var(--space-8)))]")}
					onCloseAutoFocus={restoreFocusOnClose}
					onOpenAutoFocus={(event) => {
						event.preventDefault();
						titleRef.current?.focus();
					}}
					showCloseButton={false}
				>
					<header className="flex shrink-0 items-center gap-3 border-b border-border px-4 py-3">
						<div className="min-w-0 flex-1">
							<DialogTitle className="truncate text-heading-sm outline-none" ref={titleRef} tabIndex={-1}>{artifact.name}</DialogTitle>
							<DialogDescription className="truncate text-caption">{projectName} · {artifact.sessionTitle}</DialogDescription>
						</div>
						<div className="relative z-10 flex shrink-0 items-center gap-1">
							<ArtifactActionsMenu
								artifact={artifact}
								session={{ id: artifact.sessionId }}
								iconOnly
								pinInMenu
								pinned={pinned}
								onTogglePin={() => togglePinnedArtifact(id)}
								onDeleted={onClose}
								errorClassName="bottom-auto top-full mb-0 mt-2"
								className="transition-none"
							/>
						</div>
						<Button className="shrink-0 transition-none" onClick={openInSession} size="sm" variant="secondary">
							{t("artifacts.openInSession")}
							<ArrowUpRight aria-hidden="true" className="size-icon-sm" />
						</Button>
						<Tooltip>
							<TooltipTrigger asChild>
								<DialogClose asChild>
									<Button aria-label={t("common.close")} className="shrink-0 transition-none" size="icon-sm" variant="ghost">
										<X aria-hidden="true" className="size-icon-sm" />
									</Button>
								</DialogClose>
							</TooltipTrigger>
							<TooltipContent side="bottom">{t("common.close")}</TooltipContent>
						</Tooltip>
					</header>
					<div className="min-h-0 flex-1 overflow-hidden bg-background">
						<ArtifactPreviewBody artifact={artifact} key={artifactKey(artifact)} />
					</div>
				</DialogContent>
			) : null}
		</Dialog>
	);
}

function ArtifactPreviewBody({ artifact }: { artifact: ArtifactRow }) {
	const { t } = useTranslation();
	const page = artifact.kind === "html" && artifact.inlineUrl && isInlineArtifactUrl(artifact.inlineUrl) ? artifact.inlineUrl : undefined;
	const media = fileMediaKind(artifact.path);
	const contentUrl = artifactContentUrl(artifact.sessionId, artifact.path, artifact.rawUrl);
	const options = sessionArtifactFileQueryOptions(artifact.sessionId, artifact.path, artifact.rawUrl, t("files.error.loadArtifact"));
	const query = useQuery({
		...options,
		queryKey: [...options.queryKey, artifact.updatedAt],
		enabled: Boolean(contentUrl) && !page && !media,
		staleTime: Infinity,
		retry: false,
	});
	if (page) {
		return <iframe className="h-full w-full border-0 bg-background" sandbox="allow-scripts allow-same-origin" src={page} title={artifact.name} />;
	}
	if (media) {
		return <div className="flex h-full min-h-0 flex-col bg-[var(--solid-muted,var(--muted))]"><FileMediaPreview kind={media} path={artifact.path} sessionId={artifact.sessionId} source={{ kind: "artifact", rawUrl: artifact.rawUrl, readOnly: true }} version={artifact.updatedAt} /></div>;
	}
	if (!contentUrl) return <PanelMessage>{t("artifactLink.noPreview")}</PanelMessage>;
	if (query.isPending) return <div role="status"><PanelMessage>{t("files.loading")}</PanelMessage></div>;
	if (query.isError) return <div role="alert"><PanelMessage action={<RetryButton onClick={() => { void query.refetch(); }} />}>{query.error.message}</PanelMessage></div>;
	if (!query.data || query.data.binary) return <PanelMessage>{t("files.binaryUnavailable")}</PanelMessage>;
	const detail = query.data;
	const markdown = artifact.kind === "markdown" || /\.(md|markdown)$/i.test(artifact.path);
	return (
		<div className="board-scrollbar h-full min-h-0 select-text overflow-auto overscroll-contain">
			{markdown ? (
				<MarkdownFileView artifactOrigin={new URL(artifact.rawUrl ?? contentUrl).origin} content={detail.content} filePath={artifact.path} sessionId={artifact.sessionId} truncated={detail.contentTruncated} version={query.dataUpdatedAt} />
			) : (
				<>
					{detail.contentTruncated ? <p className="px-4 py-2 text-caption text-warning">{t("files.contentTruncated")}</p> : null}
					{isSvgPath(artifact.path) ? <SvgFileView content={detail.content} path={artifact.path} /> : <pre className="whitespace-pre-wrap break-words p-4 font-mono text-control text-foreground">{detail.content}</pre>}
				</>
			)}
		</div>
	);
}
