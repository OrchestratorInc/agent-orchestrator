import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { userFacingError } from "../connectionError";
import { haptics } from "../haptics";
import type { FilesBackend } from "./backend";
import { markReviewCommentsSent, queuedComments, removeReviewComments, reviewComments, useReviewComments } from "./reviewStore";

/**
 * Sends the queued review in order. Each message the daemon accepts marks the
 * comments it carried as sent — they stay on their lines — so a failure part-way
 * leaves only the unsent ones queued.
 */
export function useReviewSender(backend: FilesBackend | null, key: string, sessionId: string) {
	const all = useReviewComments(key);
	const queued = useMemo(() => queuedComments(all), [all]);
	const [sending, setSending] = useState(false);
	const [error, setError] = useState<string>();
	// A review still waiting for the connection when the page closes is not sent
	// behind the user's back: what was not sent stays queued for the next Send.
	const closed = useRef<AbortController | null>(null);
	useEffect(() => {
		const controller = new AbortController();
		closed.current = controller;
		return () => controller.abort();
	}, []);

	const send = useCallback(async () => {
		if (!backend || sending) return;
		const pending = queuedComments(reviewComments(key));
		if (!pending.length) return;
		const signal = closed.current?.signal;
		setSending(true);
		setError(undefined);
		let delivered = 0;
		try {
			await backend.sendFeedback(sessionId, pending.map(({ target, feedback }) => ({ target, feedback })), (count) => {
				markReviewCommentsSent(key, pending.slice(delivered, delivered + count).map((comment) => comment.id));
				delivered += count;
			}, signal);
			haptics.success();
		} catch (cause) {
			if (signal?.aborted) return;
			haptics.error();
			setError(userFacingError(cause, "Couldn't send the review. Your comments are still here."));
		} finally {
			setSending(false);
		}
	}, [backend, key, sending, sessionId]);

	const remove = useCallback((id: string) => removeReviewComments(key, [id]), [key]);

	return { all, comments: queued, sending, error, send, remove };
}
