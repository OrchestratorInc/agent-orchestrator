import type { FileAnnotationComment } from "../../../shared/files/file-annotations";
import type { ServerConfig } from "../config";
import {
	getWorkspaceBranches,
	getWorkspaceFile,
	getWorkspaceHistory,
	getWorkspaceManifest,
	getWorkspaceRangeFiles,
	getWorkspaceTree,
	searchWorkspaceFiles,
	sendFileFeedback,
	workspaceImageSource,
	type FileRead,
	type WorkspaceBranches,
	type WorkspaceCommitSelection,
	type WorkspaceFileDetail,
	type WorkspaceHistory,
	type WorkspaceManifest,
	type WorkspaceSearchResult,
	type WorkspaceTree,
	type WorkspaceView,
} from "./api";
import { imageComparable } from "./selection";

export type WorkspaceImageSource = { uri: string; headers?: Record<string, string> };

/**
 * Where the Files screens read a session's worktree and send review comments:
 * the paired machine's daemon, or Orchestrator Cloud for a cloud session
 * (cloudBackend.ts). Both answer in the daemon's shapes, so the screens are
 * the same screens.
 */
export type FilesBackend = {
	/** Changes when the machine, its credential, the Cloud account, or the view does. */
	key: string;
	cloud: boolean;
	/** Set on a backend that reads another branch or a pull request instead of the worktree. */
	view?: WorkspaceView;
	manifest(sessionId: string, signal?: AbortSignal): Promise<WorkspaceManifest>;
	file(sessionId: string, path: string, read?: FileRead): Promise<WorkspaceFileDetail>;
	tree(sessionId: string, dir: string): Promise<WorkspaceTree>;
	search(sessionId: string, query: string): Promise<{ results: WorkspaceSearchResult[]; truncated: boolean }>;
	/** One side of a changed image; undefined when it cannot be shown. */
	imageSource(sessionId: string, path: string, side: "before" | "after", version: string, read?: FileRead): Promise<WorkspaceImageSource | undefined>;
	/** Sends queued comments as few messages as fit; resolves with how many reached the agent. */
	sendFeedback(sessionId: string, comments: FileAnnotationComment[], onSent?: (count: number) => void): Promise<number>;
	/** Branches, commits and other views. Absent where only the worktree can be read (Orchestrator Cloud). */
	git?: FilesGit;
};

export type FilesGit = {
	branches(sessionId: string, signal?: AbortSignal): Promise<WorkspaceBranches>;
	/** The commits of this backend's view, or of the checked-out branch. */
	history(sessionId: string, signal?: AbortSignal): Promise<WorkspaceHistory>;
	/** What this view's whole range of commits changed. */
	rangeFiles(sessionId: string, signal?: AbortSignal): Promise<WorkspaceCommitSelection>;
	/** The same machine reading `view`, or its worktree when undefined. */
	at(view: WorkspaceView | undefined): FilesBackend;
};

export function daemonFilesBackend(config: ServerConfig, view?: WorkspaceView): FilesBackend {
	const machine = JSON.stringify([config.hostId, config.host, config.httpPort, config.secure, config.password]);
	return {
		key: view ? `${machine}|${JSON.stringify(view)}` : machine,
		cloud: false,
		...(view ? { view } : {}),
		manifest: (sessionId, signal) => getWorkspaceManifest(config, sessionId, { signal }),
		file: (sessionId, path, read) => getWorkspaceFile(config, sessionId, path, read, view),
		tree: (sessionId, dir) => getWorkspaceTree(config, sessionId, dir, view),
		search: (sessionId, query) => searchWorkspaceFiles(config, sessionId, query, view),
		// A worktree scope's images would be the base and the worktree, not the scope's sides.
		imageSource: async (sessionId, path, side, version, read) => imageComparable(read ?? {}) ? workspaceImageSource(config, sessionId, path, side, version, read, view) : undefined,
		sendFeedback: (sessionId, comments, onSent) => sendFileFeedback(config, sessionId, comments, onSent),
		git: {
			branches: (sessionId, signal) => getWorkspaceBranches(config, sessionId, { signal }),
			history: (sessionId, signal) => getWorkspaceHistory(config, sessionId, view, { signal }),
			rangeFiles: async (sessionId, signal) => {
				if (!view) throw new Error("The checked-out branch's commits are in its manifest.");
				return getWorkspaceRangeFiles(config, sessionId, view, { signal });
			},
			at: (next) => daemonFilesBackend(config, next),
		},
	};
}
