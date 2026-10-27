import { useEffect } from "react";
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
