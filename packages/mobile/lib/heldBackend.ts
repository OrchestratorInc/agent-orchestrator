import { useCallback, useEffect, useReducer, useRef, useState } from "react";
import type { ArtifactsBackend, ArtifactSource } from "./artifactsBackend";
import { isUnreachableError, UnreachableError } from "./connectionError";
import type { FilesBackend } from "./files/backend";

// A machine's backend that lives as long as the machine stays selected,
// instead of as long as its connection.
//
// The verified connection drops on every app switch and endpoint race: a LAN
// address can belong to another machine when the phone wakes, so it must not
// get the credential. Backends built on that connection came and went with it,
// and every screen keyed on one (the file viewer, the file lists, the artifact
// viewer) threw away what it showed and started over: the file read again,
// the list at the top, the selection gone. #7688 fixed the chat this way; this
// does the same for the backends those screens share.
//
// A held backend keeps one identity per machine, so nothing keyed on it
// resets. Every request still goes through the live backend, never an old
// address: a call made while the connection is away waits for it to come back
// (up to HELD_WAIT_MS, then fails as unreachable). URL builders (an image, a
// page in a web view) answer from the live backend too; with none, they repeat
// what they last answered for the same arguments, so a view already showing it
// keeps the same source and does not load again.
//
// That repeated source still carries the old connection's credential, and
// until the identity probe answers, the old address may be another device's
// (a LAN address on another network). So nothing may start a new request with
// it: a retry, a reload, a download or a view mounted fresh waits for the
// connection to be verified again (`verified`, `whenVerified`,
// `useVerifiedOnMount`, `useVerifiedFor`). What is already on screen is left
// as it is.

/** How long a call waits for the connection before it fails as unreachable. */
export const HELD_WAIT_MS = 15_000;

export type LiveSlot<B> = {
	get(): B | null;
	set(next: B | null): void;
	/** The live backend, now or once the connection is back; fails as unreachable after `timeoutMs` (never, for Infinity). */
	whenLive(signal?: AbortSignal | null, timeoutMs?: number): Promise<B>;
};

export function createLiveSlot<B>(): LiveSlot<B> {
	let current: B | null = null;
	const waiting = new Set<(live: B) => void>();
	return {
		get: () => current,
		set(next) {
			current = next;
			if (!next) return;
			for (const resolve of [...waiting]) resolve(next);
		},
		whenLive(signal, timeoutMs = HELD_WAIT_MS) {
			if (current) return Promise.resolve(current);
			return new Promise<B>((resolve, reject) => {
				const done = (live: B) => { cleanup(); resolve(live); };
				const fail = (error: Error) => { cleanup(); reject(error); };
				const timer = Number.isFinite(timeoutMs) ? setTimeout(() => fail(new UnreachableError("offline")), timeoutMs) : undefined;
				const onAbort = () => fail(Object.assign(new Error("Request cancelled"), { name: "AbortError" }));
				function cleanup() {
					clearTimeout(timer);
					waiting.delete(done);
					signal?.removeEventListener("abort", onAbort);
				}
				if (signal?.aborted) return onAbort();
				signal?.addEventListener("abort", onAbort, { once: true });
				waiting.add(done);
			});
		},
	};
}

/** Whether the held backend's connection is verified, and a wait (with no limit) until it is. */
function verifiedBy<B>(slot: LiveSlot<B>) {
	return {
		verified: () => slot.get() !== null,
		whenVerified: (signal?: AbortSignal) => slot.whenLive(signal, Infinity).then(() => undefined),
	};
}

/** `fn` on the live backend, waiting for it while the connection is away. */
function onLive<B, R>(slot: LiveSlot<B>, fn: (live: B) => Promise<R>, signal?: AbortSignal | null): Promise<R> {
	const live = slot.get();
	return live ? fn(live) : slot.whenLive(signal).then(fn);
}

/** A URL builder that repeats its last answer for the same arguments while the connection is away. */
function heldSource<B, A extends unknown[]>(slot: LiveSlot<B>, build: (live: B, ...args: A) => ArtifactSource | undefined) {
	const last = new Map<string, ArtifactSource | undefined>();
	return (...args: A): ArtifactSource | undefined => {
		const key = JSON.stringify(args.map((arg) => (typeof arg === "function" ? null : arg)));
		const live = slot.get();
		if (!live) return last.get(key);
		const source = build(live, ...args);
		// The same object while it is the same source, so a view keyed on it does not reload.
		const previous = last.get(key);
		const kept = previous && source && previous.uri === source.uri && JSON.stringify(previous.headers) === JSON.stringify(source.headers) ? previous : source;
		last.set(key, kept);
		return kept;
	};
}

