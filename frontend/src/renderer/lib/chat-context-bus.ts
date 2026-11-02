import { useCallback, useMemo, useSyncExternalStore } from "react";
import type { FileCodeReference } from "../../shared/file-annotations";
import { captureFileReference, isSupportedFileReference, type ChatFileReference } from "../../shared/chat-file-reference";
import { sessionUiKey } from "./hosts";

// Lets a file or diff view hand a code reference to the session's Chat
// composer without threading callbacks through the inspector and centre-pane
// trees. A composer registers while it can accept a new draft; the session
// view registers to bring the Chat surface forward when a reference arrives.
type ReferenceListener = (reference: ChatFileReference) => boolean | void;
type RevealListener = () => void;

const composers = new Map<string, Set<ReferenceListener>>();
const revealers = new Map<string, Set<RevealListener>>();
const availabilityListeners = new Set<() => void>();

function add<T>(registry: Map<string, Set<T>>, sessionId: string, listener: T): () => void {
	let listeners = registry.get(sessionId);
	if (!listeners) {
		listeners = new Set();
		registry.set(sessionId, listeners);
	}
	listeners.add(listener);
	return () => {
		const current = registry.get(sessionId);
		if (!current) return;
		current.delete(listener);
		if (current.size === 0) registry.delete(sessionId);
	};
}

function notifyAvailability(): void {
	for (const listener of availabilityListeners) listener();
}

/** Registers the session's Chat composer as the destination for references. */
export function subscribeChatComposerReferences(sessionId: string, listener: ReferenceListener): () => void {
	const remove = add(composers, sessionId, listener);
	notifyAvailability();
	return () => {
		remove();
		notifyAvailability();
	};
}

/** Registers a callback that brings the session's Chat surface on screen. */
export function subscribeChatReveal(sessionId: string, listener: RevealListener): () => void {
	return add(revealers, sessionId, listener);
}

export function hasChatComposer(sessionId: string): boolean {
	return (composers.get(sessionId)?.size ?? 0) > 0;
}

/** A reference is accepted only when a listening composer acknowledges it. */
export function sendReferenceToChat(sessionId: string, reference: ChatFileReference): boolean {
	const listeners = composers.get(sessionId);
	if (!listeners?.size) return false;
	let accepted = false;
	for (const listener of listeners) accepted = listener(reference) !== false || accepted;
	if (accepted) for (const reveal of revealers.get(sessionId) ?? []) reveal();
	return accepted;
}

function subscribeAvailability(listener: () => void): () => void {
	availabilityListeners.add(listener);
	return () => availabilityListeners.delete(listener);
}

/** True while the session has a Chat composer that can take a reference. */
export function useHasChatComposer(sessionId: string): boolean {
	const getSnapshot = useCallback(() => hasChatComposer(sessionId), [sessionId]);
	return useSyncExternalStore(subscribeAvailability, getSnapshot, () => false);
}

/**
 * Puts highlighted code into the session's Chat composer as a reference chip,
 * or is undefined while the session has no Chat composer (a TUI session). The
 * composer registers under the host-scoped UI key, not the raw session ID.
 */
export function useAskInChat(sessionId: string, hostId?: string): ((reference: FileCodeReference) => boolean) | undefined {
	const key = sessionUiKey(sessionId, hostId);
	const available = useHasChatComposer(key);
	return useMemo(
		() => (available ? (reference: FileCodeReference) => {
			const captured = captureFileReference(reference, sessionId, hostId);
			return isSupportedFileReference(captured) && sendReferenceToChat(key, captured);
		} : undefined),
		[available, key, sessionId, hostId],
	);
}
