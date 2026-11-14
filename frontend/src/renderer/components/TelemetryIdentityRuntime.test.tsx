import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const hooks = vi.hoisted(() => ({
	session: null as unknown,
	status: "authenticated" as string,
	setCloudUser: vi.fn(async (_id: string | null): Promise<boolean | "unsupported"> => true),
	identifyCloudUser: vi.fn(async () => undefined),
	clearCloudUser: vi.fn(),
	noteCloudSignInWebVisitor: vi.fn(),
	sessionListeners: [] as ((account: unknown) => void)[],
	unsubscribe: vi.fn(),
	// What main holds for the renderer to take, once (cloud:takePendingSignIn).
	pendingSignIn: [] as unknown[],
	takePendingSignIn: vi.fn(async (): Promise<unknown> => hooks.pendingSignIn.shift() ?? null),
}));
vi.mock("../lib/cloud-session", () => ({ useCloudSession: () => ({ session: hooks.session, status: hooks.status }) }));
vi.mock("../lib/bridge", () => ({
	aoBridge: {
		telemetry: { setCloudUser: hooks.setCloudUser },
		cloud: {
			onSessionChanged: (listener: (typeof hooks.sessionListeners)[number]) => {
				hooks.sessionListeners.push(listener);
				return hooks.unsubscribe;
			},
			takePendingSignIn: hooks.takePendingSignIn,
		},
	},
}));
vi.mock("../lib/telemetry", () => ({
	identifyCloudUser: hooks.identifyCloudUser,
	clearCloudUser: hooks.clearCloudUser,
	noteCloudSignInWebVisitor: hooks.noteCloudSignInWebVisitor,
}));

import { BINDING_RETRY_MS, TelemetryIdentityRuntime } from "./TelemetryIdentityRuntime";

const account = (authProvider: "workos" | "local") => ({
	authProvider,
	user: { id: "user_01H", email: "dev@example.com", displayName: "Dev" },
	storedAt: "2026-10-08T00:00:00Z",
});

