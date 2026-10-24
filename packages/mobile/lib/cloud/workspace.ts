import { CloudApiError, type CloudClient, type WorkspaceReviewStatus } from "@aoagents/cloud-client";
import { formatFileAnnotationMessages } from "../../../shared/files/file-annotations";
import type { WorkspaceFileStatus } from "../files/api";
import type { WorkspaceSource } from "../files/workspaceSource";
import { throwIfCloudRequestAborted } from "./cancellation";
import { cloudRequestErrorCopy } from "./errorCopy";

function status(value: WorkspaceReviewStatus): WorkspaceFileStatus {
	return value === "copied" || value === "untracked" ? "added" : value;
}

/** Existing desktop review routes, normalized for the shared mobile Files UI. */
export function createCloudWorkspaceSource(input: { client: CloudClient; orgId: string; scopeKey: string; isCurrent: () => boolean; repository?: string }, sources = new Map<string, WorkspaceSource>()): WorkspaceSource {
	const { client, orgId, isCurrent, repository } = input;
	const scopeKey = repository ? JSON.stringify([input.scopeKey, "repository", repository]) : input.scopeKey;
	const repositoryQuery = repository ? { repository } : {};
	const pendingBatches = new Map<string, { sessionId: string; comments: string[]; targets: string[]; message: string; key: string }>();
	const requireCurrent = () => { if (!isCurrent()) throw new Error("Cloud account changed. Open Files from your current worker."); };
	async function read<T>(run: () => Promise<T>, signal?: AbortSignal): Promise<T> {
		requireCurrent(); throwIfCloudRequestAborted(signal);
		try {
			const value = await run();
			requireCurrent(); throwIfCloudRequestAborted(signal);
			return value;
		} catch (cause) {
			requireCurrent(); throwIfCloudRequestAborted(signal);
			if (cause instanceof CloudApiError) {
				if (cause.code === "WORKER_UNAVAILABLE") throw new Error("Files are available when this Cloud worker is connected. Return to the session to check its status or resume it.");
				if (cause.status === 401) throw new Error("Sign in to Orchestrator Cloud again to open these files.");
				if (cause.status === 403) throw new Error("You no longer have access to this Cloud workspace. Open a worker from your current board.");
				throw new Error(cloudRequestErrorCopy(cause, { includeReference: false }));
			}
			if (cause instanceof TypeError) throw new Error("Couldn't connect to Cloud. Check your connection and try again.");
			throw cause;
		}
	}
	const source: WorkspaceSource = {
		scopeKey, isCurrent,
		forRepository: (name) => {
			if (name && (name === "." || name === ".." || /[/\\\0]/.test(name) || !name.trim())) throw new Error("Invalid Cloud repository name.");
			return sources.get(name || "") ?? createCloudWorkspaceSource({ ...input, repository: name || undefined }, sources);
		},
		getManifest: async (id, options) => {
			// The worker attaches the repository catalog only to its main summary.
			// Read it alongside a sibling so direct links and switching back work.
			const [wire, main] = await read(() => Promise.all([
				client.getWorkspaceReview(orgId, id, repository ? { ...options, repository } : options),
				repository ? client.getWorkspaceReview(orgId, id, options) : Promise.resolve(undefined),
			]), options?.signal);
			const mapFile = <T extends { status: WorkspaceReviewStatus }>(file: T) => ({ ...file, status: status(file.status) });
			return {
				sessionId: id, workspaceVersion: wire.workspaceVersion,
				// Desktop's review includes the whole inventory, whereas mobile's
				// manifest lists changes only. All files are browsed through tree/search.
				files: wire.files.filter((file) => file.status !== "unmodified").map(mapFile),
				sections: {
					committed: wire.sections.committed.map(mapFile), staged: wire.sections.staged.map(mapFile),
					unstaged: wire.sections.unstaged.map(mapFile), untracked: wire.sections.untracked.map(mapFile),
				},
				summary: wire.summary, truncated: wire.truncated, compareBaseRef: wire.compareBaseRef,
				compareMode: wire.compareMode, ahead: wire.ahead, behind: wire.behind, degraded: false, repositories: main?.repositories ?? wire.repositories,
			};
		},
		getFile: async (id, path, section, options) => {
			let wire = await read(() => client.getWorkspaceReviewFile(orgId, id, { path, scope: section ?? "combined", ...repositoryQuery }, options), options?.signal);
			// Git's combined diff excludes new untracked files. The untracked
			// scope synthesizes their added patch, including on direct file links.
			if (!section && wire.status === "untracked") wire = await read(() => client.getWorkspaceReviewFile(orgId, id, { path, scope: "untracked", ...repositoryQuery }, options), options?.signal);
			// Current Cloud workers have no binary image revision transport. Match
			// desktop's honest binary fallback, never a local credential-bearing URL.
			return { ...wire, status: status(wire.status), imageMediaType: undefined };
		},
		getTree: async (id, path = "", options) => {
			const wire = await read(() => client.getWorkspaceReviewTree(orgId, id, { path, ...repositoryQuery }, options), options?.signal);
			return { ...wire, entries: wire.entries.map((entry) => ({ ...entry, status: entry.status ? status(entry.status) : undefined })) };
		},
		search: async (id, query, options) => {
			const wire = await read(() => client.searchWorkspaceReview(orgId, id, { query, limit: 50, ...repositoryQuery }, options), options?.signal);
			return { results: wire.results.map((file) => ({ ...file, status: status(file.status) })), truncated: wire.truncated || Boolean(wire.nextCursor) };
		},
		sendFeedback: async (id, comments, onSent) => {
			requireCurrent();
			const serialized = comments.map((comment) => JSON.stringify(comment));
			const targets = comments.map((comment) => JSON.stringify(comment.target));
			const requestedTargets = new Set(targets);
			for (const batch of pendingBatches.values()) {
				if (batch.sessionId !== id || !batch.targets.some((target) => requestedTargets.has(target))) continue;
				// Appending comments is safe: first recover the original immutable
				// batch, then send the additions. A changed/partly removed uncertain
				// batch must not silently be regrouped and duplicated under a new key.
				if (!batch.comments.every((comment, index) => serialized[index] === comment)) throw new Error("An earlier review's delivery is unconfirmed. Check the conversation before resending changed comments. Your current review is still here.");
			}
			const rootedComments = repository ? comments.map((comment) => ({ ...comment, target: {
				...comment.target, path: `../${repository}/${comment.target.path}`,
				...(comment.target.previousPath ? { previousPath: `../${repository}/${comment.target.previousPath}` } : {}),
			} })) : comments;
			let sent = 0;
			while (sent < comments.length) {
				requireCurrent();
				const remaining = serialized.slice(sent);
				const pending = [...pendingBatches.entries()].find(([, batch]) => batch.sessionId === id && batch.comments.every((comment, index) => remaining[index] === comment));
				const formatted = formatFileAnnotationMessages(rootedComments.slice(sent))[0];
				const count = pending?.[1].comments.length ?? formatted.count;
				// Retain the exact batch and its key after an ambiguous failure. A
				// retry may contain only the remaining comments after a partial send.
				const fingerprint = pending?.[0] ?? JSON.stringify([id, comments.slice(sent, sent + count)]);
				const batch = pending?.[1] ?? {
					sessionId: id, comments: serialized.slice(sent, sent + count), targets: targets.slice(sent, sent + count),
					message: formatted.message, key: `mobile-files-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 14)}`,
				};
				// Sending a message can wake a paused sandbox on the backend. Do not
				// turn a retained Files screen into an implicit Resume action.
				const { session } = await read(() => client.getSession(orgId, id));
				if (session.desiredState !== "running" || !session.runtimeConnected || session.isTerminated) throw new Error("Return to this Cloud session and resume or reconnect it before sending the review. Your comments are still here.");
				pendingBatches.set(fingerprint, batch);
				await read(() => client.sendMessage(orgId, id, batch.message, { idempotencyKey: batch.key }));
				pendingBatches.delete(fingerprint);
				sent += count;
				onSent?.(count);
			}
			return sent;
		},
	};
	sources.set(repository || "", source);
	return source;
}
