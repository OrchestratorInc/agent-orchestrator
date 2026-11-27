import { useCallback, useMemo } from "react";
import type { FileAnnotationRange } from "../../shared/chat-file-reference";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudWorkspaceReady } from "../hooks/useCloudWorkspaceReady";
import { cloudWorkspaceView } from "../hooks/useCloudWorkspaceReview";
import { useFileAnnotation } from "../hooks/useFileAnnotation";
import type { FilesSource } from "../hooks/useSessionWorkspaceFiles";
import { snapshotAnnotationSource } from "../lib/files-review-selection";
import { splitWorkspaceRepositoryPath, withWorkspaceRepository, type CloudCpWorkspaceReviewScope } from "../lib/cloud-cp";
import type { WorkspaceSession } from "../types/workspace";
import type { FileAnnotationModel } from "./WorkspaceDiffView";
import { CloudFileContentPane as CloudFileContent } from "./cloud-workspace/CloudFileContentPane";
import type { CloudFileViewMode } from "./cloud-workspace/CloudFileContentPane";
import { CloudWorkspaceExplorer, type CloudFileOpenOptions } from "./cloud-workspace/CloudWorkspaceExplorer";
import { repositoryAnnotationModel } from "./cloud-workspace/repositoryAnnotation";

export function CloudWorkspaceDiff({ annotation, session, isMaximized = false, onOpenFile, onSplitChange, onToggleMaximized, split }: {
	annotation?: FileAnnotationModel;
	session: WorkspaceSession;
	isMaximized?: boolean;
	onOpenFile?: (path: string, options?: CloudFileOpenOptions) => void;
	onSplitChange?: (split: boolean) => void;
	onToggleMaximized?: (next: boolean) => void;
	split?: boolean;
}) {
	return <CloudWorkspaceExplorer annotation={annotation} isMaximized={isMaximized} onOpenFile={onOpenFile} onSplitChange={onSplitChange} onToggleMaximized={onToggleMaximized} session={session} split={split} />;
}

export function CloudFileContentPane({ active, annotation, annotationRange, commitSha, initialEditing, initialLine, initialMode, initialRequestKey, onDirtyChange, path, scope, session, source, split = false }: {
	/** False while the view is kept mounted behind another tab. */
	active?: boolean;
	annotation: FileAnnotationModel;
	/** A chat reference's lines to reveal and highlight. */
	annotationRange?: FileAnnotationRange;
	commitSha?: string;
	initialEditing?: boolean;
	initialLine?: number;
	initialMode?: CloudFileViewMode;
	initialRequestKey?: number;
	onDirtyChange?: (path: string, dirty: boolean) => void;
	path: string;
	scope?: CloudCpWorkspaceReviewScope;
	session: WorkspaceSession;
	/** A branch's or PR's commits (a snapshot) instead of the worktree. */
	source?: FilesSource;
	split?: boolean;
}) {
	const { baseUrl, client } = useCloudCp();
	const orgId = session.cloud?.orgId ?? "";
	const snapshot = source?.kind === "snapshot" ? source : undefined;
	const view = useMemo(() => (snapshot ? cloudWorkspaceView(snapshot.view, snapshot.commitShas ?? []) : undefined), [snapshot]);
	// Comments on a branch's or PR's file tell the agent which revision they are about.
	const sendSnapshotAnnotation = useCallback(async (message: string) => { await client.sendSessionMessage(orgId, session.id, { text: message }); }, [client, orgId, session.id]);
	const snapshotAnnotation = useFileAnnotation(session.id, { source: snapshot ? snapshotAnnotationSource(snapshot) : "Workspace", sendMessage: sendSnapshotAnnotation });
	const paneAnnotation = snapshot ? snapshotAnnotation : annotation;
	// A waking or starting session's file waits until its workspace answers;
	// what was read before stays on screen meanwhile.
	const ready = useCloudWorkspaceReady(session);
	const handleDirtyChange = useCallback((dirty: boolean) => onDirtyChange?.(path, dirty), [onDirtyChange, path]);
	// A sibling repository's file is opened as ../<repository>/<path>; its
	// calls and comments go to that repository.
	const target = splitWorkspaceRepositoryPath(path);
	const scopedClient = withWorkspaceRepository(client, target.repository);
	const scopedAnnotation = useMemo(() => repositoryAnnotationModel(paneAnnotation, target.repository), [paneAnnotation, target.repository]);
	return <section className="relative flex h-full min-h-0 flex-col bg-background">
		<div className="board-scrollbar min-h-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain">
			<CloudFileContent active={active} annotation={scopedAnnotation} annotationRange={annotationRange} baseUrl={baseUrl} chatReferences={target.repository === undefined} client={scopedClient} commitSha={commitSha} initialEditing={initialEditing} initialLine={initialLine} initialMode={initialMode} initialRequestKey={initialRequestKey} onDirtyChange={handleDirtyChange} orgId={orgId} path={target.path} ready={ready} scope={scope} sessionId={session.id} source={source} split={split} view={view} />
		</div>
	</section>;
}
