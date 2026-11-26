import { newChatDraftScope, readChatSessionDraft, writeChatComposerText } from "./chat-drafts";
import { useSharedArtifactDraftStore } from "../stores/shared-artifact-draft-store";
import { useUiStore } from "../stores/ui-store";

/** text without prompt, and the blank line written after it. */
export function withoutPrompt(text: string, prompt: string): string {
	return text.replace(`${prompt}\n\n`, "").replace(prompt, "").trim();
}

/**
 * Lets go of the copy waiting for the new standalone session, and takes its
 * prompt out of the new-chat draft: the prompt names the copy, so left behind
 * it would point the agent at a file that was never copied.
 */
export function dropSharedArtifactDraft(): void {
	const pending = useSharedArtifactDraftStore.getState().pending;
	if (!pending) return;
	useSharedArtifactDraftStore.getState().clearPending();
	const scope = newChatDraftScope();
	const draft = readChatSessionDraft(scope).composer.text;
	if (!draft.includes(pending.prompt)) return;
	if (writeChatComposerText(scope, withoutPrompt(draft, pending.prompt)).ok) {
		useUiStore.setState((state) => ({ newChatDraftRevision: state.newChatDraftRevision + 1 }));
	}
}


/**
 * Takes a shared artifact's prompt out of the new-chat draft when no copy is
 * waiting for it any more: the copy is held in memory, the draft is saved, so
 * after a reload the line would name a file that will never be copied.
 * template is the prompt with "\u0000" for the path.
 */
export function dropOrphanSharedArtifactPrompt(template: string): void {
	if (useSharedArtifactDraftStore.getState().pending) return;
	const [before = "", after = ""] = template.split("\u0000");
	const escape = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
	const line = new RegExp(`${escape(before)}shared/[^\\n]*${escape(after)}(\\n\\n)?`, "g");
	const scope = newChatDraftScope();
	const draft = readChatSessionDraft(scope).composer.text;
	const rest = draft.replace(line, "").trim();
	if (rest === draft.trim()) return;
	if (writeChatComposerText(scope, rest).ok) {
		useUiStore.setState((state) => ({ newChatDraftRevision: state.newChatDraftRevision + 1 }));
	}
}
