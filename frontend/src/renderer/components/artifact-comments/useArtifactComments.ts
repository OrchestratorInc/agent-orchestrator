import { useCallback, useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCloudCp } from "../../hooks/useCloudCp";
import { CloudCpError } from "../../lib/cloud-cp";
import {
	ARTIFACT_COMMENTS_POLL_INTERVAL_MS,
	type ArtifactComment,
	type ArtifactCommentAnchor,
	type ArtifactCommentsSnapshot,
	type ArtifactCommentThread,
	type ArtifactCommentThreadStatus,
} from "../../../shared/artifact-comments";

/** How often an open panel asks whether anything changed. */
export const ARTIFACT_COMMENTS_POLL_MS = ARTIFACT_COMMENTS_POLL_INTERVAL_MS;

type Cached = { snapshot: ArtifactCommentsSnapshot; etag: string | null };

export const artifactCommentsQueryKey = (baseUrl: string, userId: string, slug: string) =>
	["artifact-share-comments", baseUrl, userId, slug] as const;

/** A write shown before the control plane has it. */
type PendingWrite =
	| { kind: "thread"; clientId: string; anchor: ArtifactCommentAnchor; body: string; at: string }
	| { kind: "reply"; clientId: string; threadId: string; body: string; at: string }
	| { kind: "status"; clientId: string; threadId: string; status: ArtifactCommentThreadStatus };

/** Marks a comment or thread that only exists on this screen so far. */
export const PENDING_ID_PREFIX = "pending:";

export function isPendingId(id: string): boolean {
	return id.startsWith(PENDING_ID_PREFIX);
}

function withPending(snapshot: ArtifactCommentsSnapshot, pending: readonly PendingWrite[]): ArtifactCommentsSnapshot {
	if (pending.length === 0) return snapshot;
	const you = { id: snapshot.you.userId, name: "" };
	let threads = snapshot.threads;
	for (const write of pending) {
		if (write.kind === "thread") {
			const threadId = PENDING_ID_PREFIX + write.clientId;
			const comment: ArtifactComment = { id: threadId, threadId, author: you, clientId: write.clientId, body: write.body, createdAt: write.at, editedAt: null, deleted: false };
			threads = [...threads, {
				id: threadId, version: snapshot.version, status: "open", anchor: write.anchor, createdAt: write.at,
				resolvedBy: null, resolvedAt: null, sentToAgentAt: null, sentToAgentCount: 0, comments: [comment],
			}];
		} else if (write.kind === "reply") {
			const comment: ArtifactComment = { id: PENDING_ID_PREFIX + write.clientId, threadId: write.threadId, author: you, clientId: write.clientId, body: write.body, createdAt: write.at, editedAt: null, deleted: false };
			threads = threads.map((thread) => thread.id === write.threadId ? { ...thread, comments: [...thread.comments, comment] } : thread);
		} else {
			threads = threads.map((thread) => thread.id === write.threadId ? { ...thread, status: write.status } : thread);
		}
	}
	return { ...snapshot, threads };
}

function replaceThread(snapshot: ArtifactCommentsSnapshot, next: ArtifactCommentThread): ArtifactCommentsSnapshot {
	const known = snapshot.threads.some((thread) => thread.id === next.id);
	return { ...snapshot, threads: known ? snapshot.threads.map((thread) => thread.id === next.id ? next : thread) : [...snapshot.threads, next] };
}

/** Whether an error means the link is gone or no longer opens for this person. */
export function isLostAccess(error: unknown): boolean {
	return error instanceof CloudCpError && error.status === 404;
}

/**
 * One published link's comments for the signed-in person: the current
 * version's snapshot, read with its ETag so an unchanged read is a 304.
 * Read once when a published file opens (the header count) and again when
 * the window regains focus; while `panelOpen`, also every 30 seconds while
 * the document is visible, and right after each of this person's writes.
 * Nothing streams. Writes show at once and are replaced by the saved row.
 */
