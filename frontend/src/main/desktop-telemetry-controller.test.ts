import { mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import { telemetryPolicyRetryable, type TelemetryPolicySnapshot } from "../shared/telemetry-policy";
import { DesktopTelemetryController, telemetryRetryDelayMs } from "./desktop-telemetry-controller";
import { nodeTelemetryPolicyFileSystem, TelemetryPolicyAuthority } from "./telemetry-policy-file";

describe("DesktopTelemetryController", () => {
	it("blocks a later opt-in until a failed startup queue purge succeeds", async () => {
		const authority = new AuthorityFake(false, "generation-off");
		const clearTransportCache = vi.fn().mockRejectedValueOnce(new Error("queue purge failed")).mockRejectedValueOnce(new Error("queue purge failed")).mockResolvedValue(undefined);
		const transportFactory = vi.fn().mockResolvedValue({ closeAndDrain: async () => {}, capture: () => {}, clearCache: async () => {} });
		const controller = new DesktopTelemetryController({ authority, clearTransportCache, transportFactory, environmentAllowsEvents: true, productionEnabled: true });

		await controller.initialize();
		expect(controller.snapshot()).toMatchObject({ eventsEnabled: false, state: "cleanup_pending", acknowledged: false });
		await expect(controller.setEventsEnabled(true, "generation-off")).rejects.toThrow("prior desktop telemetry purge is incomplete");
		expect(authority.writes).toEqual([]);
		expect(transportFactory).not.toHaveBeenCalled();
		await controller.setEventsEnabled(true, "generation-off");
		expect(clearTransportCache).toHaveBeenCalledTimes(3);
		expect(transportFactory).toHaveBeenCalledOnce();
	});

	it("keeps a failed startup purge when the user confirms sharing stays off", async () => {
		const authority = new AuthorityFake(false, "generation-off");
		const clearTransportCache = vi.fn().mockRejectedValueOnce(new Error("queue purge failed")).mockResolvedValue(undefined);
		const controller = new DesktopTelemetryController({
			authority,
			clearTransportCache,
			transportFactory: vi.fn(),
			environmentAllowsEvents: true,
			productionEnabled: true,
		});
		await controller.initialize();
		expect(controller.snapshot().state).toBe("cleanup_pending");
		const result = await controller.setEventsEnabled(false, "generation-off");
		expect(result).toMatchObject({ eventsEnabled: false, acknowledged: true, state: "applied" });
		expect(clearTransportCache).toHaveBeenCalledTimes(3);
	});

	it("keeps an enabled policy's offline queue available to its sender at startup", async () => {
		const authority = new AuthorityFake(true, "generation-on");
		const clearTransportCache = vi.fn();
		const transportFactory = vi.fn().mockResolvedValue({ closeAndDrain: async () => {}, capture: () => {}, clearCache: async () => {} });
		const controller = new DesktopTelemetryController({ authority, clearTransportCache, transportFactory, environmentAllowsEvents: true, productionEnabled: true });
		await controller.initialize();
		expect(clearTransportCache).not.toHaveBeenCalled();
		expect(transportFactory).toHaveBeenCalledOnce();
	});

	it("keeps opt-out cleanup pending when any desktop purge fails", async () => {
		const authority = new AuthorityFake(true, "generation-on");
		const transport = { closeAndDrain: vi.fn(), capture: vi.fn(), clearCache: vi.fn().mockRejectedValue(new Error("cache purge failed")) };
		const controller = new DesktopTelemetryController({ authority, transportFactory: async () => transport, environmentAllowsEvents: true, productionEnabled: true, clearRendererQueues: vi.fn().mockRejectedValue(new Error("queue purge failed")) });
		await controller.initialize();
		const result = await controller.setEventsEnabled(false, "generation-on");
		expect(result).toMatchObject({ eventsEnabled: false, state: "cleanup_pending", acknowledged: false, reason: "cleanup_failed" });
	});

	it("fails closed in memory when the durable off replacement fails", async () => {
		const authority = new AuthorityFake(true, "generation-on");
		authority.failWrites = true;
		const controller = new DesktopTelemetryController({ authority, transportFactory: async () => ({ closeAndDrain: async () => {}, capture: () => {}, clearCache: async () => {} }), environmentAllowsEvents: true, productionEnabled: true });
		await controller.initialize();
		await expect(controller.setEventsEnabled(false, "generation-on")).rejects.toThrow("write failed");
		expect(controller.snapshot()).toMatchObject({ eventsEnabled: false, acknowledged: false, state: "cleanup_failed" });
	});

	it("retries the durable off generation and preserved desktop purge after a post-rename fsync failure", async () => {
		const dataDir = await mkdtemp(path.join(os.tmpdir(), "ao-controller-policy-"));
		try {
			const enabledGeneration = "7f80c8a9-ec67-4a16-a067-a444ffcc5cca";
			await writeFile(path.join(dataDir, "telemetry_policy.json"), `${JSON.stringify({
				schema_version: 2,
				events_enabled: true,
				consent_generation: enabledGeneration,
				consent_production_enabled: true,
				updated_at: "2026-08-28T10:15:30.000Z",
			})}\n`, { mode: 0o600 });
			let failDirectorySync = false;
			const base = nodeTelemetryPolicyFileSystem;
			const authority = new TelemetryPolicyAuthority({
				dataDir,
				packagedDefault: false,
				platform: "linux",
				fs: {
					...base,
					syncDirectory: async (target) => {
						if (failDirectorySync) {
							failDirectorySync = false;
							throw new Error("directory fsync failed");
						}
						await base.syncDirectory(target);
					},
				},
			});
			const transport = { closeAndDrain: vi.fn(), capture: vi.fn(), clearCache: vi.fn() };
			const clearRendererQueues = vi.fn();
			const controller = new DesktopTelemetryController({
				authority,
				transportFactory: async () => transport,
				environmentAllowsEvents: true,
				productionEnabled: true,
				clearRendererQueues,
			});
			await controller.initialize();
			failDirectorySync = true;

			await expect(controller.setEventsEnabled(false, enabledGeneration)).rejects.toThrow("directory fsync failed");
			const pending = authority.snapshot();
			expect(pending).toMatchObject({ eventsEnabled: false, acknowledged: false });
			expect(transport.clearCache).not.toHaveBeenCalled();
			expect(clearRendererQueues).not.toHaveBeenCalled();

			const retried = await controller.retryPendingCleanup();

			expect(retried).toMatchObject({
				eventsEnabled: false,
				consentGeneration: pending.consentGeneration,
				acknowledged: true,
				state: "applied",
			});
			expect(authority.snapshot()).toMatchObject({ consentGeneration: pending.consentGeneration, acknowledged: true });
			expect(transport.clearCache).toHaveBeenCalledOnce();
			expect(clearRendererQueues).toHaveBeenCalledOnce();
		} finally {
			await rm(dataDir, { recursive: true, force: true });
		}
	});

	it("drains the transport before writing one durable off generation, then purges the desktop cache", async () => {
		const authority = new AuthorityFake(true, "generation-on");
		const transport = { closeAndDrain: vi.fn(), capture: vi.fn(), clearCache: vi.fn() };
		const controller = new DesktopTelemetryController({ authority, transportFactory: async () => transport, environmentAllowsEvents: true, productionEnabled: true });
		await controller.initialize();
		const result = await controller.setEventsEnabled(false, "generation-on");
		expect(authority.writes).toEqual([false]);
		expect(transport.closeAndDrain.mock.invocationCallOrder[0]).toBeLessThan(authority.writeSpy.mock.invocationCallOrder[0]);
		expect(authority.writeSpy.mock.invocationCallOrder[0]).toBeLessThan(transport.clearCache.mock.invocationCallOrder[0]);
		expect(result).toMatchObject({ eventsEnabled: false, consentGeneration: "generation-1", state: "applied", acknowledged: true });
	});

	it("rejects stale renderer generations and broadcasts disable then enable to the same subscriber", async () => {
		const authority = new AuthorityFake(false, "generation-off");
		const views: string[] = [];
		const controller = new DesktopTelemetryController({ authority, transportFactory: async () => ({ closeAndDrain: async () => {}, capture: () => {}, clearCache: async () => {} }), environmentAllowsEvents: true, productionEnabled: true, broadcast: (view) => views.push(view.consentGeneration) });
		await controller.initialize();
		await expect(controller.setEventsEnabled(true, "stale")).rejects.toThrow("stale");
		expect(authority.writes).toEqual([]);
		await controller.setEventsEnabled(true, "generation-off");
		expect(views.at(-1)).toBe("generation-1");
	});

	it("rolls a durable enablement back off when the main transport cannot start", async () => {
		const authority = new AuthorityFake(false, "generation-off");
		const transportFactory = vi.fn().mockRejectedValue(new Error("transport start failed"));
		const controller = new DesktopTelemetryController({ authority, transportFactory, environmentAllowsEvents: true, productionEnabled: true });
		await controller.initialize();

		await expect(controller.setEventsEnabled(true, "generation-off")).rejects.toThrow("transport start failed");

		expect(authority.writes).toEqual([true, false]);
		expect(controller.snapshot()).toMatchObject({ eventsEnabled: false, consentGeneration: "generation-2", acknowledged: true, state: "applied" });
		expect(controller.capture({ consentGeneration: "generation-2", kind: "message", message: "not sent" })).toBe(false);
	});

	it("settles a saved opt-in as release-blocked, without a transport or retries, when the release gate refuses enablement", async () => {
		const authority = new AuthorityFake(true, "generation-on");
		const transportFactory = vi.fn().mockResolvedValue({ closeAndDrain: async () => {}, capture: () => {}, clearCache: async () => {} });
		const broadcast = vi.fn();
		const controller = new DesktopTelemetryController({
			authority,
			transportFactory,
			environmentAllowsEvents: true, productionEnabled: false,
			broadcast,
		});

		await controller.initialize();
		expect(controller.snapshot()).toMatchObject({ state: "applied", reason: "release_blocked", eventsEnabled: true, acknowledged: true });
		expect(transportFactory).not.toHaveBeenCalled();
		expect(controller.capture({ consentGeneration: "generation-on", kind: "message", message: "not sent" })).toBe(false);
		expect(broadcast).toHaveBeenCalledTimes(1);

		for (let i = 0; i < 60; i += 1) await controller.retryPendingCleanup();
		expect(broadcast).toHaveBeenCalledTimes(1);
		expect(transportFactory).not.toHaveBeenCalled();
		expect(authority.writes).toEqual([]);
	});

	it("stays terminal on a platform without durable policy replacement", async () => {
		const authority = new TelemetryPolicyAuthority({
			dataDir: path.join(os.tmpdir(), "ao-controller-win32-unused"),
			packagedDefault: false,
			platform: "win32",
		});
		const controller = new DesktopTelemetryController({
			authority,
			transportFactory: async () => null,
			environmentAllowsEvents: true, productionEnabled: false,
		});

		await controller.initialize();
		expect(controller.snapshot()).toMatchObject({ state: "cleanup_failed", reason: "durability_unsupported", durabilitySupported: false });

		await controller.retryPendingCleanup();
		expect(controller.snapshot()).toMatchObject({ state: "cleanup_failed", reason: "durability_unsupported" });
		expect(telemetryPolicyRetryable(controller.snapshot())).toBe(false);
	});

	it("asks again for an opt-in given while the release gate was closed once a release opens it", async () => {
		const dataDir = await mkdtemp(path.join(os.tmpdir(), "ao-controller-gate-opens-"));
		try {
			const gatedGeneration = "7f80c8a9-ec67-4a16-a067-a444ffcc5cca";
			await writeFile(path.join(dataDir, "telemetry_policy.json"), `${JSON.stringify({
				schema_version: 1,
				events_enabled: true,
				consent_generation: gatedGeneration,
				updated_at: "2026-08-28T10:15:30.000Z",
			})}\n`, { mode: 0o600 });
			const boot = () => new DesktopTelemetryController({
				authority: new TelemetryPolicyAuthority({ dataDir, packagedDefault: true, platform: "linux", productionEnabled: true }),
				transportFactory: async () => ({ closeAndDrain: async () => {}, capture: () => {}, clearCache: async () => {} }),
				environmentAllowsEvents: true, productionEnabled: true,
			});

			const first = boot();
			await first.initialize();
			expect(first.snapshot()).toMatchObject({ eventsEnabled: false, consentRenewalRequired: true, state: "applied", acknowledged: true, consentGeneration: gatedGeneration });
			expect(first.capture({ consentGeneration: gatedGeneration, kind: "message", message: "not sent" })).toBe(false);

			const optedIn = await first.setEventsEnabled(true, gatedGeneration);
			expect(optedIn).toMatchObject({ eventsEnabled: true, consentRenewalRequired: false, state: "applied" });

			const second = boot();
			await second.initialize();
			expect(second.snapshot()).toMatchObject({ eventsEnabled: true, state: "applied", consentGeneration: optedIn.consentGeneration });
			expect(second.capture({ consentGeneration: optedIn.consentGeneration, kind: "message", message: "sent" })).toBe(true);
		} finally {
			await rm(dataDir, { recursive: true, force: true });
		}
	});

	it("stops asking once the user declines the renewed opt-in, and keeps it off", async () => {
		const dataDir = await mkdtemp(path.join(os.tmpdir(), "ao-controller-renewal-declined-"));
		try {
			const gatedGeneration = "7f80c8a9-ec67-4a16-a067-a444ffcc5cca";
			await writeFile(path.join(dataDir, "telemetry_policy.json"), `${JSON.stringify({
				schema_version: 1,
				events_enabled: true,
				consent_generation: gatedGeneration,
				updated_at: "2026-08-28T10:15:30.000Z",
			})}\n`, { mode: 0o600 });
			const boot = () => new DesktopTelemetryController({
				authority: new TelemetryPolicyAuthority({ dataDir, packagedDefault: true, platform: "linux", productionEnabled: true }),
				transportFactory: async () => ({ closeAndDrain: async () => {}, capture: () => {}, clearCache: async () => {} }),
				environmentAllowsEvents: true, productionEnabled: true,
			});

			const first = boot();
			await first.initialize();
			expect(first.snapshot()).toMatchObject({ eventsEnabled: false, consentRenewalRequired: true });

			const declined = await first.setEventsEnabled(false, gatedGeneration);
			expect(declined).toMatchObject({ eventsEnabled: false, consentRenewalRequired: false, state: "applied" });

			const second = boot();
			await second.initialize();
			expect(second.snapshot()).toMatchObject({ eventsEnabled: false, consentRenewalRequired: false, consentGeneration: declined.consentGeneration });
		} finally {
			await rm(dataDir, { recursive: true, force: true });
		}
	});

	it("paces retries of a desktop purge that keeps failing instead of once a second", async () => {
		const authority = new AuthorityFake(true, "generation-on");
		let clock = 0;
		const attempts: number[] = [];
		const controller = new DesktopTelemetryController({
			authority,
			transportFactory: async () => null,
			environmentAllowsEvents: true, productionEnabled: false,
			now: () => clock,
			clearRendererQueues: vi.fn().mockImplementation(async () => { attempts.push(clock); throw new Error("queue purge failed"); }),
		});

		await controller.initialize();
		await controller.setEventsEnabled(false, "generation-on");
		expect(controller.snapshot()).toMatchObject({ state: "cleanup_pending", reason: "cleanup_failed" });
		expect(telemetryPolicyRetryable(controller.snapshot())).toBe(true);
		attempts.length = 0;

		for (clock = 1_000; clock <= 3_600_000; clock += 1_000) await controller.retryPendingCleanup();

		expect(attempts.slice(0, 6)).toEqual([1_000, 3_000, 7_000, 15_000, 31_000, 63_000]);
		expect(attempts.length).toBeLessThan(70);
		expect(controller.snapshot()).toMatchObject({ state: "cleanup_pending", reason: "cleanup_failed" });
	});

	it("measures the retry delay from when a hung attempt gives up, not from when it started", async () => {
		const authority = new AuthorityFake(true, "generation-on");
		let clock = 0;
		const starts: number[] = [];
		const ends: number[] = [];
		const controller = new DesktopTelemetryController({
			authority,
			transportFactory: async () => null,
			environmentAllowsEvents: true, productionEnabled: false,
			now: () => clock,
			clearRendererQueues: vi.fn().mockImplementation(async () => {
				starts.push(clock);
				clock += 2_000;
				ends.push(clock);
				throw new Error("This operation was aborted");
			}),
		});

		await controller.initialize();
		await controller.setEventsEnabled(false, "generation-on");
		starts.length = 0;
		ends.length = 0;

		for (let tick = 0; tick < 30; tick += 1) {
			clock += 1_000;
			await controller.retryPendingCleanup();
		}

		const idleGaps = starts.slice(1).map((start, index) => start - ends[index]);
		expect(idleGaps.slice(0, 3)).toEqual([2_000, 4_000, 8_000]);
	});

	it("settles as soon as a transiently failing desktop purge succeeds again", async () => {
		const authority = new AuthorityFake(true, "generation-on");
		let clock = 0;
		let purgeable = false;
		const clearRendererQueues = vi.fn().mockImplementation(async () => {
			if (!purgeable) throw new Error("queue purge failed");
		});
		const controller = new DesktopTelemetryController({
			authority,
			transportFactory: async () => null,
			environmentAllowsEvents: true, productionEnabled: false,
			now: () => clock,
			clearRendererQueues,
		});

		await controller.initialize();
		await controller.setEventsEnabled(false, "generation-on");
		for (clock = 1_000; clock <= 20_000; clock += 1_000) await controller.retryPendingCleanup();
		expect(controller.snapshot()).toMatchObject({ state: "cleanup_pending" });

		purgeable = true;
		for (clock = 21_000; clock <= 90_000; clock += 1_000) await controller.retryPendingCleanup();
		expect(controller.snapshot()).toMatchObject({ state: "applied", reason: "release_blocked", eventsEnabled: false, acknowledged: true });

		const settled = clearRendererQueues.mock.calls.length;
		for (clock = 91_000; clock <= 150_000; clock += 1_000) await controller.retryPendingCleanup();
		expect(clearRendererQueues).toHaveBeenCalledTimes(settled);
	});

});

describe("telemetryRetryDelayMs", () => {
	it("doubles from 2s to a 60s ceiling", () => {
		expect([1, 2, 3, 4, 5, 6, 7].map(telemetryRetryDelayMs)).toEqual([2_000, 4_000, 8_000, 16_000, 32_000, 60_000, 60_000]);
	});

	it("treats a non-positive or non-finite failure count as the first failure", () => {
		expect(telemetryRetryDelayMs(0)).toBe(2_000);
		expect(telemetryRetryDelayMs(-5)).toBe(2_000);
		expect(telemetryRetryDelayMs(Number.NaN)).toBe(2_000);
		expect(telemetryRetryDelayMs(Number.POSITIVE_INFINITY)).toBe(2_000);
	});

	it("stays at the ceiling for a failure count that would overflow the exponent", () => {
		expect(telemetryRetryDelayMs(1_000)).toBe(60_000);
	});
});

class AuthorityFake {
	writes: boolean[] = [];
	failWrites = false;
	writeSpy = vi.fn();
	private current: TelemetryPolicySnapshot;
	constructor(enabled: boolean, generation: string) { this.current = { eventsEnabled: enabled, consentGeneration: generation, updatedAt: "2026-08-28T10:15:30.000Z", acknowledged: true, consentRenewalRequired: false }; }
	snapshot() { return { ...this.current }; }
	async load() { return this.snapshot(); }
	async setEventsEnabled(enabled: boolean) { this.writes.push(enabled); this.writeSpy(); if (this.failWrites) throw new Error("write failed"); this.current = { eventsEnabled: enabled, consentGeneration: `generation-${this.writes.length}`, updatedAt: "2026-08-28T10:15:31.000Z", acknowledged: true, consentRenewalRequired: false }; return this.snapshot(); }
	async retryPendingReplacement() { if (this.failWrites) throw new Error("write failed"); this.current = { ...this.current, acknowledged: true }; return this.snapshot(); }
	readonly durabilitySupported = true;
}
