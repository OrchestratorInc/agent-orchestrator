import { SIDE_CLOSE_OPERATION_TIMEOUT_MS, SIDE_CLOSE_PROTOCOL_VERSION, type SideCloseEntry, type SideCloseRequest, type SideCloseState, type SideCloseStatus } from "../shared/side-close";
import { SideCloseRecovery, type RecoveryRecord, type SideSnapshotCoverage } from "./side-close";
import type { SideLaunchAttachment, SideLaunchCapture } from "./side-launch-recovery";

const PREPARE_MS = 5_000;
const DELETE_MS = 30_000;
const READ_MS = 5_000;
const rejectedStatuses = new Set([400, 401, 403, 405, 409, 422]);
type Active = { promise: Promise<SideCloseState>; abort: AbortController; epoch: string; dispatched: boolean };
type Tracked = { entry: SideCloseEntry; uncertain: boolean; active?: Active };
const key = (sessionId: string, id: string) => JSON.stringify([sessionId, id]);

function bounded<T>(promise: Promise<T>, ms: number, signal: AbortSignal): Promise<T> {
	return new Promise<T>((resolve, reject) => {
		const abort = () => finish(() => reject(new Error("Side close request interrupted")));
		const timer = setTimeout(() => finish(() => reject(new Error("Side close request timed out"))), ms);
		const finish = (done: () => void) => { clearTimeout(timer); signal.removeEventListener("abort", abort); done(); };
		if (signal.aborted) { abort(); return; }
		signal.addEventListener("abort", abort, { once: true });
		promise.then(value => finish(() => resolve(value)), error => finish(() => reject(error)));
	});
}

/** One launch-owned close operation. IPC replies and renderer lifetime own no shutdown facts. */
export class SideCloseController {
	private epoch = "unavailable";
	private revision = 0;
	private recovery: SideCloseState["recovery"] = "unavailable";
	private attachmentKey = "";
	private published = "";
	private hydrated = false;
	private entries = new Map<string, Tracked>();
	private requests = new Map<string, string>();
	constructor(private readonly options: {
		ledger: SideCloseRecovery;
		attachment: () => SideLaunchAttachment | undefined;
		ensureClaimed: () => Promise<void>;
		capture: () => Promise<SideLaunchCapture>;
		fetch?: typeof fetch;
		onChange: (state: SideCloseState) => void;
		onOwnershipChange: () => void;
	}) {}

	setAttachment(attachment: SideLaunchAttachment | undefined, recovery: SideCloseState["recovery"]): void {
		const attachmentKey = JSON.stringify(attachment ?? null);
		const epoch = attachment ? `${attachment.appRunId}:${attachment.generation}` : this.epoch;
		if (attachmentKey !== this.attachmentKey) {
			this.attachmentKey = attachmentKey;
			this.hydrated = false;
			this.epoch = epoch;
			for (const tracked of this.entries.values()) {
				if (tracked.active) {
					tracked.uncertain ||= tracked.active.dispatched;
					tracked.active.abort.abort();
				}
				// Positive OPEN evidence is attachment-specific, even after the
				// operation has settled and no request remains to abort.
				if (tracked.active || tracked.entry.status === "rejected_open") this.update(tracked, "unconfirmed", false);
			}
		}
		this.recovery = recovery === "ready" && !this.hydrated ? "restoring" : recovery;
		this.publish();
	}
	setRecovery(recovery: SideCloseState["recovery"]): void {
		if (recovery === "ready" && !this.hydrated) recovery = "restoring";
		if (this.recovery === recovery) return;
		this.recovery = recovery;
		this.publish();
	}
	hasPending(): boolean { return [...this.entries.values()].some(item => item.entry.status === "closing" || item.entry.status === "unconfirmed" || item.entry.status === "cleanup_pending") || Boolean(this.options.ledger.state?.sides.some(record => (record as RecoveryRecord).cleanupOnly)); }
	summary(sessionId?: string): SideCloseState {
		return { protocolVersion: SIDE_CLOSE_PROTOCOL_VERSION, sessionId: sessionId ?? null, epoch: this.epoch, revision: this.revision, recovery: this.recovery,
			sides: [...this.entries.values()].map(item => item.entry).filter(item => !sessionId || item.sessionId === sessionId).map(item => ({ ...item, error: item.error ? { ...item.error } : undefined })) };
	}

	reconcile(coverage?: SideSnapshotCoverage): void {
		if (coverage?.ready) { this.hydrated = true; this.recovery = "ready"; }
		for (const side of this.options.ledger.confirmed()) {
			const id = key(side.sessionId, side.id);
			if (!this.entries.has(id)) this.entries.set(id, { entry: { ...side, label: "", version: 1, status: "closed" }, uncertain: false });
		}
		const pending = this.options.ledger.pending();
		for (const side of pending) {
			const id = key(side.sessionId, side.id);
			let tracked = this.entries.get(id);
			if (!tracked) { tracked = { entry: { id: side.id, sessionId: side.sessionId, label: side.label, version: 1, status: side.phase }, uncertain: true }; this.entries.set(id, tracked); }
			this.updateLabel(tracked, side.label, false);
			if (!tracked.active && tracked.entry.status !== "closed") this.update(tracked, side.phase, false);
		}
		for (const tracked of this.entries.values()) if (this.options.ledger.summary(tracked.entry.sessionId).closedIds.includes(tracked.entry.id)) this.update(tracked, "closed", false);
		this.publish();
	}