/** A daemon Files backend, held for the machine `key` names (see above). */
export function holdFilesBackend(slot: LiveSlot<FilesBackend>, first: FilesBackend, key: string): FilesBackend {
	const viewOf = (live: FilesBackend) => live;
	return heldFiles(slot, first, key, viewOf);
}

function heldFiles(slot: LiveSlot<FilesBackend>, first: FilesBackend, key: string, pick: (live: FilesBackend) => FilesBackend): FilesBackend {
	const on = <R,>(fn: (live: FilesBackend) => Promise<R>, signal?: AbortSignal | null) => onLive(slot, (live) => fn(pick(live)), signal);
	const view = pick(first);
	const git = view.git;
	return {
		key: view.view ? `${key}|${JSON.stringify(view.view)}` : key,
		cloud: view.cloud,
		...(view.view ? { view: view.view } : {}),
		manifest: (sessionId, signal) => on((live) => live.manifest(sessionId, signal), signal),
		// A cancelled call stops waiting and is never sent (the screen that asked for it closed).
		file: (sessionId, path, read, signal) => on((live) => live.file(sessionId, path, read, signal), signal),
		tree: (sessionId, dir, signal) => on((live) => live.tree(sessionId, dir, signal), signal),
		search: (sessionId, query, signal) => on((live) => live.search(sessionId, query, signal), signal),
		imageSource: (sessionId, path, side, version, read, signal) => on((live) => live.imageSource(sessionId, path, side, version, read, signal), signal),
		sendFeedback: (sessionId, comments, onSent, signal) => on((live) => live.sendFeedback(sessionId, comments, onSent, signal), signal),
		...verifiedBy(slot),
		...(git ? {
			git: {
				branches: (sessionId, signal) => on((live) => live.git!.branches(sessionId, signal), signal),
				history: (sessionId, signal) => on((live) => live.git!.history(sessionId, signal), signal),
				rangeFiles: (sessionId, signal) => on((live) => live.git!.rangeFiles(sessionId, signal), signal),
				// Another branch or the pull request on the same machine, held the same way.
				at: (target) => heldFiles(slot, first, key, (live) => pick(live).git!.at(target)),
			},
		} : {}),
	};
}

/** A daemon artifacts backend, held for the machine `key` names (see above). */
export function holdArtifactsBackend(slot: LiveSlot<ArtifactsBackend>, first: ArtifactsBackend, key: string): ArtifactsBackend {
	const on = <R,>(fn: (live: ArtifactsBackend) => Promise<R>, signal?: AbortSignal | null) => onLive(slot, fn, signal);
	return {
		key,
		cloud: first.cloud,
		canChange: first.canChange,
		list: (sessionId, signal) => on((live) => live.list(sessionId, signal), signal),
		text: (sessionId, path, signal) => on((live) => live.text(sessionId, path, signal), signal),
		page: (sessionId, path, signal) => on((live) => live.page(sessionId, path, signal), signal),
		imageSource: heldSource(slot, (live, sessionId: string, path: string, version?: string) => live.imageSource(sessionId, path, version)),
		webSource: heldSource(slot, (live, sessionId: string, path: string) => live.webSource(sessionId, path)),
		streamSource: heldSource(slot, (live, sessionId: string, path: string, version?: string) => live.streamSource(sessionId, path, version)),
		markdownImageSource: heldSource(slot, (live, sessionId: string, documentPath: string, url: string, versionOf?: (path: string) => string | undefined) => live.markdownImageSource(sessionId, documentPath, url, versionOf)),
		...(first.size ? { size: (sessionId: string, path: string, signal?: AbortSignal) => on((live) => live.size?.(sessionId, path, signal) ?? Promise.resolve(undefined), signal) } : {}),
		...(first.head ? { head: (sessionId: string, path: string, bytes: number, signal?: AbortSignal) => on((live) => live.head!(sessionId, path, bytes, signal), signal) } : {}),
		...(first.textWithin ? { textWithin: (sessionId: string, path: string, maxBytes: number, signal?: AbortSignal) => on((live) => live.textWithin!(sessionId, path, maxBytes, signal), signal) } : {}),
		assetLoader: (sessionId) => ({
			text: (path) => on((live) => live.assetLoader(sessionId).text(path)),
			dataUri: (path, maxBytes) => on((live) => live.assetLoader(sessionId).dataUri(path, maxBytes)),
		}),
		remove: (sessionId, path) => on((live) => live.remove(sessionId, path)),
		versions: (sessionId, path, signal) => on((live) => live.versions(sessionId, path, signal), signal),
		...(first.syncVersions ? { syncVersions: (sessionId: string, path: string, signal?: AbortSignal) => on((live) => live.syncVersions!(sessionId, path, signal), signal) } : {}),
		version: (sessionId, path, version, signal) => on((live) => live.version(sessionId, path, version, signal), signal),
		restore: (sessionId, path, version) => on((live) => live.restore(sessionId, path, version)),
		...verifiedBy(slot),
	};
}

