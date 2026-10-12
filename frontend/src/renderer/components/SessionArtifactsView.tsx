import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { ChevronLeft, Globe, Shapes } from "lucide-react";
import { useOpenArtifactPreview } from "../hooks/useOpenArtifactPreview";
import { formatTimeTerse } from "../lib/format-time";
import { sessionUiKey } from "../lib/hosts";
import type { SessionArtifact, WorkspaceSession } from "../types/workspace";
import { ArtifactFileView } from "./ArtifactFileView";
import { Button } from "./ui/button";
import { PanelMessage } from "./WorkspaceDiffView";

export type ArtifactOpenRequest = { feedback?: boolean; key: number; path: string };

/** The session's artifacts, newest first. */
function sortedArtifacts(artifacts: SessionArtifact[]): SessionArtifact[] {
	return [...artifacts].sort(
		(a, b) => (Date.parse(b.updatedAt) || 0) - (Date.parse(a.updatedAt) || 0) || a.path.localeCompare(b.path),
	);
}

/**
 * The inspector's Artifacts view (opened from the three-dot menu): every file in
 * the session's artifact directory. HTML artifacts open in the Browser panel;
 * markdown and other files open in a read-only viewer beside the list.
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
				<div className="flex h-8 shrink-0 items-center gap-1 border-b border-border bg-surface px-1">
					<Button
						aria-label={t("artifacts.back")}
						onClick={() => setSelectedPath(null)}
						size="icon-sm"
						type="button"
						variant="ghost"
					>
						<ChevronLeft aria-hidden="true" className="size-icon-sm" />
					</Button>
					<span className="min-w-0 flex-1 truncate text-xs text-muted-foreground" title={selected.path}>{selected.name}</span>
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
					/>
				</div>
			</div>
		);
	}

	return (
		<div className="flex h-full min-h-0 flex-col" role="tabpanel">
			<div className="flex h-8 shrink-0 items-center px-3 text-sm text-muted-foreground">{t("artifacts.title")}</div>
			{artifacts.length === 0 ? (
				<PanelMessage>{t("artifacts.sessionEmpty")}</PanelMessage>
			) : (
				<ul className="board-scrollbar flex min-h-0 flex-1 flex-col gap-1.5 overflow-y-auto px-3 pb-3 pt-1">
					{artifacts.map((artifact) => {
						const html = artifact.kind === "html" && Boolean(artifact.previewUrl);
						const Icon = html ? Globe : Shapes;
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
