import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { userFacingError } from "../connectionError";
import { haptics } from "../haptics";
import { markReviewCommentsSent, queuedComments, removeReviewComments, reviewComments, reviewLifetime, useReviewComments } from "./reviewStore";
import type { WorkspaceSource } from "./workspaceSource";

// The list and detail can both remain mounted in the navigation stack.
const sendingReviews = new Map<string, { lifetime: number; promise: Promise<void> }>();
const listeners = new Set<() => void>();
const emitSending = () => { for (const listener of listeners) listener(); };
function subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; }

/**
 * Sends the queued review in order. Each message the daemon accepts marks the
 * comments it carried as sent — they stay on their lines — so a failure part-way
 * leaves only the unsent ones queued.
 */
export function useReviewSender(source: WorkspaceSource | null, key: string, sessionId: string) {
	const all = useReviewComments(key);
	const queued = useMemo(() => queuedComments(all), [all]);
	const [failure, setFailure] = useState<{ source: WorkspaceSource; key: string; sessionId: string; error: string }>();
	const mounted = useRef(false);
	const owner = useRef({ source, key, sessionId }); owner.current = { source, key, sessionId };
	useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
	const readSending = useCallback(() => Boolean(source?.isCurrent() && sendingReviews.get(key)?.lifetime === reviewLifetime(key)), [key, source]);
	const sending = useSyncExternalStore(subscribe, readSending, readSending);

	const send = useCallback(async () => {
		const ownsUI = () => mounted.current && owner.current.source === source && owner.current.key === key && owner.current.sessionId === sessionId && Boolean(source?.isCurrent());
		if (!source || !ownsUI()) return;
		const lifetime = reviewLifetime(key);
		const existing = sendingReviews.get(key); if (existing?.lifetime === lifetime) return existing.promise;
		const pending = queuedComments(reviewComments(key));
		if (!pending.length) return;
		const ownsQueue = () => source.isCurrent() && reviewLifetime(key) === lifetime;
		const promise = Promise.resolve().then(async () => {
			if (!ownsQueue()) return;
			if (ownsUI()) setFailure(undefined);
			let delivered = 0;
			const accepted = (count: number) => {
				if (ownsQueue()) markReviewCommentsSent(key, pending.slice(delivered, delivered + count).map((comment) => comment.id));
				delivered += count;
			};
			try {
				const count = await source.sendFeedback(sessionId, pending.map(({ target, feedback }) => ({ target, feedback })), accepted);
				if (count > delivered) accepted(count - delivered);
				if (ownsUI() && ownsQueue()) haptics.success();
			} catch (cause) {
				if (ownsUI() && ownsQueue()) {
					haptics.error();
					setFailure({ source, key, sessionId, error: userFacingError(cause, "Couldn't send the review. Your comments are still here.") });
				}
			}
		}).finally(() => { if (sendingReviews.get(key)?.promise === promise) { sendingReviews.delete(key); emitSending(); } });
		sendingReviews.set(key, { lifetime, promise }); emitSending();
		return promise;
	}, [source, key, sessionId]);

	const remove = useCallback((id: string) => { if (mounted.current && owner.current.source === source && owner.current.key === key && source?.isCurrent()) removeReviewComments(key, [id]); }, [key, source]);
	const error = failure?.source === source && failure.key === key && failure.sessionId === sessionId && source?.isCurrent() ? failure.error : undefined;

	return { all: source?.isCurrent() ? all : [], comments: source?.isCurrent() ? queued : [], sending, error, send, remove };
}
