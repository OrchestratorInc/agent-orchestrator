import { useCallback, useEffect, useState } from "react";
import { useUiStore } from "../stores/ui-store";

export type ChatMessageFocusTarget = { id: string; text: string; messageId: string };

export function useChatMessageFocus(uiSessionId: string, reviewId: string | undefined, ready: boolean, focus: (target: ChatMessageFocusTarget) => void) {
	const request = useUiStore((state) => state.chatMessageFocusRequest);
	useEffect(() => {
		if (!request || !ready) return;
		if (request.sessionId !== uiSessionId || request.reviewId !== reviewId) return;
		focus({ id: `palette:${request.nonce}`, text: request.text, messageId: request.messageId });
		useUiStore.getState().clearChatMessageFocusRequest(request.nonce);
	}, [focus, ready, request, reviewId, uiSessionId]);
}

export function useChatNavigationRequest<T extends { id: string }>(owner: string) {
	const [pending, setPending] = useState<{ owner: string; request: T }>();
	const request = pending?.owner === owner ? pending.request : undefined;
	const navigate = useCallback((next: T) => setPending({ owner, request: next }), [owner]);
	const handled = useCallback((id: string) => {
		setPending((current) => current?.request.id === id ? undefined : current);
	}, []);
	return { request, navigate, handled };
}
