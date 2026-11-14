import { useCallback, useMemo, useSyncExternalStore } from "react";
import type { FileCodeReference } from "../../shared/file-annotations";
import { captureFileReference, isSupportedFileReference, type ChatFileReference } from "../../shared/chat-file-reference";
import { sessionUiKey } from "./hosts";

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

function addComposer<T>(registry: Map<string, Set<T>>, sessionId: string, listener: T): () => void {
	const remove = add(registry, sessionId, listener);
	notifyAvailability();
	return () => {
		remove();
		notifyAvailability();
	};
}

function deliver<T>(registry: Map<string, Set<(value: T) => boolean | void>>, sessionId: string, value: T): boolean {
	const listeners = registry.get(sessionId);
	if (!listeners?.size) return false;
	let accepted = false;
	for (const listener of listeners) accepted = listener(value) !== false || accepted;
	if (accepted) for (const reveal of revealers.get(sessionId) ?? []) reveal();
	return accepted;
}

export function subscribeChatComposerReferences(sessionId: string, listener: ReferenceListener): () => void {
	return addComposer(composers, sessionId, listener);
}

export function subscribeChatComposerText(sessionId: string, listener: TextListener): () => void {
	return addComposer(textComposers, sessionId, listener);
}

export function sendTextToChat(sessionId: string, text: string): boolean {
	return deliver(textComposers, sessionId, text);
}

export function useAddTextToChat(sessionId: string, hostId?: string): ((text: string) => boolean) | undefined {
	const key = sessionUiKey(sessionId, hostId);
	const getSnapshot = useCallback(() => (textComposers.get(key)?.size ?? 0) > 0, [key]);
	const available = useSyncExternalStore(subscribeAvailability, getSnapshot, () => false);
	return useMemo(() => (available ? (text: string) => sendTextToChat(key, text) : undefined), [available, key]);
}

export function subscribeChatReveal(sessionId: string, listener: RevealListener): () => void {
	return add(revealers, sessionId, listener);
}

export function hasChatComposer(sessionId: string): boolean {
	return (composers.get(sessionId)?.size ?? 0) > 0;
}

export function sendReferenceToChat(sessionId: string, reference: ChatFileReference): boolean {
	return deliver(composers, sessionId, reference);
}

function subscribeAvailability(listener: () => void): () => void {
	availabilityListeners.add(listener);
	return () => availabilityListeners.delete(listener);
}

export function useHasChatComposer(sessionId: string): boolean {
	const getSnapshot = useCallback(() => hasChatComposer(sessionId), [sessionId]);
	return useSyncExternalStore(subscribeAvailability, getSnapshot, () => false);
}

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