describe("TelemetryIdentityRuntime", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		hooks.sessionListeners.length = 0;
		hooks.pendingSignIn.length = 0;
		hooks.status = "authenticated";
		hooks.setCloudUser.mockImplementation(async () => true);
	});
	afterEach(() => {
		vi.useRealTimers();
	});

	it("says nothing while the session is still loading, so a launch is not read as a sign-out", () => {
		hooks.session = null;
		hooks.status = "loading";
		render(<TelemetryIdentityRuntime />);
		expect(hooks.setCloudUser).not.toHaveBeenCalled();
		expect(hooks.clearCloudUser).not.toHaveBeenCalled();
	});

	it("hands the user id (not the email) to main and identifies once the daemon acknowledged it", async () => {
		hooks.session = account("workos");
		render(<TelemetryIdentityRuntime />);
		await vi.waitFor(() => expect(hooks.identifyCloudUser).toHaveBeenCalledWith({ id: "user_01H", email: "dev@example.com" }));
		expect(hooks.setCloudUser).toHaveBeenCalledWith("user_01H");
		expect(JSON.stringify(hooks.setCloudUser.mock.calls)).not.toContain("dev@example.com");
	});

	it("never identifies without an acknowledgement, and retries until the daemon records the user", async () => {
		vi.useFakeTimers();
		hooks.session = account("workos");
		// Daemon unavailable, then a 500, then recovered.
		hooks.setCloudUser.mockResolvedValueOnce(false).mockResolvedValueOnce(false).mockResolvedValue(true);
		render(<TelemetryIdentityRuntime />);
		await act(async () => {
			await vi.advanceTimersByTimeAsync(0);
		});
		expect(hooks.identifyCloudUser).not.toHaveBeenCalled();
		await act(async () => {
			await vi.advanceTimersByTimeAsync(BINDING_RETRY_MS[0]);
		});
		expect(hooks.identifyCloudUser).not.toHaveBeenCalled();
		await act(async () => {
			await vi.advanceTimersByTimeAsync(BINDING_RETRY_MS[1]);
		});
		expect(hooks.setCloudUser).toHaveBeenCalledTimes(3);
		expect(hooks.identifyCloudUser).toHaveBeenCalledTimes(1);
	});

	it("a sign-out while the binding is still pending cancels the identify for good", async () => {
		vi.useFakeTimers();
		hooks.session = account("workos");
		hooks.setCloudUser.mockImplementation(async (id) => id === null);
		const view = render(<TelemetryIdentityRuntime />);
		await act(async () => {
			await vi.advanceTimersByTimeAsync(0);
		});
		hooks.session = null;
		view.rerender(<TelemetryIdentityRuntime />);
		hooks.setCloudUser.mockImplementation(async () => true); // the daemon recovers afterwards
		await act(async () => {
			await vi.advanceTimersByTimeAsync(BINDING_RETRY_MS.reduce((a, b) => a + b, 0));
		});
		expect(hooks.identifyCloudUser).not.toHaveBeenCalled();
		expect(hooks.clearCloudUser).toHaveBeenCalled();
		expect(hooks.setCloudUser).toHaveBeenLastCalledWith(null);
	});

	it("retries a sign-out whose request was lost until the daemon acknowledges it", async () => {
		vi.useFakeTimers();
		hooks.session = null;
		// Transport failures with no daemon status change in between.
		hooks.setCloudUser.mockRejectedValueOnce(new Error("aborted")).mockResolvedValueOnce(false).mockResolvedValue(true);
		render(<TelemetryIdentityRuntime />);
		await act(async () => {
			await vi.advanceTimersByTimeAsync(0);
		});
		expect(hooks.setCloudUser).toHaveBeenCalledTimes(1);
		await act(async () => {
			await vi.advanceTimersByTimeAsync(BINDING_RETRY_MS[0]);
		});
		expect(hooks.setCloudUser).toHaveBeenCalledTimes(2);
		await act(async () => {
			await vi.advanceTimersByTimeAsync(BINDING_RETRY_MS[1]);
		});
		expect(hooks.setCloudUser).toHaveBeenCalledTimes(3);
		expect(hooks.setCloudUser.mock.calls.every(([id]) => id === null)).toBe(true);
		// Acknowledged: no further attempts.
		await act(async () => {
			await vi.advanceTimersByTimeAsync(BINDING_RETRY_MS.reduce((a, b) => a + b, 0));
		});
		expect(hooks.setCloudUser).toHaveBeenCalledTimes(3);
		expect(hooks.identifyCloudUser).not.toHaveBeenCalled();
	});

	it("keeps retrying a sign-out past the schedule, at the capped interval, until the daemon acknowledges", async () => {
		vi.useFakeTimers();
		hooks.session = null;
		const attemptsInSchedule = BINDING_RETRY_MS.length + 1;
		let calls = 0;
		hooks.setCloudUser.mockImplementation(async () => ++calls > attemptsInSchedule + 2); // recovers well after the schedule ends
		render(<TelemetryIdentityRuntime />);
		await act(async () => {
			await vi.advanceTimersByTimeAsync(BINDING_RETRY_MS.reduce((a, b) => a + b, 0));
		});
		expect(calls).toBe(attemptsInSchedule); // the whole schedule is spent and still not acknowledged
		const cap = BINDING_RETRY_MS[BINDING_RETRY_MS.length - 1];
		await act(async () => {
			await vi.advanceTimersByTimeAsync(cap);
		});
		expect(calls).toBe(attemptsInSchedule + 1);
		await act(async () => {
			await vi.advanceTimersByTimeAsync(cap);
		});
		expect(calls).toBe(attemptsInSchedule + 2); // the daemon answers here
		await act(async () => {
			await vi.advanceTimersByTimeAsync(cap * 5);
		});
		expect(calls).toBe(attemptsInSchedule + 3); // acknowledged on the next attempt, then it stops
		expect(hooks.identifyCloudUser).not.toHaveBeenCalled();
	});

	it("stops retrying a sign-out when the daemon does not support the identity route", async () => {
		vi.useFakeTimers();
		hooks.session = null;
		hooks.setCloudUser.mockResolvedValue("unsupported");
		render(<TelemetryIdentityRuntime />);
		await act(async () => {
			await vi.advanceTimersByTimeAsync(BINDING_RETRY_MS.reduce((a, b) => a + b, 0) * 3);
		});
		expect(hooks.setCloudUser).toHaveBeenCalledTimes(1);
	});

	it("still gives up on a sign-in after the schedule", async () => {
		vi.useFakeTimers();
		hooks.session = account("workos");
		hooks.setCloudUser.mockImplementation(async () => false);
		render(<TelemetryIdentityRuntime />);
		await act(async () => {
			await vi.advanceTimersByTimeAsync(BINDING_RETRY_MS.reduce((a, b) => a + b, 0) * 3);
		});
		expect(hooks.setCloudUser).toHaveBeenCalledTimes(BINDING_RETRY_MS.length + 1);
		expect(hooks.identifyCloudUser).not.toHaveBeenCalled();
	});

	it("never identifies the dev-only local provider", async () => {
		hooks.session = account("local");
		render(<TelemetryIdentityRuntime />);
		expect(hooks.identifyCloudUser).not.toHaveBeenCalled();
		expect(hooks.setCloudUser).toHaveBeenCalledWith(null);
	});

	it("clears the identity when signed out", () => {
		hooks.session = null;
		render(<TelemetryIdentityRuntime />);
		expect(hooks.clearCloudUser).toHaveBeenCalled();
		expect(hooks.setCloudUser).toHaveBeenCalledWith(null);
	});

	it("takes a sign-in's website visitor id on each session notification, once, and hands it to telemetry", async () => {
		hooks.session = account("workos");
		const view = render(<TelemetryIdentityRuntime />);
		await act(async () => {});
		expect(hooks.sessionListeners).toHaveLength(1);
		const [notify] = hooks.sessionListeners;
		const notifyWith = async (pending: unknown) => {
			if (pending) hooks.pendingSignIn.push(pending);
			await act(async () => { notify(pending ? (pending as { account: unknown }).account : null); });
		};

		await notifyWith({ account: account("workos"), signIn: { webDistinctId: "019a3f5e-8c2b" } });
		await notifyWith(null);
		await notifyWith({ account: account("local"), signIn: { webDistinctId: "019a3f5e-8c2b" } });
		expect(hooks.noteCloudSignInWebVisitor.mock.calls).toEqual([
			["user_01H", "019a3f5e-8c2b"],
			[null, "019a3f5e-8c2b"],
		]);

		view.unmount();
		expect(hooks.unsubscribe).toHaveBeenCalledTimes(1);
	});

	it("takes the sign-in that started the app when it mounts, before any notification", async () => {
		// The sign-in link launched the app: its notification went out before
		// this window was listening, so main kept the context.
		hooks.pendingSignIn.push({ account: account("workos"), signIn: { webDistinctId: "019a3f5e-8c2b" } });
		hooks.session = account("workos");
		render(<TelemetryIdentityRuntime />);
		await act(async () => {});
		expect(hooks.noteCloudSignInWebVisitor.mock.calls).toEqual([["user_01H", "019a3f5e-8c2b"]]);
	});
});
