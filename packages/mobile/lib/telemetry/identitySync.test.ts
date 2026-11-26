import { describe, expect, it, vi } from "vitest";

vi.mock("expo-crypto", () => ({
	CryptoDigestAlgorithm: { SHA256: "SHA-256" },
	digestStringAsync: async (_algorithm: string, value: string) =>
		(await import("node:crypto")).createHash("sha256").update(value).digest("hex"),
}));
import type { ServerConfig } from "../config";
import { createDesktopIdentitySync } from "./identitySync";
import type { DesktopTelemetryIdentity } from "./telemetry";

const cfg = (hostId: string, password = "token") => ({ hostId, host: hostId, httpPort: "3011", secure: false, password }) as ServerConfig;

function deferred<T>() {
	let resolve!: (v: T) => void;
	const promise = new Promise<T>((r) => (resolve = r));
	return { promise, resolve };
}

describe("createDesktopIdentitySync", () => {
	it("discards a slow response from an obsolete connection set", async () => {
		const a = deferred<DesktopTelemetryIdentity>();
		const adopted: (DesktopTelemetryIdentity | null)[] = [];
		const sync = createDesktopIdentitySync(
			(c) => ((c as unknown as { hostId: string }).hostId === "a" ? a.promise : Promise.resolve({ optedOut: true })),
			(i) => adopted.push(i),
		);
		const first = sync([cfg("a")]); // slow, opted in
		const second = sync([cfg("b")]); // newer, opted out
		await second;
		a.resolve({ distinctId: "ins_a", optedOut: false });
		await first;
		expect(adopted.at(-1)).toEqual({ optedOut: true });
		expect(adopted).toContain(null);
	});

	it("a sync with no connected desktops supersedes a pending one", async () => {
		const a = deferred<DesktopTelemetryIdentity>();
		const adopted: (DesktopTelemetryIdentity | null)[] = [];
		const sync = createDesktopIdentitySync(() => a.promise, (i) => adopted.push(i));
		const pending = sync([cfg("a")]);
		await sync([]);
		a.resolve({ distinctId: "ins_a", optedOut: false });
		await pending;
		expect(adopted).toEqual([null, null]);
	});

	it("opt-out from any desktop wins, otherwise the first identity", async () => {
		const adopted: (DesktopTelemetryIdentity | null)[] = [];
		const byHost: Record<string, DesktopTelemetryIdentity> = {
			a: { distinctId: "ins_a", optedOut: false },
			b: { optedOut: true },
		};
		const sync = createDesktopIdentitySync(async (c) => byHost[(c as unknown as { hostId: string }).hostId], (i) => adopted.push(i));
		await sync([cfg("a"), cfg("b")]);
		await sync([cfg("a")]);
		expect(adopted.filter(Boolean)).toEqual([{ optedOut: true }, { distinctId: "ins_a", optedOut: false }]);
	});

	it("keeps going when one desktop is unreachable", async () => {
		const adopted: (DesktopTelemetryIdentity | null)[] = [];
		const sync = createDesktopIdentitySync(async (c) => {
			if ((c as unknown as { hostId: string }).hostId === "down") throw new Error("unreachable");
			return { distinctId: "ins_up", optedOut: false };
		}, (i) => adopted.push(i));
		await sync([cfg("down"), cfg("up")]);
		expect(adopted.at(-1)).toEqual({ distinctId: "ins_up", optedOut: false });
	});

	it("clears the old paired installation before a new host responds", async () => {
		const next = deferred<DesktopTelemetryIdentity>();
		const adopted: (DesktopTelemetryIdentity | null)[] = [];
		const sync = createDesktopIdentitySync(
			(c) => ((c as unknown as { hostId: string }).hostId === "a"
				? Promise.resolve({ distinctId: "user_A", installId: "ins_a", optedOut: false })
				: next.promise),
			(i) => adopted.push(i),
		);
		await sync([cfg("a")]);
		const pending = sync([cfg("b")]);
		expect(adopted.at(-1)).toBeNull();
		next.resolve({ distinctId: "user_B", installId: "ins_b", optedOut: false });
		await pending;
		expect(adopted.at(-1)?.installId).toBe("ins_b");
		await sync([]);
		expect(adopted.at(-1)).toBeNull();
	});

	it("clears attribution when the same host is re-paired with a new credential", async () => {
		const next = deferred<DesktopTelemetryIdentity>();
		const started = deferred<void>();
		const adopted: (DesktopTelemetryIdentity | null)[] = [];
		let calls = 0;
		const sync = createDesktopIdentitySync(() => {
			calls++;
			if (calls === 1) return Promise.resolve({ distinctId: "user_A", installId: "ins_a", optedOut: false });
			started.resolve();
			return next.promise;
		}, (identity) => adopted.push(identity));
		await sync([cfg("a", "old-token")]);
		const pending = sync([cfg("a", "new-token")]);
		await started.promise;
		expect(adopted.at(-1)).toBeNull();
		next.resolve({ distinctId: "user_B", installId: "ins_b", optedOut: false });
		await pending;
		expect(adopted.at(-1)?.installId).toBe("ins_b");
	});
});