	requestClose(input: SideCloseRequest): Promise<SideCloseState> {
		if (!input || [input.sessionId, input.sideId, input.requestId, input.epoch].some(value => typeof value !== "string" || !value || value.length > 255) || !Number.isSafeInteger(input.expectedVersion) || input.expectedVersion < 0) throw new Error("Invalid side close request");
		const id = key(input.sessionId, input.sideId);
		const seen = this.requests.get(input.requestId);
		if (seen && seen !== id) throw new Error("Side close request identity was reused");
		if (input.epoch !== this.epoch) return Promise.resolve(this.summary(input.sessionId));
		let tracked = this.entries.get(id);
		if (tracked?.entry.status === "closed") return Promise.resolve(this.summary(input.sessionId));
		if (tracked?.active) return tracked.active.promise;
		if (seen) return Promise.resolve(this.summary(input.sessionId));
		if (input.expectedVersion !== (tracked?.entry.version ?? 0)) return Promise.resolve(this.summary(input.sessionId));
		const original = this.record(input.sideId);
		if (original && original.side.sessionId !== input.sessionId) throw new Error("Side chat belongs to another session");
		if (!tracked) { tracked = { entry: { id: input.sideId, sessionId: input.sessionId, label: original?.side.label ?? "", version: 0, status: "unconfirmed" }, uncertain: false }; this.entries.set(id, tracked); }
		this.requests.set(input.requestId, id);
		tracked.entry.operationId = input.requestId;
		tracked.entry.error = undefined;
		let resolve!: (state: SideCloseState) => void;
		const active: Active = { promise: new Promise(yes => { resolve = yes; }), abort: new AbortController(), epoch: this.epoch, dispatched: false };
		tracked.active = active;
		this.update(tracked, "closing");
		void this.run(tracked, active).then(() => resolve(this.summary(input.sessionId)), () => {
			if (tracked!.entry.status !== "closed") this.update(tracked!, "unconfirmed");
			resolve(this.summary(input.sessionId));
		});
		return active.promise;
	}

