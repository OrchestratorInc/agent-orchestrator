import { useCallback, useEffect, useState } from "react";
import { messageEditDraft, type MessageEditDraft } from "./InlineMessageEditor";
import { editableMessageText } from "./messageAttachments";
import type { ConversationItem, ConversationMessage } from "./types";

/**
 * The prompt open in its inline editor, held above the virtualized list so the
 * draft survives the row scrolling out and back.
 *
 * The editor is drawn in place of its prompt, so an edit whose prompt leaves the
 * conversation (another device edited or rolled it back, or a refresh dropped
 * its page) would have nothing on screen to type in while the composer stays
 * hidden for it. The edit closes then, and `onEditingChange(false)` brings the
 * composer back.
 */
export function useMessageEdit({ items, onEditMessage, onEditingChange, onStart }: {
	items: readonly ConversationItem[];
	onEditMessage?(turnId: string, text: string, clientMessageId: string): Promise<void>;
	onEditingChange?(editing: boolean): void;
	onStart?(): void;
}) {
	const [editDraft, setEditDraft] = useState<MessageEditDraft>();
	const startEdit = useCallback((message: ConversationMessage) => {
		if (!message.turnId) return;
		const { body, attachments } = editableMessageText(message.text);
		onStart?.();
		setEditDraft(messageEditDraft({ turnId: message.turnId, initialText: body, attachments }));
	}, [onStart]);
	const cancelEdit = useCallback(() => setEditDraft(undefined), []);
	const submitEdit = useCallback(async (turnId: string, text: string, clientMessageId: string) => {
		if (!onEditMessage) return;
		await onEditMessage(turnId, text, clientMessageId);
		setEditDraft((current) => current?.turnId === turnId ? undefined : current);
	}, [onEditMessage]);

	const editingTurnId = editDraft?.turnId;
	const promptPresent = editingTurnId !== undefined && items.some((item) =>
		item.kind === "message" && item.role === "user" && item.origin === "human" && item.turnId === editingTurnId);
	useEffect(() => {
		if (editingTurnId !== undefined && !promptPresent) {
			setEditDraft((current) => current?.turnId === editingTurnId ? undefined : current);
		}
	}, [editingTurnId, promptPresent]);

	const editing = editDraft !== undefined && promptPresent;
	useEffect(() => {
		onEditingChange?.(editing);
	}, [editing, onEditingChange]);

	return { editDraft: editing ? editDraft : undefined, editing, startEdit, cancelEdit, submitEdit };
}
