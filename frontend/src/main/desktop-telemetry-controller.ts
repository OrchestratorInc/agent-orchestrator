import { AGENT_SWITCH_FAILURE_PRODUCTION_ENABLED, type RendererTelemetryCapture, type TelemetryPolicySnapshot, type TelemetryPolicyView } from "../shared/telemetry-policy";

const RETRY_BACKOFF_INIT_MS = 2_000;
const RETRY_BACKOFF_MAX_MS = 60_000;
const RETRY_BACKOFF_MAX_EXPONENT = 31;

export function telemetryRetryDelayMs(failures: number): number {
	const attempt = Number.isFinite(failures) ? Math.min(Math.max(Math.floor(failures), 1), RETRY_BACKOFF_MAX_EXPONENT) : 1;
	return Math.min(RETRY_BACKOFF_INIT_MS * 2 ** (attempt - 1), RETRY_BACKOFF_MAX_MS);
}

export type DesktopTelemetryTransport = {
	closeAndDrain(): Promise<void>;
	clearCache(): Promise<void>;
	capture(request: RendererTelemetryCapture): void;
};

type Authority = {
	durabilitySupported: boolean;
	load(): Promise<TelemetryPolicySnapshot>;
	snapshot(): TelemetryPolicySnapshot;
	setEventsEnabled(enabled: boolean): Promise<TelemetryPolicySnapshot>;
	retryPendingReplacement(): Promise<TelemetryPolicySnapshot>;
};

export class DesktopTelemetryController {
	private view: TelemetryPolicyView;
	private transport: DesktopTelemetryTransport | null = null;
	private operation: Promise<TelemetryPolicyView>;
	private pendingDesktopCleanup: (() => Promise<boolean>) | null = null;
	private retryFailures = 0;
	private nextRetryAtMs = 0;

	constructor(private readonly options: {
		authority: Authority;
		transportFactory: () => Promise<DesktopTelemetryTransport | null>;
		environmentAllowsEvents: boolean;
		productionEnabled?: boolean;
		now?: () => number;
		broadcast?: (view: TelemetryPolicyView) => void;
		clearRendererQueues?: () => Promise<void>;
	}) {
		this.view = this.toView(options.authority.snapshot(), "applied");
		this.operation = Promise.resolve(this.view);
	}

	snapshot(): TelemetryPolicyView { return { ...this.view }; }

	initialize(): Promise<TelemetryPolicyView> {
		return this.serialize(async () => {
			const snapshot = await this.options.authority.load();
			this.view = this.toView(snapshot, snapshot.acknowledged ? "applied" : "cleanup_failed");
			if (snapshot.acknowledged && this.captureEnabled(snapshot)) this.transport = await this.options.transportFactory();
			this.publish();
			return this.snapshot();
		});
	}

	setEventsEnabled(enabled: boolean, expectedGeneration: string): Promise<TelemetryPolicyView> {
		return this.serialize(async () => {
			if (expectedGeneration !== this.view.consentGeneration) throw new Error("stale telemetry consent generation");
			this.resetRetryBackoff();
			return enabled ? this.enable() : this.disable();
		});
	}

	async retryPendingCleanup(): Promise<TelemetryPolicyView> {
		return this.serialize(async () => {
			if (this.view.state === "applied" || !this.options.authority.durabilitySupported) return this.snapshot();
			if (this.clock() < this.nextRetryAtMs) return this.snapshot();
			let authorityVerified = false;
			try {
				let snapshot = this.options.authority.snapshot();
				if (!snapshot.acknowledged) snapshot = await this.options.authority.retryPendingReplacement();
				authorityVerified = snapshot.acknowledged &&
					snapshot.consentGeneration === this.view.consentGeneration &&
					snapshot.eventsEnabled === this.view.eventsEnabled;
				if (!authorityVerified) throw new Error("durable policy authority was not acknowledged");
				if (!snapshot.eventsEnabled && this.pendingDesktopCleanup && !(await this.pendingDesktopCleanup())) throw new Error("desktop cleanup is incomplete");
				this.pendingDesktopCleanup = null;
				this.view = this.toView(snapshot, "applied", this.baseReason());
				if (this.captureEnabled(this.view) && !this.transport) this.transport = await this.options.transportFactory();
				this.resetRetryBackoff();
			} catch {
				this.retryFailures += 1;
				this.nextRetryAtMs = this.clock() + telemetryRetryDelayMs(this.retryFailures);
				this.view = {
					...this.view,
					state: authorityVerified ? "cleanup_pending" : "cleanup_failed",
					acknowledged: false,
					reason: "cleanup_failed",
				};
			}
			this.publish(); return this.snapshot();
		});
	}