export function useArtifactComments(slug: string | null, { panelOpen = false, enabled = true }: { panelOpen?: boolean; enabled?: boolean } = {}) {
	const { client, ready, baseUrl, userId } = useCloudCp();
	const queryClient = useQueryClient();
	const key = useMemo(() => artifactCommentsQueryKey(baseUrl, userId, slug ?? ""), [baseUrl, slug, userId]);
	const query = useQuery({
		queryKey: key,
		enabled: enabled && ready && Boolean(slug),
		staleTime: 0,
		refetchOnWindowFocus: true,
		refetchInterval: panelOpen ? ARTIFACT_COMMENTS_POLL_MS : false,
		refetchIntervalInBackground: false,
		retry: (count, error) => !isLostAccess(error) && count < 1,
		queryFn: async ({ signal }): Promise<Cached> => {
			const previous = queryClient.getQueryData<Cached>(key);
			const result = await client.listArtifactShareComments(slug!, { signal, ...(previous?.etag ? { etag: previous.etag } : {}) });
			if (result.notModified) {
				// Same object: nothing downstream redraws.
				if (previous) return previous;
				const fresh = await client.listArtifactShareComments(slug!, { signal });
				if (fresh.notModified) throw new CloudCpError("The control plane answered 304 to a first read.", { status: 304 });
				return { snapshot: fresh.snapshot, etag: fresh.etag };
			}
			return { snapshot: result.snapshot, etag: result.etag };
		},
	});
	// Opening the panel reads at once rather than waiting for the next poll.
	const refetch = query.refetch;
	useEffect(() => {
		if (panelOpen && slug) void refetch();
	}, [panelOpen, refetch, slug]);

	const [pending, setPending] = useState<PendingWrite[]>([]);
	useEffect(() => setPending([]), [slug]);
	const snapshot = useMemo(
		() => query.data ? withPending(query.data.snapshot, pending) : undefined,
		[pending, query.data],
	);

	const patch = useCallback((update: (snapshot: ArtifactCommentsSnapshot) => ArtifactCommentsSnapshot) => {
		queryClient.setQueryData<Cached>(key, (current) => current ? { ...current, snapshot: update(current.snapshot) } : current);
	}, [key, queryClient]);

	/** Runs a write with its optimistic copy shown, then reads the new snapshot. */
	const write = useCallback(async <T>(optimistic: PendingWrite | null, run: () => Promise<T>, apply: (result: T) => void): Promise<T> => {
		if (optimistic) setPending((current) => [...current, optimistic]);
		try {
			const result = await run();
			apply(result);
			return result;
		} finally {
			if (optimistic) setPending((current) => current.filter((item) => item.clientId !== optimistic.clientId));
			void queryClient.invalidateQueries({ queryKey: key, exact: true });
		}
	}, [key, queryClient]);

	const actions = useMemo(() => ({
		createThread: (anchor: ArtifactCommentAnchor, body: string, clientId: string) => {
			const version = query.data?.snapshot.version ?? 1;
			return write(
				{ kind: "thread", clientId, anchor, body, at: new Date().toISOString() },
				() => client.createArtifactShareThread(slug!, { version, anchor, body, clientId }),
				(thread) => patch((current) => replaceThread(current, thread)),
			);
		},
		reply: (threadId: string, body: string, clientId: string) => write(
			{ kind: "reply", clientId, threadId, body, at: new Date().toISOString() },
			() => client.replyArtifactShareThread(slug!, threadId, { body, clientId }),
			(comment) => patch((current) => ({
				...current,
				threads: current.threads.map((thread) => thread.id === threadId && !thread.comments.some((item) => item.id === comment.id || item.clientId === comment.clientId)
					? { ...thread, comments: [...thread.comments, comment] }
					: thread),
			})),
		),
		setStatus: (threadId: string, status: ArtifactCommentThreadStatus) => write(
			{ kind: "status", clientId: crypto.randomUUID(), threadId, status },
			() => client.setArtifactShareThreadStatus(slug!, threadId, status),
			(thread) => patch((current) => replaceThread(current, thread)),
		),
		edit: (commentId: string, body: string) => write(
			null,
			() => client.editArtifactShareComment(slug!, commentId, { body }),
			(comment) => patch((current) => ({
				...current,
				threads: current.threads.map((thread) => ({ ...thread, comments: thread.comments.map((item) => item.id === comment.id ? comment : item) })),
			})),
		),
		remove: (commentId: string) => write(null, () => client.deleteArtifactShareComment(slug!, commentId), () => undefined),
		markSent: (threadId: string) => write(
			null,
			() => client.markArtifactShareThreadSent(slug!, threadId),
			(thread) => patch((current) => replaceThread(current, thread)),
		),
	}), [client, patch, query.data?.snapshot.version, slug, write]);

	const openCount = snapshot?.threads.filter((thread) => thread.status === "open").length ?? 0;
	return {
		snapshot,
		openCount,
		isLoading: query.isPending && query.fetchStatus !== "idle",
		error: query.error,
		lostAccess: isLostAccess(query.error),
		refetch,
		...actions,
	};
}

export type ArtifactCommentsState = ReturnType<typeof useArtifactComments>;
