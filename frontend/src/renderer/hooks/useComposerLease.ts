import { useCallback, useEffect, useRef, useState } from "react";
import type { CloudCpClient, CloudCpComposerAnswer, CloudCpComposerParticipant, CloudCpSessionLive } from "../lib/cloud-cp";

export const COMPOSER_RENEW_MS = 3_000;
export const COMPOSER_DRAFT_PUBLISH_MS = 120;

export interface ComposerLeaseState {
	shared: boolean;
	holding: boolean;
	locked: boolean;
	holder?: CloudCpComposerParticipant;
	position?: number;
	canTakeOver: boolean;
	epoch?: number;
	error?: string;
	remoteDraft?: { holder: CloudCpComposerParticipant; text: string; cursor: number };
	noteDraft: (text: string, caret?: number) => void;
	requestTurn: () => void;
	leaveLine: () => void;
	takeOver: () => void;
	sent: () => void;
}

type ComposerClient = Pick<CloudCpClient, "acquireComposer" | "renewComposer" | "releaseComposer" | "requestComposerTurn" | "leaveComposerQueue" | "takeOverComposer" | "publishComposerDraft">;

export function useComposerLease({ client, orgId, sessionId, userId, live, canWrite }: {
	client: ComposerClient;
	orgId: string | undefined;
	sessionId: string;
	userId: string;
	live: CloudCpSessionLive | undefined;
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
	const othersHere = (live?.viewers ?? []).some((viewer) => viewer.userId !== userId);
	const shared = canWrite && Boolean(live) && (othersHere || Boolean(liveHolder && !holdingLive) || (composer?.waitlist.length ?? 0) > 0);
	const holding = holdingLive || (grant !== undefined && !(liveHolder && !holdingLive));
	const epoch = holdingLive ? composer!.epoch : grant;
	const position = composer ? composer.waitlist.findIndex((waiter) => waiter.userId === userId) + 1 : 0;
	const aheadInLine = Boolean(composer?.waitlist.length) && position !== 1;
	const locked = shared && !holding && (Boolean(liveHolder) || aheadInLine);
	epochRef.current = shared && holding ? epoch : undefined;

	useEffect(() => {
		if (!composer) return;
		if (holdingLive) setGrant(composer.epoch);
		else if (liveHolder) setGrant(undefined);
	}, [composer, holdingLive, liveHolder]);

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

	useEffect(() => {
		if (shared && holding && epoch !== undefined && draftRef.current.trim() !== "") scheduleDraft();
	}, [epoch, holding, scheduleDraft, shared]);

	useEffect(() => {
		if (shared && !holding && !locked && draftRef.current.trim() !== "") acquire();
	}, [acquire, holding, locked, shared]);

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
