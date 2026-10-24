import type { FileAnnotationComment } from "../../../shared/files/file-annotations";
import type { WorkspaceFileDetail, WorkspaceManifest, WorkspaceSearchResult, WorkspaceSection, WorkspaceTree } from "./api";

export type WorkspaceRequestOptions = { signal?: AbortSignal };
export type WorkspaceImageSource = { uri: string; headers: Record<string, string> };

/** The Files surface's operations, bound to one machine or authenticated account. */
export interface WorkspaceSource {
	readonly scopeKey: string;
	isCurrent(): boolean;
	forRepository?(name?: string): WorkspaceSource;
	getManifest(sessionId: string, options?: WorkspaceRequestOptions): Promise<WorkspaceManifest>;
	getFile(sessionId: string, path: string, section?: WorkspaceSection, options?: WorkspaceRequestOptions): Promise<WorkspaceFileDetail>;
	getTree(sessionId: string, path?: string, options?: WorkspaceRequestOptions): Promise<WorkspaceTree>;
	search(sessionId: string, query: string, options?: WorkspaceRequestOptions): Promise<{ results: WorkspaceSearchResult[]; truncated: boolean }>;
	imageSource?(sessionId: string, path: string, side: "before" | "after", version: string): WorkspaceImageSource;
	sendFeedback(sessionId: string, comments: FileAnnotationComment[], onSent?: (count: number) => void): Promise<number>;
}
