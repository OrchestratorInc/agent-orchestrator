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

/** Takes prompt, exactly, out of the new-chat draft. */
function removeFromDraft(prompt: string): void {
	const scope = newChatDraftScope();
	const draft = readChatSessionDraft(scope).composer.text;
	if (!draft.includes(prompt)) return;
	if (writeChatComposerText(scope, withoutPrompt(draft, prompt)).ok) {
		useUiStore.setState((state) => ({ newChatDraftRevision: state.newChatDraftRevision + 1 }));
	}
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
	forgetSavedPrompt();
	removeFromDraft(pending.prompt);
}

/**
 * Takes a shared artifact's prompt out of the new-chat draft when no copy is
 * waiting for it any more (after a reload): only the exact line that was
 * written, never text the person added around it.
 */
export function dropOrphanSharedArtifactPrompt(): void {
	if (useSharedArtifactDraftStore.getState().pending) return;
	const prompt = readSavedPrompt();
	if (!prompt) return;
	forgetSavedPrompt();
	removeFromDraft(prompt);
}
