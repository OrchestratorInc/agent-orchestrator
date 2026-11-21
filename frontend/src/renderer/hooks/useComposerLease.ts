import { useCallback, useEffect, useRef, useState } from "react";
import type { CloudCpClient, CloudCpComposerAnswer, CloudCpComposerParticipant, CloudCpSessionLive } from "../lib/cloud-cp";

/** How often a holder renews while the composer is in use. */
export const COMPOSER_RENEW_MS = 3_000;
/** At most how often a holder's draft is shared while they type. */
export const COMPOSER_DRAFT_PUBLISH_MS = 120;

export interface ComposerLeaseState {
	/**
	 * Someone else has this session open right now, so the lease applies and
	 * the holder's draft is shared, a viewer's view included.
	 */
	shared: boolean;
	/** This person holds the lease. */
	holding: boolean;
	/** Someone else holds the lease, or is ahead in line for it. */
	locked: boolean;
	/** Who holds the lease when it is not this person. */
	holder?: CloudCpComposerParticipant;
	/** This person's place in line, from 1, when waiting. */
	position?: number;
	canTakeOver: boolean;
	/** The grant to present with a send or steer, when holding. */
	epoch?: number;
	error?: string;
	/** What the holder is typing, when someone else holds the lease and has typed. */
	remoteDraft?: { holder: CloudCpComposerParticipant; text: string; cursor: number };
	/**
	 * Report a draft edit or caret move: takes the lease on the first keystroke,
	 * shares the draft live while holding it, releases when cleared.
	 */
	noteDraft: (text: string, caret?: number) => void;
	requestTurn: () => void;
	leaveLine: () => void;
	takeOver: () => void;
	/** The send or steer that used the grant was accepted; the server released the lease. */
	sent: () => void;
}

type ComposerClient = Pick<CloudCpClient, "acquireComposer" | "renewComposer" | "releaseComposer" | "requestComposerTurn" | "leaveComposerQueue" | "takeOverComposer" | "publishComposerDraft">;

/**
 * Drives a shared cloud session's composer lease from its live state. While
 * nobody else has the session open it does nothing at all, so solo use costs
 * no extra requests: the server admits a send without a grant while the lease
 * is free and nobody waits.
 */
