import type { PendingSideClose, SideCloseOutcome, SideCloseEvidence } from "../shared/side-close";

export interface RecoveryRecord {
	side: { id: string; sessionId: string; label?: string; [key: string]: unknown };
	providerHostId?: string;
	providerForkId?: string;
	harness?: string;
	cleanupOnly?: boolean;
	hostStopped?: boolean;
}
export interface SideSnapshotCoverage { ids: readonly string[]; ready?: boolean }
export interface SideLaunchSnapshot {
	appRunId: string;
	sides: unknown[];
	numbers?: Record<string, number>;
}

// Close intent is owned by Electron for this launch, including while the daemon
// is unavailable. A stale open snapshot must never replace a cleanup record.
export class SideCloseRecovery {
	state?: SideLaunchSnapshot;
	private closing = new Map<string, { original: RecoveryRecord; cleanup: RecoveryRecord; observed: boolean; accepted: boolean; conversationClosed: boolean }>();
	private closed = new Set<string>();
	// Retain shutdown proof for this Electron launch, including across renderer
	// remounts and snapshots that no longer contain the native fork cleanup.
	private confirmedClosed = new Map<string, string>();

	private rememberClosed(side: RecoveryRecord["side"]): void {
		this.confirmedClosed.set(side.id, side.sessionId);
		this.closed.add(side.id);
	}

	remember(state: SideLaunchSnapshot, coverage?: SideSnapshotCoverage): void {
		const records = state.sides as RecoveryRecord[];
		const previous = (this.state?.sides ?? []) as RecoveryRecord[];
		const covered = (id: string) => !coverage || coverage.ids.includes(id);
		const numbers = this.state?.numbers || state.numbers ? Object.fromEntries([...new Set([...Object.keys(this.state?.numbers ?? {}), ...Object.keys(state.numbers ?? {})])].map(id => [id, Math.max(this.state?.numbers?.[id] ?? 0, state.numbers?.[id] ?? 0)])) : undefined;
		// A replacement renderer can discover cleanup without having issued DELETE.
		for (const record of records) {
			if (record.cleanupOnly && record.hostStopped) this.rememberClosed(record.side);
			if (record.cleanupOnly && !record.hostStopped && !this.closing.has(record.side.id) && !this.closed.has(record.side.id)) {
				this.closing.set(record.side.id, { original: record, cleanup: record, observed: true, accepted: false, conversationClosed: true });
				this.closed.add(record.side.id);
			}
		}
		for (const [id, intent] of this.closing) {
			const incoming = records.find((record) => record.side?.id === id);
			if (incoming?.cleanupOnly) {
				intent.cleanup = incoming;
				intent.observed = true;
				intent.conversationClosed = true;
			} else if (!incoming && covered(id) && (intent.conversationClosed || intent.accepted)) {
				this.rememberClosed(intent.original.side);
				this.closing.delete(id);
			}
		}
		this.state = { ...state, ...(numbers ? { numbers } : {}), sides: [
			...records.filter((record) => record.side?.id && !this.closing.has(record.side.id) && (record.cleanupOnly || !this.closed.has(record.side.id))),
			...previous.filter(record => !covered(record.side.id) && !records.some(next => next.side?.id === record.side.id) && !this.closing.has(record.side.id) && (record.cleanupOnly || !this.closed.has(record.side.id))),
			...[...this.closing.values()].map((intent) => intent.cleanup),
		] };
	}

	begin(id: string): void {
		if (this.closing.has(id)) return;
		const original = (this.state?.sides as RecoveryRecord[] | undefined)?.find((record) => record.side?.id === id);
		if (!original) throw new Error("Side chat recovery ownership is unavailable. Refresh and retry close.");
		const cleanup: RecoveryRecord = {
			side: Object.fromEntries(Object.entries(original.side).filter(([key]) => ["id", "sessionId", "mainConversationId", "label", "state", "contextMode", "hasWork", "createdAt", "updatedAt"].includes(key))) as RecoveryRecord["side"],
			providerHostId: original.providerHostId,
			providerForkId: original.providerForkId, harness: original.harness,
			cleanupOnly: true, hostStopped: original.hostStopped,
		};
		this.closing.set(id, { original, cleanup, observed: Boolean(original.cleanupOnly), accepted: false, conversationClosed: Boolean(original.cleanupOnly) });
		this.closed.add(id);
		this.state = { ...this.state!, sides: this.state!.sides.map((record) => (record as RecoveryRecord).side?.id === id ? cleanup : record) };
	}

	cancel(id: string, verified?: RecoveryRecord): boolean {
		const intent = this.closing.get(id);
		if (!intent) return true;
		if (intent.observed || intent.accepted) return false;
		this.closing.delete(id);
		this.closed.delete(id);
		this.state = { ...this.state!, sides: this.state!.sides.map((record) => (record as RecoveryRecord).side?.id === id ? verified ?? intent.original : record) };
		return true;
	}

	confirm(id: string): void {
		const intent = this.closing.get(id);
		if (intent) {
			intent.accepted = true;
			this.rememberClosed(intent.original.side);
		}
	}

	markRestoreAttempt(snapshot?: SideLaunchSnapshot): void {
		// Once cleanup may have reached a replacement daemon, an old daemon's
		// rejection cannot establish that the close was never accepted.
		for (const [id, intent] of this.closing) {
			if (!snapshot || (snapshot.sides as RecoveryRecord[]).some(record => record.side.id === id && record.cleanupOnly)) intent.observed = true;
		}
	}
	markRestoreAccepted(snapshot: SideLaunchSnapshot): void {
		for (const record of snapshot.sides as RecoveryRecord[]) {
			const intent = this.closing.get(record.side.id);
			if (record.cleanupOnly && intent) { intent.observed = true; intent.conversationClosed = true; }
		}
	}

	// Only call after an already-closed DELETE and a fresh snapshot from the
	// same daemon: absence then proves its stop-before-delete cleanup finished.
	reconcileMissing(id: string, snapshot: SideLaunchSnapshot, coverage?: SideSnapshotCoverage): SideCloseOutcome {
		const record = (snapshot.sides as RecoveryRecord[]).find((record) => record.side?.id === id);
		if ((!record && (!coverage || coverage.ids.includes(id))) || (record?.cleanupOnly && record.hostStopped)) {
			this.confirm(id);
			this.remember(snapshot, coverage);
			return "closed";
		}
		return record?.cleanupOnly ? "cleanup_pending" : "unconfirmed";
	}

	pending(sessionId?: string): PendingSideClose[] {
		return [...this.closing.values()]
			.filter((intent) => !intent.accepted && !intent.cleanup.hostStopped && !this.confirmedClosed.has(intent.original.side.id) && (!sessionId || intent.original.side.sessionId === sessionId))
			.map(({ original, conversationClosed }) => ({ id: original.side.id, sessionId: original.side.sessionId, label: original.side.label ?? "", phase: conversationClosed ? "cleanup_pending" : "unconfirmed" }));
	}

	summary(sessionId?: string): SideCloseEvidence {
		return {
			pending: this.pending(sessionId),
			closedIds: [...this.confirmedClosed].filter(([, owner]) => !sessionId || owner === sessionId).map(([id]) => id),
		};
	}
	confirmed(): { id: string; sessionId: string }[] { return [...this.confirmedClosed].map(([id, sessionId]) => ({ id, sessionId })); }
}
