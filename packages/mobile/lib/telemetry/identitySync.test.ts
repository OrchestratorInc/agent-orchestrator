import { describe, expect, it } from "vitest";
import type { ServerConfig } from "../config";
import { createDesktopIdentitySync } from "./identitySync";
import type { DesktopTelemetryIdentity } from "./telemetry";

const cfg = (hostId: string) => ({ hostId }) as unknown as ServerConfig;

function deferred<T>() {
	let resolve!: (v: T) => void;
	const promise = new Promise<T>((r) => (resolve = r));
	return { promise, resolve };
}

describe("createDesktopIdentitySync", () => {
	it("discards a slow response from an obsolete connection set", async () => {
		const a = deferred<DesktopTelemetryIdentity>();
		const adopted: DesktopTelemetryIdentity[] = [];
		const sync = createDesktopIdentitySync(
			(c) => ((c as unknown as { hostId: string }).hostId === "a" ? a.promise : Promise.resolve({ optedOut: true })),
			(i) => adopted.push(i),
		);
		const first = sync([cfg("a")]); // slow, opted in
		const second = sync([cfg("b")]); // newer, opted out
		await second;
		a.resolve({ distinctId: "ins_a", optedOut: false });
		await first;
		expect(adopted).toEqual([{ optedOut: true }]);
	});

	it("a sync with no connected desktops supersedes a pending one", async () => {
		const a = deferred<DesktopTelemetryIdentity>();
		const adopted: DesktopTelemetryIdentity[] = [];
		const sync = createDesktopIdentitySync(() => a.promise, (i) => adopted.push(i));
		const pending = sync([cfg("a")]);
		await sync([]);
		a.resolve({ distinctId: "ins_a", optedOut: false });
		await pending;
		expect(adopted).toEqual([]);
	});

	it("opt-out from any desktop wins, otherwise the first identity", async () => {
		const adopted: DesktopTelemetryIdentity[] = [];
		const byHost: Record<string, DesktopTelemetryIdentity> = {
			a: { distinctId: "ins_a", optedOut: false },
			b: { optedOut: true },
		};
		const sync = createDesktopIdentitySync(async (c) => byHost[(c as unknown as { hostId: string }).hostId], (i) => adopted.push(i));
		await sync([cfg("a"), cfg("b")]);
		await sync([cfg("a")]);
		expect(adopted).toEqual([{ optedOut: true }, { distinctId: "ins_a", optedOut: false }]);
	});

	it("keeps going when one desktop is unreachable", async () => {
		const adopted: DesktopTelemetryIdentity[] = [];
		const sync = createDesktopIdentitySync(async (c) => {
			if ((c as unknown as { hostId: string }).hostId === "down") throw new Error("unreachable");
			return { distinctId: "ins_up", optedOut: false };
		}, (i) => adopted.push(i));
		await sync([cfg("down"), cfg("up")]);
		expect(adopted).toEqual([{ distinctId: "ins_up", optedOut: false }]);
	});
});