export function useComposerLease({ client, orgId, sessionId, userId, live, canWrite }: {
	client: ComposerClient;
	orgId: string | undefined;
	sessionId: string;
	userId: string;
	live: CloudCpSessionLive | undefined;
	/** False for a read-only participant, who never takes the lease. */
	canWrite: boolean;
}): ComposerLeaseState {
	const [grant, setGrant] = useState<number>();
	const [error, setError] = useState<string>();
	const draftRef = useRef("");
	const caretRef = useRef<number | undefined>(undefined);
	const typedRef = useRef(false);
	const pendingRef = useRef(false);
	const publishTimerRef = useRef<number | undefined>(undefined);
	const publishingRef = useRef(false);
	const publishAgainRef = useRef(false);
	const epochRef = useRef<number | undefined>(undefined);

	const composer = live?.composer;
	const liveHolder = composer?.holder ?? undefined;
	const holdingLive = Boolean(liveHolder && liveHolder.userId === userId);
	// Anyone else here, a viewer too, watches the holder's draft, so the lease
	// (which carries the draft) applies as soon as someone else has it open.
	const othersHere = (live?.viewers ?? []).some((viewer) => viewer.userId !== userId);
	const shared = canWrite && Boolean(live) && (othersHere || Boolean(liveHolder && !holdingLive) || (composer?.waitlist.length ?? 0) > 0);
	const holding = holdingLive || (grant !== undefined && !(liveHolder && !holdingLive));
	const epoch = holdingLive ? composer!.epoch : grant;
	const position = composer ? composer.waitlist.findIndex((waiter) => waiter.userId === userId) + 1 : 0;
	const aheadInLine = Boolean(composer?.waitlist.length) && position !== 1;
	const locked = shared && !holding && (Boolean(liveHolder) || aheadInLine);
	epochRef.current = shared && holding ? epoch : undefined;

	// The live state is the authority: a grant it no longer shows (taken over,
	// lapsed) is gone, and one it shows passed to this person (from the line)
	// is theirs.
	useEffect(() => {
		if (!composer) return;
		if (holdingLive) setGrant(composer.epoch);
		else if (liveHolder) setGrant(undefined);
	}, [composer, holdingLive, liveHolder]);

	// Automatic calls (taking the lease on a keystroke, renewing) skip while
	// another is in flight; a person's own click always goes through.
	const run = useCallback(async (call: () => Promise<CloudCpComposerAnswer>, automatic = false) => {
		if (!orgId || (automatic && pendingRef.current)) return;
		if (automatic) pendingRef.current = true;
		try {
			const answer = await call();
			setGrant(answer.granted ? answer.composer.epoch : undefined);
			setError(undefined);
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : String(cause));
		} finally {
			if (automatic) pendingRef.current = false;
		}
	}, [orgId]);

	const acquire = useCallback(() => {
		if (!orgId) return;
		void run(() => client.acquireComposer(orgId, sessionId), true);
	}, [client, orgId, run, sessionId]);

	// Share the newest draft with everyone else; a draft typed while the last
	// one was on its way goes next, so the others always end on the latest.
	const publishDraft = useCallback(async () => {
		const grantEpoch = epochRef.current;
		if (!orgId || grantEpoch === undefined || draftRef.current.trim() === "") return;
		if (publishingRef.current) {
			publishAgainRef.current = true;
			return;
		}
		publishingRef.current = true;
		try {
			const text = draftRef.current;
			const cursor = Math.min(Math.max(caretRef.current ?? text.length, 0), text.length);
			const answer = await client.publishComposerDraft(orgId, sessionId, grantEpoch, text, cursor);
			typedRef.current = false;
			if (!answer.granted) setGrant(undefined);
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : String(cause));
		} finally {
			publishingRef.current = false;
			if (publishAgainRef.current) {
				publishAgainRef.current = false;
				void publishDraft();
			}
		}
	}, [client, orgId, sessionId]);
	// Throttled, not debounced: steady typing still reaches the others every
	// COMPOSER_DRAFT_PUBLISH_MS, and the publish sends the newest draft.
	const scheduleDraft = useCallback(() => {
		if (publishTimerRef.current !== undefined) return;
		publishTimerRef.current = window.setTimeout(() => {
			publishTimerRef.current = undefined;
			void publishDraft();
		}, COMPOSER_DRAFT_PUBLISH_MS);
	}, [publishDraft]);
	useEffect(() => () => {
		if (publishTimerRef.current !== undefined) window.clearTimeout(publishTimerRef.current);
	}, []);

	const noteDraft = useCallback((text: string, caret?: number) => {
		const edited = text !== draftRef.current;
		draftRef.current = text;
		caretRef.current = caret;
		if (!shared) return;
		if (text.trim() === "") {
			if (holding && epoch !== undefined && orgId) void run(() => client.releaseComposer(orgId, sessionId, epoch));
			return;
		}
		if (edited) typedRef.current = true;
		if (holding) scheduleDraft();
		else if (!locked && edited) acquire();
	}, [acquire, client, epoch, holding, locked, orgId, run, scheduleDraft, sessionId, shared]);

	// A grant just arrived (taken on the first keystroke, or handed over from
	// the line) for a draft already written: share it.
	useEffect(() => {
		if (shared && holding && epoch !== undefined && draftRef.current.trim() !== "") scheduleDraft();
	}, [epoch, holding, scheduleDraft, shared]);

	// Someone joined while this person had a draft: take the lease for it.
	useEffect(() => {
		if (shared && !holding && !locked && draftRef.current.trim() !== "") acquire();
	}, [acquire, holding, locked, shared]);

	// Renew while holding; report whether there was a keystroke since the last
	// renewal, so an idle holder loses the lease on the server's schedule.
	useEffect(() => {
		if (!shared || !holding || epoch === undefined || !orgId) return;
		const timer = window.setInterval(() => {
			const typing = typedRef.current;
			typedRef.current = false;
			void run(() => client.renewComposer(orgId, sessionId, epoch, typing), true);
		}, COMPOSER_RENEW_MS);
		return () => window.clearInterval(timer);
	}, [client, epoch, holding, orgId, run, sessionId, shared]);

	return {
		shared,
		holding,
		locked,
		holder: liveHolder && !holdingLive ? liveHolder : undefined,
		remoteDraft: liveHolder && !holdingLive && composer?.draft?.text
			? { holder: liveHolder, text: composer.draft.text, cursor: composer.draft.cursor }
			: undefined,
		position: position > 0 ? position : undefined,
		canTakeOver: Boolean(live?.canTakeOver),
		epoch: holding ? epoch : undefined,
		error,
		noteDraft,
		requestTurn: () => { if (orgId) void run(() => client.requestComposerTurn(orgId, sessionId)); },
		leaveLine: () => { if (orgId) void run(() => client.leaveComposerQueue(orgId, sessionId)); },
		takeOver: () => { if (orgId) void run(() => client.takeOverComposer(orgId, sessionId)); },
		sent: () => setGrant(undefined),
	};
}
