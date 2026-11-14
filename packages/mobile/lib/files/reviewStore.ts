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
/** When each review last changed, so one left alone for a week is dropped. */
const changedAt = new Map<string, number>();
const listeners = new Set<() => void>();
let nextId = 0;

/** A review untouched for this long is not restored. */
export const REVIEW_KEEP_MS = 7 * 24 * 60 * 60 * 1000;
/** The most comments kept on the phone across all reviews; the newest win. */
export const REVIEW_KEEP_COMMENTS = 300;

/**
 * Comments are keyed by machine and session so moving between files, or between
 * the list and the viewer, never loses one. They are also saved to the phone
 * (see reviewPersistence.ts): a phone closes apps in the background to free
 * memory, and an unsent review is the reader's own words, gone for good with it.
 */
export function reviewKey(hostId: string | undefined, sessionId: string): string {
	return `${hostId ?? ""}|${sessionId}`;
}

function emit(key: string, next: readonly ReviewComment[]) {
	if (next.length) {
		comments.set(key, next);
		changedAt.set(key, Date.now());
	} else {
		comments.delete(key);
		changedAt.delete(key);
	}
	for (const listener of listeners) listener();
}

/** What is saved to the phone: every review, newest first, within the limits above. */
export type StoredReviews = { v: 1; reviews: Array<{ key: string; changedAt: number; comments: ReviewComment[] }> };

export function storedReviews(now = Date.now()): StoredReviews {
	const reviews = [...comments.entries()]
		.map(([key, list]) => ({ key, changedAt: changedAt.get(key) ?? now, comments: [...list] }))
		.filter((review) => now - review.changedAt < REVIEW_KEEP_MS)
		.sort((a, b) => b.changedAt - a.changedAt);
	let room = REVIEW_KEEP_COMMENTS;
	const kept: StoredReviews["reviews"] = [];
	for (const review of reviews) {
		if (room <= 0) break;
		const list = review.comments.slice(-room);
		room -= list.length;
		kept.push({ ...review, comments: list });
	}
	return { v: 1, reviews: kept };
}

/**
 * Puts saved reviews back after the app restarts. Comments written since
 * launch stay, after the restored ones; anything unreadable is skipped.
 */
export function restoreReviews(raw: unknown, now = Date.now()): void {
	const stored = raw as Partial<StoredReviews> | null;
	if (!stored || stored.v !== 1 || !Array.isArray(stored.reviews)) return;
	let restored = false;
	for (const review of stored.reviews) {
		if (!review || typeof review.key !== "string" || typeof review.changedAt !== "number" || !Array.isArray(review.comments)) continue;
		if (now - review.changedAt >= REVIEW_KEEP_MS) continue;
		const saved = review.comments.filter(isComment);
		if (!saved.length) continue;
		const current = comments.get(review.key) ?? EMPTY;
		const ids = new Set(current.map((comment) => comment.id));
		comments.set(review.key, [...saved.filter((comment) => !ids.has(comment.id)), ...current]);
		changedAt.set(review.key, Math.max(review.changedAt, changedAt.get(review.key) ?? 0));
		restored = true;
	}
	if (restored) for (const listener of listeners) listener();
}

function isComment(value: unknown): value is ReviewComment {
	const comment = value as Partial<ReviewComment> | null;
	return Boolean(comment && typeof comment.id === "string" && typeof comment.label === "string" && typeof comment.feedback === "string"
		&& comment.target && typeof comment.target === "object" && (comment.status === "queued" || comment.status === "sent"));
}

/**
 * Forgets every review written on one machine (or on Orchestrator Cloud): the
 * machine was forgotten, or the Cloud account signed out or changed.
 */
export function forgetHostReviews(hostId: string): void {
	const prefix = reviewKey(hostId, "");
	let forgot = false;
	for (const key of [...comments.keys()]) {
		if (!key.startsWith(prefix)) continue;
		comments.delete(key);
		changedAt.delete(key);
		forgot = true;
	}
	if (forgot) for (const listener of listeners) listener();
}

/** Called after every change to the comments, for saving them. */
export function onReviewsChanged(listener: () => void): () => void {
	return subscribe(listener);
}

export function reviewComments(key: string): readonly ReviewComment[] {
	return comments.get(key) ?? EMPTY;
}

export function queuedComments(all: readonly ReviewComment[]): readonly ReviewComment[] {
	return all.filter((comment) => comment.status === "queued");
}

export function addReviewComment(key: string, comment: Omit<ReviewComment, "id" | "status"> & Partial<Pick<ReviewComment, "status">>): ReviewComment {
	// Unique across launches too: restored comments keep their ids.
	const created: ReviewComment = { status: "queued", ...comment, id: `c${Date.now().toString(36)}-${(++nextId).toString(36)}` };
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