/**
 * `live` held for as long as `scope` (the machine) stays the same: null until
 * the first live backend for that machine, then one object for good. Without
 * a scope (nothing selected) it is `live` as is.
 */
export function useHeldBackend<B>(live: B | null, scope: string, hold: (slot: LiveSlot<B>, first: B, key: string) => B): B | null {
	const held = useRef<{ scope: string; slot: LiveSlot<B>; backend: B | null } | null>(null);
	if (!scope) return live;
	if (held.current?.scope !== scope) held.current = { scope, slot: createLiveSlot<B>(), backend: null };
	const entry = held.current;
	// Set as it renders, not after: a child's effect in the same commit (they run
	// first) must never reach the connection that just went away.
	entry.slot.set(live);
	if (live && !entry.backend) entry.backend = hold(entry.slot, live, scope);
	return entry.backend;
}

type Verifiable = { verified?(): boolean; whenVerified?(signal?: AbortSignal): Promise<void> };

/** A source's identity for `useVerifiedFor`: its address and the credential it carries. */
export function sourceKey(source: { uri: string; headers?: Record<string, string> } | null | undefined): string | null {
	return source ? JSON.stringify([source.uri, source.headers ?? null]) : null;
}

/**
 * Whether a view may load what `key` names (a source, see `sourceKey`) with the
 * backend's credential: at once with a verified connection (or a backend that
 * needs none, as Cloud's), else once the connection is verified again. What
 * the view has on screen stays: the same key, still on screen, keeps it through
 * a reconnect. Any other key waits: a view first becoming eligible (a card
 * scrolled into view, a page swiped to), another file, or one shown before
 * another source (or nothing) replaced it. `null` is nothing to load yet.
 *
 * Checked when the view becomes eligible, not when it mounts: one mounted
 * hidden while connected still waits if it first shows while the app
 * reconnects. Build the source in the component that calls this, so the
 * render it wakes builds it again from the verified connection.
 */
export function useVerifiedFor(backend: Verifiable | null | undefined, key: string | null | undefined): boolean {
	const allowed = useRef<{ backend: Verifiable; key: string } | undefined>(undefined);
	const [, wake] = useReducer((count: number) => count + 1, 0);
	// A backend with no verification concept (Cloud's signed addresses) is always ready.
	const needs = Boolean(backend?.whenVerified);
	const kept = allowed.current?.backend === backend && allowed.current?.key === key;
	const ready = key != null && (!needs || (backend?.verified?.() ?? true) || kept);
	// Only the source on screen keeps its allowance: another key (or nothing) on
	// screen, or another backend, ends it, so switching back to an earlier source
	// is a new load that waits (A -> B -> A must not remount A's old credential).
	allowed.current = ready && needs && backend ? { backend, key: key! } : undefined;
	const waiting = needs && key != null && !ready;
	useEffect(() => {
		if (!waiting || !backend?.whenVerified) return;
		const cancel = new AbortController();
		backend.whenVerified(cancel.signal).then(() => { if (!cancel.signal.aborted) wake(); }, () => {});
		return () => cancel.abort();
	}, [backend, waiting]);
	return ready;
}

/**
 * Whether a view that loads with the backend's credential may mount: at once
 * with a verified connection (or a backend that needs none, as Cloud's), else
 * once the connection is verified again. Once it has mounted, it stays: losing
 * the connection later does not take it off screen. A view that loads later
 * than it mounts (once it scrolls into view or is swiped to) checks with
 * `useVerifiedFor` at that moment instead.
 */
export function useVerifiedOnMount(backend: Verifiable | null | undefined): boolean {
	return useVerifiedFor(backend, "mounted");
}

/**
 * Runs `fn` (a reload, a retry) now with a verified connection, else once it is
 * verified again; cancelled with `signal`.
 */
export function afterVerified(backend: Verifiable | null | undefined, fn: () => void, signal?: AbortSignal): void {
	if (!backend?.whenVerified || (backend.verified?.() ?? true)) {
		fn();
		return;
	}
	backend.whenVerified(signal).then(() => { if (!signal?.aborted) fn(); }, () => {});
}

