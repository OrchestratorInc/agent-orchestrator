import { useCallback, useSyncExternalStore } from "react";
import type { FileAnnotationTarget } from "../../../shared/files/file-annotations";

/** Where a comment sits in the diff: under this line, on this side. */
export type CommentAnchor = { side: "old" | "new"; line: number };

/**
 * A note written against a file. `queued` ones wait to go with the rest of the
 * review; `sent` ones stay on the line, GitHub-style, so the reader still sees
 * what was asked where.
 */
export type ReviewComment = {
	id: string;
	/** "pkce.ts · Lines 39–40", for the queue and for screen readers. */
	label: string;
	feedback: string;
	target: FileAnnotationTarget;
	anchor?: CommentAnchor;
	status: "queued" | "sent";
};

const EMPTY: readonly ReviewComment[] = [];
const comments = new Map<string, readonly ReviewComment[]>();
const listeners = new Set<() => void>();
let nextId = 0;

/**
 * Comments are keyed by machine and session so moving between files, or between
 * the list and the viewer, never loses one. They live for the app process: a
 * review is short-lived, and an unsent note is cheap to retype.
 */
export function reviewKey(hostId: string | undefined, sessionId: string): string {
	return `${hostId ?? ""}|${sessionId}`;
}

function emit(key: string, next: readonly ReviewComment[]) {
	if (next.length) comments.set(key, next);
	else comments.delete(key);
	for (const listener of listeners) listener();
}

export function reviewComments(key: string): readonly ReviewComment[] {
	return comments.get(key) ?? EMPTY;
}

export function queuedComments(all: readonly ReviewComment[]): readonly ReviewComment[] {
	return all.filter((comment) => comment.status === "queued");
}

export function addReviewComment(key: string, comment: Omit<ReviewComment, "id" | "status"> & Partial<Pick<ReviewComment, "status">>): ReviewComment {
	const created: ReviewComment = { status: "queued", ...comment, id: `c${++nextId}` };
	emit(key, [...reviewComments(key), created]);
	return created;
}

export function updateReviewComment(key: string, id: string, feedback: string) {
	emit(key, reviewComments(key).map((comment) => comment.id === id ? { ...comment, feedback } : comment));
}

export function markReviewCommentsSent(key: string, ids: readonly string[]) {
	const sent = new Set(ids);
	emit(key, reviewComments(key).map((comment) => sent.has(comment.id) ? { ...comment, status: "sent" as const } : comment));
}

export function removeReviewComments(key: string, ids: readonly string[]) {
	const drop = new Set(ids);
	emit(key, reviewComments(key).filter((comment) => !drop.has(comment.id)));
}

function subscribe(listener: () => void) {
	listeners.add(listener);
	return () => { listeners.delete(listener); };
}

export function useReviewComments(key: string): readonly ReviewComment[] {
	const read = useCallback(() => reviewComments(key), [key]);
	return useSyncExternalStore(subscribe, read, read);
}
