import { useCallback, useEffect, useRef, useState } from "react";
import { messageEditDraft, type MessageEditDraft } from "./InlineMessageEditor";
import { editRefusedDefinitively } from "./conversationErrors";
import { editableMessageText } from "./messageAttachments";
import { clientMessageId } from "./useConversation";
import type { ConversationItem, ConversationMessage } from "./types";

/**
 * The prompt open in its inline editor, held above the virtualized list so the
 * draft survives the row scrolling out and back.
 *
 * The editor is drawn in place of its prompt, so an ordinary edit whose prompt
 * leaves the conversation (another device edited or rolled it back, or a refresh
 * dropped its page) closes, and `onEditingChange(false)` brings the composer back.
 *
 * An edit whose delivery came back uncertain is different: the daemon may already
 * have branched, which itself removes the prompt. It keeps its exact payload and
 * delivery id so Retry replays the first result instead of branching twice, and
 * it stays open (drawn detached, as on the desktop) until it is resolved or
 * deliberately abandoned.
 */
export function useMessageEdit({ items, onEditMessage, onEditingChange, onStart }: {
	items: readonly ConversationItem[];
	onEditMessage?(turnId: string, text: string, clientMessageId: string): Promise<void>;
	onEditingChange?(editing: boolean): void;
	onStart?(): void;
}) {
	const [editDraft, setEditDraft] = useState<MessageEditDraft>();
	const draftRef = useRef(editDraft);
	draftRef.current = editDraft;
	const startEdit = useCallback((message: ConversationMessage) => {
		if (!message.turnId) return;
		const { body, attachments } = editableMessageText(message.text);
		onStart?.();
		setEditDraft(messageEditDraft({ turnId: message.turnId, initialText: body, attachments }));
	}, [onStart]);
	const cancelEdit = useCallback(() => setEditDraft(undefined), []);
	/** Sends `payload`; `body` is what the editor shows if the delivery comes back uncertain. */
	const submitEdit = useCallback(async (payload: string, body: string) => {
		const draft = draftRef.current;
		if (!draft || !onEditMessage) return;
		try {
			await onEditMessage(draft.turnId, payload, draft.deliveryId);
			setEditDraft((current) => current?.deliveryId === draft.deliveryId ? undefined : current);
		} catch (cause) {
			const code = typeof cause === "object" && cause !== null && "code" in cause ? String(cause.code ?? "") || undefined : undefined;
			setEditDraft((current) => {
				if (current?.deliveryId !== draft.deliveryId) return current;
				// A definitive refusal proves nothing was sent: a fresh attempt cannot
				// duplicate it. Anything else may have reached the provider.
				return editRefusedDefinitively(code)
					? { ...current, deliveryId: clientMessageId(), pending: undefined }
					: { ...current, pending: { body, payload } };
			});
			throw cause;
		}
	}, [onEditMessage]);

	const editingTurnId = editDraft?.turnId;
	const pending = Boolean(editDraft?.pending);
	const promptPresent = editingTurnId !== undefined && items.some((item) =>
		item.kind === "message" && item.role === "user" && item.origin === "human" && item.turnId === editingTurnId);
	useEffect(() => {
		if (editingTurnId !== undefined && !promptPresent && !pending) {
			setEditDraft((current) => current?.turnId === editingTurnId && !current.pending ? undefined : current);
		}
	}, [editingTurnId, pending, promptPresent]);

	const editing = editDraft !== undefined && (promptPresent || pending);
	useEffect(() => {
		onEditingChange?.(editing);
	}, [editing, onEditingChange]);

	return {
		editDraft: editing ? editDraft : undefined,
		editing,
		/** An unresolved delivery whose prompt is gone: draw the editor apart from the list. */
		detached: editing && !promptPresent,
		startEdit,
		cancelEdit,
		submitEdit,
	};
}
