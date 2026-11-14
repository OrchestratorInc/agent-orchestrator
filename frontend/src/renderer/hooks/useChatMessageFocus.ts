import { useCallback, useEffect, useState } from "react";
import { useUiStore } from "../stores/ui-store";

export type ChatMessageFocusTarget = { id: string; text: string; messageId: string };

/**
 * Hands a palette chat hit to the transcript that holds its message: the
 * session's own chat, or (with reviewId) the reviewer chat for that review.
 * The transcript's annotation navigation then scrolls to and marks it.
 */
export function useChatMessageFocus(uiSessionId: string, reviewId: string | undefined, ready: boolean, focus: (target: ChatMessageFocusTarget) => void) {
	const request = useUiStore((state) => state.chatMessageFocusRequest);
	useEffect(() => {
		if (!request || !ready) return;
		if (request.sessionId !== uiSessionId || request.reviewId !== reviewId) return;
		focus({ id: `palette:${request.nonce}`, text: request.text, messageId: request.messageId });
		useUiStore.getState().clearChatMessageFocusRequest(request.nonce);
	}, [focus, ready, request, reviewId, uiSessionId]);
}

/**
 * A chat's pending navigation request (a palette hit, an annotation's source),
 * kept for the chat it was asked of. A chat surface can outlive its chat: the
 * Cloud surface stays mounted from session to session and the chat inside
 * remounts on each, so a request held past its use was applied again to
 * whichever chat mounted next. It applies once: the chat reports it handled
 * and it is dropped.
 */
export function useChatNavigationRequest<T extends { id: string }>(owner: string) {
	const [pending, setPending] = useState<{ owner: string; request: T }>();
	const request = pending?.owner === owner ? pending.request : undefined;
	const navigate = useCallback((next: T) => setPending({ owner, request: next }), [owner]);
	const handled = useCallback((id: string) => {
		setPending((current) => current?.request.id === id ? undefined : current);
	}, []);
	return { request, navigate, handled };
}
