import { newChatDraftScope, readChatSessionDraft, writeChatComposerText } from "./chat-drafts";
import { useSharedArtifactDraftStore } from "../stores/shared-artifact-draft-store";
import { useUiStore } from "../stores/ui-store";

/**
 * The exact prompt line last written into the new-chat draft for a shared
 * artifact's copy. Saved beside the draft, which survives a reload while the
 * copy (held in memory) does not, so the line can be taken out exactly.
 */
const PROMPT_KEY = "ao.sharedArtifactDraftPrompt";

function readSavedPrompt(): string | null {
	try {
		return localStorage.getItem(PROMPT_KEY);
	} catch {
		return null;
	}
}

/** Remembers the prompt line written into the draft for a shared artifact's copy. */
export function rememberSharedArtifactPrompt(prompt: string): void {
	try {
		localStorage.setItem(PROMPT_KEY, prompt);
	} catch {
		// Without storage, a reload leaves the line for the person to delete.
	}
}

function forgetSavedPrompt(): void {
	try {
		localStorage.removeItem(PROMPT_KEY);
	} catch {
		// Nothing saved.
	}
}

/** text without prompt, and the blank line written after it. Anything else the person typed stays. */
export function withoutPrompt(text: string, prompt: string): string {
	return text.replace(`${prompt}\n\n`, "").replace(prompt, "").trim();
}

/** Takes prompt, exactly, out of the new-chat draft: false when the draft could not be saved. */
function removeFromDraft(prompt: string): boolean {
	const scope = newChatDraftScope();
	const draft = readChatSessionDraft(scope).composer.text;
	if (!draft.includes(prompt)) return true;
	if (!writeChatComposerText(scope, withoutPrompt(draft, prompt)).ok) return false;
	useUiStore.setState((state) => ({ newChatDraftRevision: state.newChatDraftRevision + 1 }));
	return true;
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
	// Kept while the line is still in the draft, so a later cleanup can take it out.
	if (removeFromDraft(pending.prompt)) forgetSavedPrompt();
}

/**
 * Takes a shared artifact's prompt out of the new-chat draft when no copy is
 * waiting for it any more (after a reload): only the exact line that was
 * written, never text the person added around it. False when the draft
 * could not be saved, leaving the line to be taken out later.
 */
export function dropOrphanSharedArtifactPrompt(): boolean {
	if (useSharedArtifactDraftStore.getState().pending) return true;
	const prompt = readSavedPrompt();
	if (!prompt) return true;
	if (!removeFromDraft(prompt)) return false;
	forgetSavedPrompt();
	return true;
}
