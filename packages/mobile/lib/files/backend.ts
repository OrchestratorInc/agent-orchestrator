import type { FileAnnotationComment } from "../../../shared/files/file-annotations";
import type { ServerConfig } from "../config";
import {
	getWorkspaceFile,
	getWorkspaceManifest,
	getWorkspaceTree,
	searchWorkspaceFiles,
	sendFileFeedback,
	workspaceImageSource,
	type WorkspaceFileDetail,
	type WorkspaceManifest,
	type WorkspaceSearchResult,
	type WorkspaceSection,
	type WorkspaceTree,
} from "./api";

export type WorkspaceImageSource = { uri: string; headers?: Record<string, string> };

/**
 * Where the Files screens read a session's worktree and send review comments:
 * the paired machine's daemon, or Orchestrator Cloud for a cloud session
 * (cloudBackend.ts). Both answer in the daemon's shapes, so the screens are
 * the same screens.
 */
export type FilesBackend = {
	/** Changes when the machine, its credential, or the Cloud account does. */
	key: string;
	cloud: boolean;
	manifest(sessionId: string, signal?: AbortSignal): Promise<WorkspaceManifest>;
	file(sessionId: string, path: string, section?: WorkspaceSection): Promise<WorkspaceFileDetail>;
	tree(sessionId: string, dir: string): Promise<WorkspaceTree>;
	search(sessionId: string, query: string): Promise<{ results: WorkspaceSearchResult[]; truncated: boolean }>;
	/** One side of a changed image; undefined when it cannot be shown. */
	imageSource(sessionId: string, path: string, side: "before" | "after", version: string): Promise<WorkspaceImageSource | undefined>;
	/** Sends queued comments as few messages as fit; resolves with how many reached the agent. */
	sendFeedback(sessionId: string, comments: FileAnnotationComment[], onSent?: (count: number) => void): Promise<number>;
};

export function daemonFilesBackend(config: ServerConfig): FilesBackend {
	return {
		key: JSON.stringify([config.hostId, config.host, config.httpPort, config.secure, config.password]),
		cloud: false,
		manifest: (sessionId, signal) => getWorkspaceManifest(config, sessionId, { signal }),
		file: (sessionId, path, section) => getWorkspaceFile(config, sessionId, path, section),
		tree: (sessionId, dir) => getWorkspaceTree(config, sessionId, dir),
		search: (sessionId, query) => searchWorkspaceFiles(config, sessionId, query),
		imageSource: async (sessionId, path, side, version) => workspaceImageSource(config, sessionId, path, side, version),
		sendFeedback: (sessionId, comments, onSent) => sendFileFeedback(config, sessionId, comments, onSent),
	};
}
