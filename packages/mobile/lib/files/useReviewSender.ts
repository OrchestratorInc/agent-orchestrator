import { useCallback, useMemo, useState } from "react";
import type { ServerConfig } from "../config";
import { userFacingError } from "../connectionError";
import { haptics } from "../haptics";
import { sendFileFeedback } from "./api";
import { markReviewCommentsSent, queuedComments, removeReviewComments, reviewComments, useReviewComments } from "./reviewStore";

/**
 * Sends the queued review in order. Each message the daemon accepts marks the
 * comments it carried as sent — they stay on their lines — so a failure part-way
 * leaves only the unsent ones queued.
 */
export function useReviewSender(config: ServerConfig | null, key: string, sessionId: string) {
	const all = useReviewComments(key);
	const queued = useMemo(() => queuedComments(all), [all]);
	const [sending, setSending] = useState(false);
	const [error, setError] = useState<string>();

	const send = useCallback(async () => {
		if (!config || sending) return;
		const pending = queuedComments(reviewComments(key));
		if (!pending.length) return;
		setSending(true);
		setError(undefined);
		let delivered = 0;
		try {
			await sendFileFeedback(config, sessionId, pending.map(({ target, feedback }) => ({ target, feedback })), (count) => {
				markReviewCommentsSent(key, pending.slice(delivered, delivered + count).map((comment) => comment.id));
				delivered += count;
			});
			haptics.success();
		} catch (cause) {
			haptics.error();
			setError(userFacingError(cause, "Couldn't send the review. Your comments are still here."));
		} finally {
			setSending(false);
		}
	}, [config, key, sending, sessionId]);

	const remove = useCallback((id: string) => removeReviewComments(key, [id]), [key]);

	return { all, comments: queued, sending, error, send, remove };
}
