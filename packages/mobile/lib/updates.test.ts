import { describe, expect, it, vi } from "vitest";
import {
	checkAndDownload,
	CHECK_STALE_MS,
	describeUpdateRow,
	latestTime,
	onForeground,
	REOFFER_MS,
	shouldOfferUpdate,
	type UpdatesApi,
} from "./updates";

function api(over: Partial<UpdatesApi> = {}): UpdatesApi {
	return {
		isEnabled: true,
		checkForUpdateAsync: vi.fn(async () => ({ isAvailable: false, isRollBackToEmbedded: false }) as never),
		fetchUpdateAsync: vi.fn(async () => ({ isNew: false, isRollBackToEmbedded: false }) as never),
		...over,
	};
}

describe("onForeground", () => {
	const t0 = 1_000_000_000;
	const idle = { pending: false, lastCheckAt: t0, lastOfferedAt: null };
	const NONE = { prompt: false, check: false };
	const CHECK = { prompt: false, check: true };

	it("checks again once the last check is stale, however short the absence", () => {
		expect(onForeground({ ...idle, now: t0 + CHECK_STALE_MS - 1 })).toEqual(NONE);
		expect(onForeground({ ...idle, now: t0 + CHECK_STALE_MS })).toEqual(CHECK);
	});

	it("checks when nothing has checked yet this run", () => {
		expect(onForeground({ ...idle, lastCheckAt: null, now: t0 })).toEqual(CHECK);
	});

	it("offers a put-off update again once a day, not on every return", () => {
		const offered = { pending: true, lastCheckAt: t0, lastOfferedAt: t0 };
		expect(onForeground({ ...offered, now: t0 + 60_000 })).toEqual(NONE);
		// A stale check still runs in between: a newer update may be out.
		expect(onForeground({ ...offered, now: t0 + CHECK_STALE_MS })).toEqual(CHECK);
		expect(onForeground({ ...offered, now: t0 + REOFFER_MS - 1 })).toEqual(CHECK);
		expect(onForeground({ ...offered, lastCheckAt: t0 + REOFFER_MS - 1, now: t0 + REOFFER_MS })).toEqual({
			prompt: true,
			check: false,
		});
	});

	it("still checks on the day it offers a put-off update again", () => {
		// The re-offer and a due check are independent: returning once a day must
		// still find an update newer than the one being offered.
		expect(onForeground({ pending: true, lastCheckAt: t0, lastOfferedAt: t0, now: t0 + REOFFER_MS })).toEqual({
			prompt: true,
			check: true,
		});
	});

	it("leaves a pending update that was never offered to shouldOfferUpdate", () => {
		expect(onForeground({ pending: true, lastCheckAt: t0, lastOfferedAt: null, now: t0 + REOFFER_MS })).toEqual(CHECK);
	});
});

describe("latestTime", () => {
	it("picks the later known time", () => {
		expect(latestTime(1, 2)).toBe(2);
		expect(latestTime(3, null)).toBe(3);
		expect(latestTime(undefined, 4)).toBe(4);
		expect(latestTime(null, undefined)).toBeNull();
	});
});

