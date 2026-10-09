import { describe, expect, it, vi } from "vitest";
import { ACTIVE_STORAGE_KEY } from "./dailyActive";
import { MOBILE_EVENTS } from "./events";
import { createMobileTelemetry, type MobileTelemetryClient } from "./telemetry";

function fakeClient() {
	const captures: Array<{ event: string; props?: Record<string, unknown> }> = [];
	const registered: Record<string, unknown>[] = [];
	const calls: string[] = [];
	const client: MobileTelemetryClient = {
		capture: (event, props) => captures.push({ event, props }),
		register: (props) => void registered.push(props),
		identify: (id) => void calls.push(`identify:${id}`),
		reset: () => void calls.push("reset"),
		unregister: (name) => void calls.push(`unregister:${name}`),
		optOut: () => void calls.push("optOut"),
		optIn: () => void calls.push("optIn"),
	};
	return { client, captures, registered, calls };
}

function memory(initial?: Record<string, string>) {
	const map = new Map(Object.entries(initial ?? {}));
	return {
		getItem: async (k: string) => map.get(k) ?? null,
		setItem: async (k: string, v: string) => void map.set(k, v),
	};
}

describe("createMobileTelemetry", () => {
	it("registers the context as super-properties once on creation", () => {
		const { client, registered } = fakeClient();
		createMobileTelemetry(client, { client: "mobile", platform: "ios" });
		expect(registered).toEqual([{ client: "mobile", platform: "ios" }]);
	});

	it("sanitizes properties on capture, dropping anything unregistered", () => {
		const { client, captures } = fakeClient();
		const t = createMobileTelemetry(client, {});
		t.capture(MOBILE_EVENTS.featureUsed, {
			feature: "spawn",
			outcome: "succeeded",
			session_title: "leak me",
			password: "hunter2",
		});
		expect(captures).toEqual([
			{
				event: MOBILE_EVENTS.featureUsed,
				props: { feature: "spawn", outcome: "succeeded", $process_person_profile: false },
			},
		]);
	});

	// Fail closed on the event name: a typo must send nothing, not a bare event.
	it("drops an event name that is not in the allowlist", () => {
		const { client, captures } = fakeClient();
		const t = createMobileTelemetry(client, {});
		// @ts-expect-error deliberately passing an unknown event name
		t.capture("ao.mobile_app.typo", { feature: "spawn" });
		expect(captures).toEqual([]);
	});

	it("stamps $process_person_profile:false on every event for the anonymous rate", () => {
		const { client, captures } = fakeClient();
		const t = createMobileTelemetry(client, {});
		t.capture(MOBILE_EVENTS.paired, { method: "qr" });
		expect(captures[0].props?.$process_person_profile).toBe(false);
	});

	it("drops an event named in the build-time kill switch", () => {
		const { client, captures } = fakeClient();
		const t = createMobileTelemetry(client, {}, { disabledEvents: [MOBILE_EVENTS.connected] });
		t.capture(MOBILE_EVENTS.connected, { trigger: "launch" });
		t.capture(MOBILE_EVENTS.paired, { method: "qr" });
		expect(captures.map((c) => c.event)).toEqual([MOBILE_EVENTS.paired]);
	});

	it("drops an event the rate limiter rejects", () => {
		const { client, captures } = fakeClient();
		let calls = 0;
		const t = createMobileTelemetry(client, {}, { allow: () => (++calls <= 1) });
		t.capture(MOBILE_EVENTS.paired, { method: "qr" }); // 1st: allowed
		t.capture(MOBILE_EVENTS.paired, { method: "qr" }); // 2nd: rate-limited
		expect(captures).toHaveLength(1);
	});

	it("emits the daily active heartbeat once per UTC day", async () => {
		const { client, captures } = fakeClient();
		const store = memory();
		const t = createMobileTelemetry(client, {});
		await t.active(store, new Date("2026-08-06T01:00:00Z"));
		await t.active(store, new Date("2026-08-06T20:00:00Z"));
		await t.active(store, new Date("2026-08-07T00:01:00Z"));
		expect(captures.map((c) => c.event)).toEqual([MOBILE_EVENTS.active, MOBILE_EVENTS.active]);
	});

	it("marks the active day in storage", async () => {
		const { client } = fakeClient();
		const store = memory();
		const t = createMobileTelemetry(client, {});
		await t.active(store, new Date("2026-08-06T01:00:00Z"));
		expect(await store.getItem(ACTIVE_STORAGE_KEY)).toBe("2026-08-06");
	});

	describe("desktop identity", () => {
		it("identifies with the desktop distinct id once, registers github/cloud ids, and lifts the anonymous flag", () => {
			const { client, captures, registered, calls } = fakeClient();
			const t = createMobileTelemetry(client, {});
			t.adoptDesktopIdentity({ distinctId: "user_01H", cloudUserId: "user_01H", githubLogin: "octocat", optedOut: false });
			t.adoptDesktopIdentity({ distinctId: "user_01H", cloudUserId: "user_01H", githubLogin: "octocat", optedOut: false });
			expect(calls.filter((c) => !c.startsWith("unregister"))).toEqual(["identify:user_01H"]);
			expect(registered.at(-1)).toEqual({ github_actor: "octocat", ao_cloud_user_id: "user_01H" });
			t.capture(MOBILE_EVENTS.paired, { method: "qr" });
			expect(captures[0].props).not.toHaveProperty("$process_person_profile");
			// Email never reaches the phone or its events.
			expect(JSON.stringify([registered, captures])).not.toContain("@");
		});

		it("stays anonymous when the desktop reports no identity", () => {
			const { client, captures, calls } = fakeClient();
			const t = createMobileTelemetry(client, {});
			t.adoptDesktopIdentity({ optedOut: false });
			t.capture(MOBILE_EVENTS.paired, { method: "qr" });
			expect(calls).toEqual([]);
			expect(captures[0].props?.$process_person_profile).toBe(false);
		});

		it("opt-out stops capture, resets the SDK, persists, and is lifted by an opted-in desktop", () => {
			const { client, captures, calls } = fakeClient();
			const persisted: boolean[] = [];
			const t = createMobileTelemetry(client, {}, { onOptOutChange: (v) => persisted.push(v) });
			t.adoptDesktopIdentity({ distinctId: "ins_abc", optedOut: false });
			t.adoptDesktopIdentity({ optedOut: true });
			t.capture(MOBILE_EVENTS.paired, { method: "qr" });
			expect(captures).toEqual([]);
			expect(calls.filter((c) => !c.startsWith("unregister"))).toEqual(["identify:ins_abc", "reset", "optOut"]);

			t.adoptDesktopIdentity({ distinctId: "ins_abc", optedOut: false });
			expect(calls.filter((c) => !c.startsWith("unregister")).slice(3)).toEqual(["optIn", "identify:ins_abc"]);
			t.capture(MOBILE_EVENTS.paired, { method: "qr" });
			expect(captures).toHaveLength(1);
			expect(persisted).toEqual([true, false]);
		});

		it("drops everything until the saved preference loads, then re-applies a saved opt-out to the SDK", () => {
			const { client, captures, calls } = fakeClient();
			const t = createMobileTelemetry(client, {}, { awaitPreference: true });
			t.capture(MOBILE_EVENTS.paired, { method: "qr" });
			expect(captures).toEqual([]);

			t.setOptedOut(true);
			// reset() clears the SDK's own opt-out flag, so optOut() must follow it.
			expect(calls).toEqual(["reset", "optOut"]);
			t.capture(MOBILE_EVENTS.paired, { method: "qr" });
			expect(captures).toEqual([]);
		});

		it("releases capture when the saved preference is opted in", () => {
			const { client, captures } = fakeClient();
			const t = createMobileTelemetry(client, {}, { awaitPreference: true });
			t.setOptedOut(false);
			t.capture(MOBILE_EVENTS.paired, { method: "qr" });
			expect(captures).toHaveLength(1);
		});

		it("removes the previous identity properties when the new identity lacks them", () => {
			const { client, calls, registered } = fakeClient();
			const t = createMobileTelemetry(client, {});
			t.adoptDesktopIdentity({ distinctId: "user_01H", cloudUserId: "user_01H", githubLogin: "octocat", optedOut: false });
			calls.length = 0;
			// Signed out: install id only, no user id, GitHub login still known.
			t.adoptDesktopIdentity({ distinctId: "ins_abc", githubLogin: "octocat", optedOut: false });
			expect(calls).toContain("unregister:ao_cloud_user_id");
			expect(calls).not.toContain("unregister:github_actor");
			expect(registered.at(-1)).toEqual({ github_actor: "octocat" });
			// A desktop with no GitHub login at all clears the old handle too.
			calls.length = 0;
			t.adoptDesktopIdentity({ distinctId: "ins_def", optedOut: false });
			expect(calls).toEqual(expect.arrayContaining(["unregister:github_actor", "unregister:ao_cloud_user_id"]));
		});

		it("re-registers when only the GitHub login changes for the same distinct id", () => {
			const { client, registered } = fakeClient();
			const t = createMobileTelemetry(client, {});
			t.adoptDesktopIdentity({ distinctId: "ins_abc", optedOut: false });
			t.adoptDesktopIdentity({ distinctId: "ins_abc", githubLogin: "octocat", optedOut: false });
			expect(registered.at(-1)).toEqual({ github_actor: "octocat" });
		});

		it("restores a persisted opt-out before anything is sent", async () => {
			const { client, captures } = fakeClient();
			const t = createMobileTelemetry(client, {}, { optedOut: true });
			await t.active(memory(), new Date("2026-08-06T01:00:00Z"));
			t.capture(MOBILE_EVENTS.paired, { method: "qr" });
			expect(captures).toEqual([]);
		});
	});
});
