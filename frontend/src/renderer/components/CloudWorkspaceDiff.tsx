import { useCallback, useMemo } from "react";
import { useCloudCp } from "../hooks/useCloudCp";
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

export function CloudFileContentPane({ annotation, commitSha, initialEditing, initialLine, initialMode, initialRequestKey, onDirtyChange, path, scope, session, split = false }: {
	annotation: FileAnnotationModel;
	commitSha?: string;
	initialEditing?: boolean;
	initialLine?: number;
	initialMode?: CloudFileViewMode;
	initialRequestKey?: number;
	onDirtyChange?: (path: string, dirty: boolean) => void;
	path: string;
	scope?: CloudCpWorkspaceReviewScope;
	session: WorkspaceSession;
	split?: boolean;
}) {
	const { baseUrl, client } = useCloudCp();
	const handleDirtyChange = useCallback((dirty: boolean) => onDirtyChange?.(path, dirty), [onDirtyChange, path]);
	// A sibling repository's file is opened as ../<repository>/<path>; its
	// calls and comments go to that repository.
	const target = splitWorkspaceRepositoryPath(path);
	const scopedClient = withWorkspaceRepository(client, target.repository);
	const scopedAnnotation = useMemo(() => repositoryAnnotationModel(annotation, target.repository), [annotation, target.repository]);
	return <section className="relative flex h-full min-h-0 flex-col bg-background">
		<div className="board-scrollbar min-h-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain">
			<CloudFileContent annotation={scopedAnnotation} baseUrl={baseUrl} client={scopedClient} commitSha={commitSha} initialEditing={initialEditing} initialLine={initialLine} initialMode={initialMode} initialRequestKey={initialRequestKey} onDirtyChange={handleDirtyChange} orgId={session.cloud?.orgId ?? ""} path={target.path} scope={scope} sessionId={session.id} split={split} />
		</div>
	</section>;
}