describe("checkAndDownload", () => {
	it("does nothing in builds where updates are off", async () => {
		const a = api({ isEnabled: false });
		expect(await checkAndDownload(a)).toEqual({ kind: "disabled" });
		expect(a.checkForUpdateAsync).not.toHaveBeenCalled();
	});

	it("reports up to date without downloading", async () => {
		const a = api();
		expect(await checkAndDownload(a)).toEqual({ kind: "up-to-date" });
		expect(a.fetchUpdateAsync).not.toHaveBeenCalled();
	});

	it("downloads an available update but never reloads", async () => {
		const a = api({
			checkForUpdateAsync: vi.fn(async () => ({ isAvailable: true, isRollBackToEmbedded: false }) as never),
			fetchUpdateAsync: vi.fn(async () => ({ isNew: true, isRollBackToEmbedded: false }) as never),
		});
		expect(await checkAndDownload(a)).toEqual({ kind: "downloaded" });
	});

	it("treats a rollback directive as something to restart into", async () => {
		const a = api({
			checkForUpdateAsync: vi.fn(async () => ({ isAvailable: false, isRollBackToEmbedded: true }) as never),
			fetchUpdateAsync: vi.fn(async () => ({ isNew: false, isRollBackToEmbedded: true }) as never),
		});
		expect(await checkAndDownload(a)).toEqual({ kind: "downloaded" });
	});

	it("is up to date when the server's update was already downloaded", async () => {
		const a = api({
			checkForUpdateAsync: vi.fn(async () => ({ isAvailable: true, isRollBackToEmbedded: false }) as never),
		});
		expect(await checkAndDownload(a)).toEqual({ kind: "up-to-date" });
	});

	it("returns errors instead of throwing", async () => {
		const boom = new Error("offline");
		const a = api({ checkForUpdateAsync: vi.fn(async () => { throw boom; }) });
		expect(await checkAndDownload(a)).toEqual({ kind: "error", error: boom });
		const b = api({
			checkForUpdateAsync: vi.fn(async () => ({ isAvailable: true, isRollBackToEmbedded: false }) as never),
			fetchUpdateAsync: vi.fn(async () => { throw boom; }),
		});
		expect(await checkAndDownload(b)).toEqual({ kind: "error", error: boom });
	});

	it("shares one request between concurrent callers", async () => {
		let release: (v: never) => void = () => {};
		const a = api();
		// First call hangs until released; later calls resolve normally.
		vi.mocked(a.checkForUpdateAsync).mockImplementationOnce(() => new Promise<never>((r) => { release = r; }));
		const first = checkAndDownload(a);
		const second = checkAndDownload(a);
		expect(second).toBe(first);
		release({ isAvailable: false, isRollBackToEmbedded: false } as never);
		expect(await first).toEqual({ kind: "up-to-date" });
		expect(a.checkForUpdateAsync).toHaveBeenCalledTimes(1);
		// Once settled, the next call is a fresh request.
		await checkAndDownload(a);
		expect(a.checkForUpdateAsync).toHaveBeenCalledTimes(2);
	});
});

describe("describeUpdateRow", () => {
	const idle = { enabled: true, pending: false, phase: "idle" as const, lastManual: null };

	it("is inert in development builds, whatever else is going on", () => {
		expect(describeUpdateRow({ ...idle, enabled: false, pending: true })).toEqual({
			value: "Off in development builds",
			tone: "default",
			busy: false,
			action: null,
		});
	});

	it("offers a restart as soon as an update is downloaded", () => {
		expect(describeUpdateRow({ ...idle, pending: true, phase: "checking" }).action).toBe("restart");
	});

	it("shows progress without an action", () => {
		expect(describeUpdateRow({ ...idle, phase: "checking" })).toMatchObject({ value: "Checking…", busy: true, action: null });
		expect(describeUpdateRow({ ...idle, phase: "downloading" })).toMatchObject({ value: "Downloading…", busy: true, action: null });
	});

	it("reports the last manual check and stays tappable", () => {
		expect(describeUpdateRow({ ...idle, lastManual: { kind: "up-to-date" } })).toEqual({
			value: "Up to date",
			tone: "good",
			busy: false,
			action: "check",
		});
		expect(describeUpdateRow({ ...idle, lastManual: { kind: "error", error: null } })).toEqual({
			value: "Couldn't check",
			tone: "bad",
			busy: false,
			action: "check",
		});
		expect(describeUpdateRow(idle)).toEqual({ value: "Check now", tone: "default", busy: false, action: "check" });
	});
});

describe("shouldOfferUpdate", () => {
	it("offers a downloaded update once", () => {
		expect(shouldOfferUpdate(true, "u1", null)).toBe(true);
		expect(shouldOfferUpdate(true, "u1", "u1")).toBe(false);
	});

	it("offers a newer update even after the last one was put off", () => {
		expect(shouldOfferUpdate(true, "u2", "u1")).toBe(true);
	});

	it("offers nothing until an update is downloaded", () => {
		expect(shouldOfferUpdate(false, undefined, null)).toBe(false);
		expect(shouldOfferUpdate(false, "u1", null)).toBe(false);
	});

	it("waits while the user is onboarding or pairing, and offers once they leave", () => {
		expect(shouldOfferUpdate(true, "u1", null, "/onboarding")).toBe(false);
		expect(shouldOfferUpdate(true, "u1", null, "/pair")).toBe(false);
		expect(shouldOfferUpdate(true, "u1", null, "/")).toBe(true);
		expect(shouldOfferUpdate(true, "u1", null, "/session/abc")).toBe(true);
	});

	it("keys a rollback without an id as one update", () => {
		expect(shouldOfferUpdate(true, undefined, null)).toBe(true);
		expect(shouldOfferUpdate(true, undefined, "embedded")).toBe(false);
	});
});
