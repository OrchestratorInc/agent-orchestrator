import { useCallback, useEffect, useRef, useState } from "react";
import type { CloudCpClient, CloudCpComposerAnswer, CloudCpComposerParticipant, CloudCpSessionLive } from "../lib/cloud-cp";

/** How often a holder renews while the composer is in use. */
export const COMPOSER_RENEW_MS = 3_000;

export interface ComposerLeaseState {
	/** Someone else can write in this session right now, so the lease applies. */
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
	/** Report a draft edit: takes the lease on the first keystroke, renews while typing, releases when cleared. */
	noteDraft: (text: string) => void;
	requestTurn: () => void;
	leaveLine: () => void;
	takeOver: () => void;
	/** The send or steer that used the grant was accepted; the server released the lease. */
	sent: () => void;
}

type ComposerClient = Pick<CloudCpClient, "acquireComposer" | "renewComposer" | "releaseComposer" | "requestComposerTurn" | "leaveComposerQueue" | "takeOverComposer">;

/**
 * Drives a shared cloud session's composer lease from its live state. In a
 * session nobody else can write in, it does nothing at all, so solo use costs
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
	const typedRef = useRef(false);
	const pendingRef = useRef(false);

	const composer = live?.composer;
	const liveHolder = composer?.holder ?? undefined;
	const holdingLive = Boolean(liveHolder && liveHolder.userId === userId);
	const otherWriters = (live?.viewers ?? []).some((viewer) => viewer.userId !== userId && viewer.role !== "viewer");
	const shared = canWrite && Boolean(live) && (otherWriters || Boolean(liveHolder && !holdingLive) || (composer?.waitlist.length ?? 0) > 0);
	const holding = holdingLive || (grant !== undefined && !(liveHolder && !holdingLive));
	const epoch = holdingLive ? composer!.epoch : grant;
	const position = composer ? composer.waitlist.findIndex((waiter) => waiter.userId === userId) + 1 : 0;
	const aheadInLine = Boolean(composer?.waitlist.length) && position !== 1;
	const locked = shared && !holding && (Boolean(liveHolder) || aheadInLine);

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

	const noteDraft = useCallback((text: string) => {
		draftRef.current = text;
		if (!shared) return;
		if (text.trim() === "") {
			if (holding && epoch !== undefined && orgId) void run(() => client.releaseComposer(orgId, sessionId, epoch));
			return;
		}
		typedRef.current = true;
		if (!holding && !locked) acquire();
	}, [acquire, client, epoch, holding, locked, orgId, run, sessionId, shared]);

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
