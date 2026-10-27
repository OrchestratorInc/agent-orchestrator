import { ArrowUpRight, X } from "lucide-react";
import { useRef } from "react";
import { useTranslation } from "react-i18next";
import { isInlineArtifactUrl } from "../../shared/agent-page-url";
import { useRestoreFocusOnClose } from "../hooks/useRestoreFocusOnClose";
import { useCloudArtifactAccess, usePinnedArtifactUrl } from "../hooks/useCloudArtifacts";
import { useCloudCp } from "../hooks/useCloudCp";
import { artifactContentUrl } from "../lib/artifact-content-url";
import { artifactKey, type ArtifactRow } from "../lib/artifacts";
import { useNavigateToSession } from "../lib/navigate-to-session";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import { useArtifactsViewStore } from "../stores/artifacts-view-store";
import { ArtifactActionsMenu } from "./ArtifactActionsMenu";
import { ArtifactFileView } from "./ArtifactFileView";
import { CloudArtifactFrame } from "./CloudArtifactFrame";
import { PanelMessage, RetryButton } from "./WorkspaceDiffView";
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
								session={{ id: artifact.sessionId, cloud: artifact.cloud }}
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
	const cloud = Boolean(artifact.cloud);
	const access = useCloudArtifactAccess({ id: artifact.sessionId, cloud: artifact.cloud }, cloud);
	const pinned = usePinnedArtifactUrl(cloud ? access.data : undefined, cloud ? artifact.path : undefined, artifact.updatedAt);
	const { client: cloudClient } = useCloudCp(cloud);
	const page = artifact.kind === "html" && artifact.inlineUrl && isInlineArtifactUrl(artifact.inlineUrl) ? artifact.inlineUrl : undefined;
	const contentUrl = artifactContentUrl(artifact.sessionId, artifact.path, artifact.rawUrl);
	if (artifact.cloud) {
		const orgId = artifact.cloud.orgId;
		if (access.isError) return <PanelMessage action={<RetryButton onClick={() => { void access.refetch(); }} />}>{t("artifacts.accessError")}</PanelMessage>;
		if (!pinned.url) return <PanelMessage>{t("files.loading")}</PanelMessage>;
		const accessRefused = () => {
			pinned.repin();
			void access.refetch();
		};
		if (artifact.kind === "html") return <CloudArtifactFrame onAccessRefused={accessRefused} title={artifact.name} url={pinned.url} />;
		return <ArtifactFileView artifactName={artifact.name} artifactRoot={pinned.root} cloud onAccessRefused={accessRefused} path={artifact.path} rawUrl={pinned.url} readOnly sendMessage={async (message) => { await cloudClient.sendSessionMessage(orgId, artifact.sessionId, { text: message }); }} sessionId={artifact.sessionId} />;
	}
	if (page) {
		return <iframe className="h-full w-full border-0 bg-background" sandbox="allow-scripts allow-same-origin" src={page} title={artifact.name} />;
	}
	if (!contentUrl) return <PanelMessage>{t("artifactLink.noPreview")}</PanelMessage>;
	return <ArtifactFileView artifactName={artifact.name} path={artifact.path} rawUrl={artifact.rawUrl} readOnly sessionId={artifact.sessionId} />;
}