/**
 * `afterVerified` for a Retry button: `retry(fn)` runs `fn` now with a verified
 * connection, else once it is verified again, and only while the view still
 * shows what Retry was pressed for, `target` on `backend`. A view reused for
 * another file drops a Retry still waiting, as unmounting does, and so does a
 * second press; one that wakes for an earlier target does nothing.
 */
export function useRetryAfterVerified(backend: Verifiable | null | undefined, target: string): (fn: () => void) => void {
	const pending = useRef<AbortController | null>(null);
	const current = useRef({ backend, target });
	current.current = { backend, target };
	useEffect(() => () => pending.current?.abort(), [backend, target]);
	return useCallback((fn: () => void) => {
		pending.current?.abort();
		const controller = new AbortController();
		pending.current = controller;
		const pressedFor = current.current;
		afterVerified(pressedFor.backend, () => {
			if (current.current.backend !== pressedFor.backend || current.current.target !== pressedFor.target) return;
			fn();
		}, controller.signal);
	}, []);
}

/**
 * A source resolved afresh through the live connection once it is verified,
 * for a view opened from one already on screen (the full-screen image viewer):
 * the one on screen may carry the last connection's address and credential,
 * and the machine answering as itself at a new address does not vouch for the
 * old one. Undefined when cancelled with `signal` or when it cannot be resolved.
 */
export async function resolveWhenVerified<S>(backend: Verifiable | null | undefined, resolve: (signal?: AbortSignal) => Promise<S | undefined>, signal?: AbortSignal): Promise<S | undefined> {
	try {
		if (backend?.whenVerified && !(backend.verified?.() ?? true)) await backend.whenVerified(signal);
		if (signal?.aborted) return undefined;
		const source = await resolve(signal);
		return signal?.aborted ? undefined : source;
	} catch {
		return undefined;
	}
}

/** What `useOpenWhenVerified` is doing: opening the view named `key`, or failed to. */
export type VerifiedOpenState = { pending?: string; failed?: string };

/**
 * Opens a view (the full-screen image viewer) from a source resolved afresh
 * with `resolveWhenVerified`, saying what happens meanwhile: `pending` names
 * the view opening, while it waits for the connection too, and `failed` the
 * one that could not open, because the connection stayed away for `timeoutMs`
 * or the verified connection could not resolve its source. Opening it again
 * tries again. Another open or unmounting drops one in flight without a word.
 */
export function useOpenWhenVerified<S>(backend: Verifiable | null | undefined, timeoutMs = HELD_WAIT_MS): VerifiedOpenState & {
	open(key: string, resolve: (signal?: AbortSignal) => Promise<S | undefined>, show: (source: S) => void): void;
} {
	const [state, setState] = useState<VerifiedOpenState>({});
	const inFlight = useRef<AbortController | null>(null);
	useEffect(() => () => inFlight.current?.abort(), []);
	const open = useCallback((key: string, resolve: (signal?: AbortSignal) => Promise<S | undefined>, show: (source: S) => void) => {
		inFlight.current?.abort();
		const controller = new AbortController();
		inFlight.current = controller;
		let timedOut = false;
		const timer = setTimeout(() => { timedOut = true; controller.abort(); }, timeoutMs);
		setState({ pending: key });
		void resolveWhenVerified(backend, resolve, controller.signal).then((source) => {
			clearTimeout(timer);
			// Replaced by another open, or unmounted: nothing to say.
			if (inFlight.current !== controller || (controller.signal.aborted && !timedOut)) return;
			inFlight.current = null;
			if (source !== undefined && !timedOut) {
				setState({});
				show(source);
			} else {
				setState({ failed: key });
			}
		});
	}, [backend, timeoutMs]);
	return { ...state, open };
}

/**
 * `load` (what a view will show, where undefined is a confirmed "nothing to
 * show"), tried again once the connection is verified when it started while
 * the connection was away and failed to reach it: a call that timed out waiting
 * is not an answer, so it must not be kept as "nothing there". Other failures,
 * and one from an attempt made while verified, reject as they are; cancelled
 * with `signal`.
 */
export async function retryWhenVerified<T>(backend: Verifiable | null | undefined, load: (signal?: AbortSignal) => Promise<T>, signal?: AbortSignal): Promise<T> {
	for (;;) {
		// Started while the connection was away: a failure to reach it is no answer.
		const away = Boolean(backend?.whenVerified) && !(backend?.verified?.() ?? true);
		try {
			return await load(signal);
		} catch (error) {
			if (!away || !isUnreachableError(error) || signal?.aborted) throw error;
			await backend!.whenVerified!(signal);
		}
	}
}