	private record(id: string): RecoveryRecord | undefined { return (this.options.ledger.state?.sides as RecoveryRecord[] | undefined)?.find(record => record.side.id === id); }
	private publish(): void {
		const content = JSON.stringify({ epoch: this.epoch, recovery: this.recovery, sides: this.summary().sides });
		if (content === this.published) return;
		this.published = content;
		this.revision++;
		try { this.options.onChange(this.summary()); } catch { /* notification failure cannot revoke an owned outcome */ }
	}
	private update(tracked: Tracked, status: SideCloseStatus, publish = true): void {
		if (tracked.entry.status === "closed" && status !== "closed") return;
		if (tracked.entry.status !== status || status === "closing") { tracked.entry.status = status; tracked.entry.version++; if (publish) this.publish(); }
	}
	private updateLabel(tracked: Tracked, label: unknown, publish = true): void {
		if (typeof label !== "string" || !label || label === tracked.entry.label) return;
		tracked.entry.label = label;
		tracked.entry.version++;
		if (publish) this.publish();
	}
	private ownershipChanged(): void { this.options.onOwnershipChange(); }
	private closed(tracked: Tracked): void {
		// Commit proof BEFORE any fallible bookkeeping, notification, body disposal, or IPC reply.
		this.update(tracked, "closed", false);
		tracked.uncertain = false;
		try { this.options.ledger.confirm(tracked.entry.id); } catch { /* confirmed HTTP proof belongs to this controller too */ }
		try { this.ownershipChanged(); } catch { /* proof remains owned */ }
		this.publish();
	}
	private async run(tracked: Tracked, active: Active): Promise<void> {
		const attachment = this.options.attachment();
		const sameAttachment = () => this.epoch === active.epoch && JSON.stringify(this.options.attachment()) === JSON.stringify(attachment);
		const current = () => sameAttachment() && tracked.active === active;
		const previousUncertainty = tracked.uncertain;
		const total = setTimeout(() => active.abort.abort(), SIDE_CLOSE_OPERATION_TIMEOUT_MS);
		let deleteTimer: ReturnType<typeof setTimeout> | undefined;
		let response: Response | undefined;
		const discard = (value: Response | undefined) => { try { void value?.body?.cancel().catch(() => undefined); } catch { /* a consumed/locked response has no ownership significance */ } };
		try {
			if (!attachment) throw new Error("Side chat daemon is unavailable");
			if (this.record(tracked.entry.id)) { this.options.ledger.begin(tracked.entry.id); this.ownershipChanged(); }
			const prepareDeadline = Date.now() + PREPARE_MS;
			const preparing = () => current() && !active.abort.signal.aborted && Date.now() < prepareDeadline;
			await bounded((async () => {
				await this.options.ensureClaimed();
				if (!preparing()) throw new Error("Side close preparation expired");
				const captured = await this.options.capture();
				if (!preparing()) throw new Error("Side close preparation expired");
				if (captured.attachment.generation !== attachment.generation) throw new Error("Side chat attachment changed");
				const record = (captured.state.sides as RecoveryRecord[]).find(record => record.side.id === tracked.entry.id);
				if (record && record.side.sessionId !== tracked.entry.sessionId) throw new Error("Side chat belongs to another session");
				if (record) this.updateLabel(tracked, record.side.label);
				if (this.record(tracked.entry.id)) this.options.ledger.begin(tracked.entry.id);
				this.ownershipChanged();
			})(), PREPARE_MS, active.abort.signal);
			if (!current() || active.abort.signal.aborted) throw new Error("Side close request expired");
			active.dispatched = true;
			tracked.uncertain = true;
			deleteTimer = setTimeout(() => active.abort.abort(), DELETE_MS);
			const request = (this.options.fetch ?? fetch)(`http://127.0.0.1:${attachment.port}/api/v1/sessions/${encodeURIComponent(tracked.entry.sessionId)}/conversation/side-chats/${encodeURIComponent(tracked.entry.id)}`, { method: "DELETE", redirect: "error", signal: active.abort.signal });
			void request.then(value => {
				// A late positive result is still proof for this exact attachment,
				// even if a renderer/watchdog already settled the caller.
				if (value.status === 204) { if (sameAttachment()) this.closed(tracked); discard(value); }
				else if (!current() || active.abort.signal.aborted) discard(value);
			}, () => undefined);
			response = await bounded(request, DELETE_MS, active.abort.signal);
			if (!current()) return;
			if (response.status === 204) return;
			let code: string | undefined;
			try {
				const body = await bounded(response.json(), READ_MS, active.abort.signal) as { code?: unknown; requestId?: unknown };
				if (typeof body.code === "string") code = body.code;
				tracked.entry.error = { code, requestId: typeof body.requestId === "string" ? body.requestId : undefined };
			} catch { /* malformed response is not rejection evidence */ }
			clearTimeout(deleteTimer);
			deleteTimer = undefined;
			const captured = await bounded(this.options.capture(), READ_MS, active.abort.signal);
			if (!current()) return;
			if (tracked.entry.status === "closed") return;
			if (response.status === 404 && code === "CHAT_SIDE_CLOSED") {
				const outcome = this.options.ledger.reconcileMissing(tracked.entry.id, captured.state, captured.coverage);
				if (outcome === "closed") { this.closed(tracked); return; }
			}
			const pending = this.options.ledger.pending(tracked.entry.sessionId).find(side => side.id === tracked.entry.id);
			if (pending?.phase === "cleanup_pending") { this.update(tracked, "cleanup_pending"); return; }
			if (code && rejectedStatuses.has(response.status) && !previousUncertainty) {
				const list = await bounded((this.options.fetch ?? fetch)(`http://127.0.0.1:${attachment.port}/api/v1/sessions/${encodeURIComponent(tracked.entry.sessionId)}/conversation/side-chats`, { redirect: "error", signal: active.abort.signal }), READ_MS, active.abort.signal);
				const body = list.status === 200 ? await bounded(list.json(), READ_MS, active.abort.signal) as { sides?: { id?: string; sessionId?: string }[] } : undefined;
				const open = body?.sides?.some(side => side.id === tracked.entry.id && side.sessionId === tracked.entry.sessionId);
				const record = (captured.state.sides as RecoveryRecord[]).find(record => record.side.id === tracked.entry.id && record.side.sessionId === tracked.entry.sessionId && !record.cleanupOnly);
				if (current() && open && record && this.options.ledger.cancel(tracked.entry.id, record)) {
					tracked.uncertain = false;
					this.update(tracked, "rejected_open");
					try { this.ownershipChanged(); } catch { /* verified cancellation stays usable */ }
					return;
				}
			}
			this.update(tracked, "unconfirmed");
		} catch {
			if (current() && tracked.entry.status !== "closed") {
				tracked.uncertain ||= active.dispatched;
				const pending = this.options.ledger.pending(tracked.entry.sessionId).find(side => side.id === tracked.entry.id);
				this.update(tracked, pending?.phase === "cleanup_pending" ? "cleanup_pending" : "unconfirmed");
			}
		} finally {
			clearTimeout(total);
			if (deleteTimer) clearTimeout(deleteTimer);
			active.abort.abort();
			discard(response);
			if (tracked.active === active) tracked.active = undefined;
			this.reconcile();
		}
	}
}
