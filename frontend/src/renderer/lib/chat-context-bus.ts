import { useCallback, useMemo, useSyncExternalStore } from "react";
import type { FileCodeReference } from "../../shared/file-annotations";
import { captureFileReference, isSupportedFileReference, type ChatFileReference } from "../../shared/chat-file-reference";
import { sessionUiKey } from "./hosts";

// Lets a file or diff view hand a code reference, or the PR card a quoted
// comment, to the session's Chat composer without threading callbacks through
// the inspector and centre-pane trees. A composer registers while it can
// accept a new draft; the session view registers to bring the Chat surface
// forward when something arrives.
type ReferenceListener = (reference: ChatFileReference) => boolean | void;
type TextListener = (text: string) => boolean | void;
type RevealListener = () => void;

const composers = new Map<string, Set<ReferenceListener>>();
const textComposers = new Map<string, Set<TextListener>>();
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

/** Registers a composer listener, telling availability hooks as it comes and goes. */
function addComposer<T>(registry: Map<string, Set<T>>, sessionId: string, listener: T): () => void {
	const remove = add(registry, sessionId, listener);
	notifyAvailability();
	return () => {
		remove();
		notifyAvailability();
	};
}

/** Hands a value to the session's composers; accepted when one takes it, which brings Chat forward. */
function deliver<T>(registry: Map<string, Set<(value: T) => boolean | void>>, sessionId: string, value: T): boolean {
	const listeners = registry.get(sessionId);
	if (!listeners?.size) return false;
	let accepted = false;
	for (const listener of listeners) accepted = listener(value) !== false || accepted;
	if (accepted) for (const reveal of revealers.get(sessionId) ?? []) reveal();
	return accepted;
}

/** Registers the session's Chat composer as the destination for references. */
export function subscribeChatComposerReferences(sessionId: string, listener: ReferenceListener): () => void {
	return addComposer(composers, sessionId, listener);
}

/** Registers the session's Chat composer as the destination for quoted text (a PR comment). */
export function subscribeChatComposerText(sessionId: string, listener: TextListener): () => void {
	return addComposer(textComposers, sessionId, listener);
}

/** Text is accepted only when a listening composer adds it to its draft. */
export function sendTextToChat(sessionId: string, text: string): boolean {
	return deliver(textComposers, sessionId, text);
}

/**
 * Adds text to the session's Chat composer draft, below what is already
 * typed, so the person can say what to do with it before sending; undefined
 * while the session has no Chat composer (a TUI session).
 */
export function useAddTextToChat(sessionId: string, hostId?: string): ((text: string) => boolean) | undefined {
	const key = sessionUiKey(sessionId, hostId);
	const getSnapshot = useCallback(() => (textComposers.get(key)?.size ?? 0) > 0, [key]);
	const available = useSyncExternalStore(subscribeAvailability, getSnapshot, () => false);
	return useMemo(() => (available ? (text: string) => sendTextToChat(key, text) : undefined), [available, key]);
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
	return deliver(composers, sessionId, reference);
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