	capture(request: RendererTelemetryCapture): boolean {
		if (!this.captureEnabled(this.view) || request.consentGeneration !== this.view.consentGeneration || !this.transport) return false;
		this.transport.capture(request); return true;
	}

	async close(): Promise<void> {
		const transport = this.transport;
		this.transport = null;
		await transport?.closeAndDrain();
	}

	private async disable(): Promise<TelemetryPolicyView> {
		const closingTransport = this.transport;
		let transportDrained = true;
		try { await closingTransport?.closeAndDrain(); } catch { transportDrained = false; }
		this.transport = null;
		this.pendingDesktopCleanup = async () => {
			let purged = true;
			if (!transportDrained) {
				try { await closingTransport?.closeAndDrain(); transportDrained = true; } catch { purged = false; }
			}
			try { await closingTransport?.clearCache(); } catch { purged = false; }
			try { await this.options.clearRendererQueues?.(); } catch { purged = false; }
			return purged;
		};
		let snapshot: TelemetryPolicySnapshot;
		try { snapshot = await this.options.authority.setEventsEnabled(false); }
		catch (error) {
			snapshot = { ...this.options.authority.snapshot(), eventsEnabled: false, acknowledged: false };
			this.view = this.toView(snapshot, "cleanup_failed", "cleanup_failed");
			this.publish();
			throw error;
		}
		const desktopPurged = await this.pendingDesktopCleanup();
		if (desktopPurged) this.pendingDesktopCleanup = null;
		const cleanupApplied = desktopPurged;
		this.view = this.toView({ ...snapshot, acknowledged: snapshot.acknowledged && cleanupApplied }, cleanupApplied ? "applied" : "cleanup_pending", cleanupApplied ? this.baseReason() : "cleanup_failed");
		this.publish(); return this.snapshot();
	}

	private async enable(): Promise<TelemetryPolicyView> {
		if (!this.options.authority.durabilitySupported) throw new Error("telemetry enablement is unavailable because durable policy replacement is unsupported");
		if (!this.options.environmentAllowsEvents) { this.view = { ...this.view, environmentVeto: true, reason: "environment_veto" }; this.publish(); return this.snapshot(); }
		if (!this.view.eventsEnabled && this.view.state !== "applied") {
			if (this.pendingDesktopCleanup && !(await this.pendingDesktopCleanup())) throw new Error("prior desktop telemetry purge is incomplete");
			this.pendingDesktopCleanup = null;
		}
		const snapshot = await this.options.authority.setEventsEnabled(true);
		let transport: DesktopTelemetryTransport | null = null;
		try {
			if (this.captureEnabled(snapshot)) {
				transport = await this.options.transportFactory();
				if (!transport) throw new Error("desktop telemetry transport is unavailable");
			}
		} catch (error) {
			// If enabling fails after durable state changes, reuse the full opt-out
			// path so no process can retain ambiguous enablement.
			this.view = this.toView({ ...snapshot, acknowledged: false }, "cleanup_pending", "cleanup_failed");
			await this.disable();
			throw error;
		}
		this.transport = transport;
		this.view = this.toView(snapshot, "applied", this.baseReason()); this.publish(); return this.snapshot();
	}

	private captureEnabled(snapshot: Pick<TelemetryPolicySnapshot, "eventsEnabled" | "acknowledged">): boolean {
		return snapshot.eventsEnabled && snapshot.acknowledged && this.options.authority.durabilitySupported && this.options.environmentAllowsEvents && (this.options.productionEnabled ?? AGENT_SWITCH_FAILURE_PRODUCTION_ENABLED);
	}

	private toView(snapshot: TelemetryPolicySnapshot, state: TelemetryPolicyView["state"], reason = this.baseReason()): TelemetryPolicyView {
		return { ...snapshot, state, environmentVeto: !this.options.environmentAllowsEvents, durabilitySupported: this.options.authority.durabilitySupported, reason };
	}

	private baseReason(): TelemetryPolicyView["reason"] {
		if (!this.options.authority.durabilitySupported) return "durability_unsupported";
		if (!this.options.environmentAllowsEvents) return "environment_veto";
		if (!(this.options.productionEnabled ?? AGENT_SWITCH_FAILURE_PRODUCTION_ENABLED)) return "release_blocked";
		return undefined;
	}

	private clock(): number {
		return (this.options.now ?? Date.now)();
	}

	private resetRetryBackoff(): void {
		this.retryFailures = 0;
		this.nextRetryAtMs = 0;
	}

	private publish(): void {
		this.options.broadcast?.(this.snapshot());
	}

	private serialize(operation: () => Promise<TelemetryPolicyView>): Promise<TelemetryPolicyView> {
		const next = this.operation.catch(() => this.snapshot()).then(operation);
		this.operation = next; return next;
	}
}
