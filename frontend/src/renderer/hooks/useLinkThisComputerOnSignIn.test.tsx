import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { AccountHostSyncError } from "../lib/account-remote-hosts";
import { apiErrorException } from "../lib/api-error-exception";
import { apiClient } from "../lib/api-client";
import { linkThisComputer } from "../lib/link-this-computer";

// Signing in while Connect Mobile serves this computer links it, so it gets
// its fixed address without the user turning on Allow access as well.
const fake = vi.hoisted(() => ({
	signedIn: false,
	failNextStatusRead: false,
	status: { enabled: true, noPublicTunnel: false, accountLinked: false, hostId: "h_this", password: "pw-1", url: "https://quick.trycloudflare.com:443" },
}));
vi.mock("../lib/cloud-session", () => ({ useCloudSession: () => ({ session: fake.signedIn ? { user: { id: "user_01" } } : null }) }));
vi.mock("./useSettings", () => ({ useSettings: () => ({ settings: { cloudControlPlaneUrl: "https://cp.example.test" } }) }));
vi.mock("../lib/link-this-computer", () => ({ linkThisComputer: vi.fn(async () => ({ hostId: "h_this", added: true })) }));
vi.mock("../lib/api-client", () => ({
	apiClient: { POST: vi.fn(async () => ({ data: {} })) },
	apiErrorMessage: (err: any) => (typeof err === "object" && err?.message ? err.message : "failed"),
}));
vi.mock("../components/settings/ConnectMobileContent", () => ({
	fetchMobileStatus: vi.fn(async () => {
		if (fake.failNextStatusRead) {
			fake.failNextStatusRead = false;
			throw new Error("daemon is starting");
		}
		return { ...fake.status };
	}),
	tunnelUrl: (status: { url: string }) => status.url,
}));

import { fetchMobileStatus } from "../components/settings/ConnectMobileContent";
import { isRetryableLinkError, useLinkThisComputerOnSignIn } from "./useLinkThisComputerOnSignIn";

const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider>;

beforeEach(() => {
	vi.mocked(linkThisComputer).mockReset().mockImplementation(async () => ({ hostId: "h_this", added: true }));
	vi.mocked(apiClient.POST).mockClear();
	vi.mocked(fetchMobileStatus).mockClear();
	fake.signedIn = false;
	fake.failNextStatusRead = false;
	Object.assign(fake.status, { enabled: true, noPublicTunnel: false, accountLinked: false });
});

afterEach(() => {
	vi.unstubAllEnvs();
	vi.useRealTimers();
});

test("signing in links a computer Connect Mobile already serves", async () => {
	const { rerender } = renderHook(() => useLinkThisComputerOnSignIn(), { wrapper });
	expect(linkThisComputer).not.toHaveBeenCalled();
	fake.signedIn = true;
	rerender();
	await waitFor(() => expect(linkThisComputer).toHaveBeenCalledWith("https://cp.example.test", "user_01", "h_this", "pw-1", { ifUnlisted: true, url: undefined }));
	// The connector is asked again once the listing landed, so it moves at once.
	await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/mobile/remote-access"));
	rerender();
	await new Promise((resolve) => setTimeout(resolve, 20));
	expect(linkThisComputer).toHaveBeenCalledTimes(1); // once per sign-in
});

test("an AO Cloud without fixed addresses lists it at the tunnel's address", async () => {
	vi.mocked(linkThisComputer).mockResolvedValueOnce({ hostId: "", added: false });
	fake.signedIn = true;
	renderHook(() => useLinkThisComputerOnSignIn(), { wrapper });
	await waitFor(() => expect(linkThisComputer).toHaveBeenLastCalledWith("https://cp.example.test", "user_01", "h_this", "pw-1", { ifUnlisted: true, url: "https://quick.trycloudflare.com:443" }));
});

test("an account with no fixed address left lists it at the tunnel's address", async () => {
	vi.mocked(linkThisComputer).mockRejectedValueOnce(new AccountHostSyncError("AO Cloud host sync failed (409).", 409));
	fake.signedIn = true;
	renderHook(() => useLinkThisComputerOnSignIn(), { wrapper });
	await waitFor(() => expect(linkThisComputer).toHaveBeenLastCalledWith("https://cp.example.test", "user_01", "h_this", "pw-1", { ifUnlisted: true, url: "https://quick.trycloudflare.com:443" }));
});

test("a computer an account already holds is left alone", async () => {
	// Signing in to another account must not take it from the first, and one
	// its account removed on another device must not be listed again.
	Object.assign(fake.status, { accountLinked: true });
	fake.signedIn = true;
	renderHook(() => useLinkThisComputerOnSignIn(), { wrapper });
	await new Promise((resolve) => setTimeout(resolve, 20));
	expect(linkThisComputer).not.toHaveBeenCalled();
});

test("a computer without remote access is not linked", async () => {
	Object.assign(fake.status, { noPublicTunnel: true });
	fake.signedIn = true;
	renderHook(() => useLinkThisComputerOnSignIn(), { wrapper });
	await new Promise((resolve) => setTimeout(resolve, 20));
	expect(linkThisComputer).not.toHaveBeenCalled();
});

test("a development build does not publish its temporary identity", async () => {
	vi.stubEnv("MODE", "development");
	fake.signedIn = true;
	renderHook(() => useLinkThisComputerOnSignIn(), { wrapper });
	await new Promise((resolve) => setTimeout(resolve, 20));
	expect(linkThisComputer).not.toHaveBeenCalled();
});

test("a status read that fails before the daemon is ready is retried", async () => {
	vi.useFakeTimers();
	fake.failNextStatusRead = true;
	fake.signedIn = true;
	renderHook(() => useLinkThisComputerOnSignIn(), { wrapper });
	await vi.advanceTimersByTimeAsync(0);
	expect(linkThisComputer).not.toHaveBeenCalled();
	await vi.advanceTimersByTimeAsync(30_000);
	expect(linkThisComputer).toHaveBeenCalledWith("https://cp.example.test", "user_01", "h_this", "pw-1", { ifUnlisted: true, url: undefined });
});

test("remote access turned on after signing in is picked up", async () => {
	vi.useFakeTimers();
	Object.assign(fake.status, { enabled: false });
	fake.signedIn = true;
	renderHook(() => useLinkThisComputerOnSignIn(), { wrapper });
	await vi.advanceTimersByTimeAsync(0);
	expect(linkThisComputer).not.toHaveBeenCalled();
	Object.assign(fake.status, { enabled: true });
	await vi.advanceTimersByTimeAsync(30_000);
	expect(linkThisComputer).toHaveBeenCalledWith("https://cp.example.test", "user_01", "h_this", "pw-1", { ifUnlisted: true, url: undefined });
});

test("a failed link attempt is retried even if accountLinked became true", async () => {
	vi.useFakeTimers();
	fake.signedIn = true;
	vi.mocked(linkThisComputer).mockImplementationOnce(async () => {
		// Token issuance set accountLinked before the directory entry could be saved.
		fake.status.accountLinked = true;
		throw new Error("directory save failed");
	});
	renderHook(() => useLinkThisComputerOnSignIn(), { wrapper });
	await vi.advanceTimersByTimeAsync(0);
	expect(linkThisComputer).toHaveBeenCalledTimes(1);
	await vi.advanceTimersByTimeAsync(30_000);
	expect(linkThisComputer).toHaveBeenCalledTimes(2);
});

test("a non-retryable 4xx error stops retrying immediately", async () => {
	vi.useFakeTimers();
	fake.signedIn = true;
	vi.mocked(linkThisComputer).mockRejectedValueOnce(new AccountHostSyncError("AO Cloud host sync failed (403).", 403));
	renderHook(() => useLinkThisComputerOnSignIn(), { wrapper });
	await vi.advanceTimersByTimeAsync(0);
	expect(linkThisComputer).toHaveBeenCalledTimes(1);
	await vi.advanceTimersByTimeAsync(60_000);
	expect(linkThisComputer).toHaveBeenCalledTimes(1);
});

test("retry delays back off exponentially and stop after max retries", async () => {
	vi.useFakeTimers();
	fake.signedIn = true;
	Object.assign(fake.status, { enabled: false });
	renderHook(() => useLinkThisComputerOnSignIn(), { wrapper });
	await vi.advanceTimersByTimeAsync(0);
	// Initial attempt was made (status was not enabled, so retry 1 was scheduled for 30s).
	expect(fetchMobileStatus).toHaveBeenCalledTimes(1);

	// Before 30s, no second attempt:
	await vi.advanceTimersByTimeAsync(29_000);
	expect(fetchMobileStatus).toHaveBeenCalledTimes(1);
	expect(linkThisComputer).not.toHaveBeenCalled();

	// At 30s, attempt 1 runs (delay doubled to 60s):
	await vi.advanceTimersByTimeAsync(1_000);
	expect(fetchMobileStatus).toHaveBeenCalledTimes(2);

	// Advance 59s: still waiting on 60s delay:
	await vi.advanceTimersByTimeAsync(59_000);
	expect(fetchMobileStatus).toHaveBeenCalledTimes(2);

	// At +60s (90s total), attempt 2 runs (delay doubled to 120s):
	await vi.advanceTimersByTimeAsync(1_000);
	expect(fetchMobileStatus).toHaveBeenCalledTimes(3);

	// Advance 119s (209s total): still waiting on 120s delay:
	await vi.advanceTimersByTimeAsync(119_000);
	expect(fetchMobileStatus).toHaveBeenCalledTimes(3);

	// At +120s (210s total), attempt 3 runs (delay doubled to 240s):
	await vi.advanceTimersByTimeAsync(1_000);
	expect(fetchMobileStatus).toHaveBeenCalledTimes(4);

	// Advance 239s (449s total): still waiting on 240s delay:
	await vi.advanceTimersByTimeAsync(239_000);
	expect(fetchMobileStatus).toHaveBeenCalledTimes(4);

	// At +240s (450s total), attempt 4 runs (delay capped at 300s):
	await vi.advanceTimersByTimeAsync(1_000);
	expect(fetchMobileStatus).toHaveBeenCalledTimes(5);

	// Advance 299s (749s total): still waiting on 300s delay:
	await vi.advanceTimersByTimeAsync(299_000);
	expect(fetchMobileStatus).toHaveBeenCalledTimes(5);

	// At +300s (750s total), attempt 5 runs (max retries reached):
	await vi.advanceTimersByTimeAsync(1_000);
	expect(fetchMobileStatus).toHaveBeenCalledTimes(6);

	// Remote access turns on now, but max retries was reached so no more polls occur:
	Object.assign(fake.status, { enabled: true });
	await vi.advanceTimersByTimeAsync(600_000);
	expect(fetchMobileStatus).toHaveBeenCalledTimes(6);
	expect(linkThisComputer).not.toHaveBeenCalled();
});

test("isRetryableLinkError classifies 429 as retryable and permanent 4xx as non-retryable", () => {
	expect(isRetryableLinkError(new AccountHostSyncError("rate limit", 429))).toBe(true);
	expect(isRetryableLinkError({ status: 429 })).toBe(true);
	expect(isRetryableLinkError(new Error("wrapped", { cause: { status: 429 } }))).toBe(true);

	expect(isRetryableLinkError(new AccountHostSyncError("forbidden", 403))).toBe(false);
	expect(isRetryableLinkError({ status: 403 })).toBe(false);
	expect(isRetryableLinkError(new Error("wrapped", { cause: { status: 403 } }))).toBe(false);
});

test("isRetryableLinkError recognizes daemon API error envelopes including wrapped 403 response shape", () => {
	const daemon403Envelope = {
		error: "forbidden",
		code: "REMOTE_HOST_ACCOUNT_TOKEN",
		message: "connection password required",
	};
	// Direct daemon envelope
	expect(isRetryableLinkError(daemon403Envelope)).toBe(false);
	// Wrapped by apiErrorException
	expect(isRetryableLinkError(apiErrorException(daemon403Envelope))).toBe(false);
	// Deeply wrapped in an outer Error
	expect(isRetryableLinkError(new Error("link failed", { cause: apiErrorException(daemon403Envelope) }))).toBe(false);
	// With preserved HTTP status
	expect(isRetryableLinkError(Object.assign(apiErrorException(daemon403Envelope), { status: 403 }))).toBe(false);

	// Other permanent daemon error envelopes
	expect(isRetryableLinkError({ error: "unauthorized", code: "DEVICE_REVOKED" })).toBe(false);
	expect(isRetryableLinkError({ error: "bad_request", code: "INVALID_JSON" })).toBe(false);
	expect(isRetryableLinkError({ error: "not_found", code: "ROUTE_NOT_FOUND" })).toBe(false);
	expect(isRetryableLinkError({ error: "not_implemented", code: "REMOTE_HOST_ACCOUNT_TOKEN" })).toBe(false);

	// Retryable daemon error envelopes
	expect(isRetryableLinkError(apiErrorException({ error: "conflict", code: "REMOTE_ACCESS_OFF" }))).toBe(true);
	expect(isRetryableLinkError(apiErrorException({ error: "too_many_requests", code: "LOCKED_OUT" }))).toBe(true);
	expect(isRetryableLinkError(apiErrorException({ error: "unavailable", code: "SERVICE_UNAVAILABLE" }))).toBe(true);
	expect(isRetryableLinkError(apiErrorException({ error: "internal", code: "INTERNAL_ERROR" }))).toBe(true);
});

test("isRetryableLinkError terminates without hanging on cyclic error cause chains", () => {
	const selfReferencing = new Error("self-referencing");
	(selfReferencing as { cause?: unknown }).cause = selfReferencing;
	expect(isRetryableLinkError(selfReferencing)).toBe(true);

	const cyclicA = new Error("cycle-a");
	const cyclicB = new Error("cycle-b");
	(cyclicA as { cause?: unknown }).cause = cyclicB;
	(cyclicB as { cause?: unknown }).cause = cyclicA;
	expect(isRetryableLinkError(cyclicA)).toBe(true);

	const cyclicForbidden: { error: string; cause?: unknown } = { error: "forbidden" };
	cyclicForbidden.cause = cyclicForbidden;
	expect(isRetryableLinkError(cyclicForbidden)).toBe(false);
});

test("stops retrying immediately on a wrapped 403 error from the account-token endpoint", async () => {
	vi.useFakeTimers();
	fake.signedIn = true;
	const accountToken403 = apiErrorException({
		error: "forbidden",
		code: "REMOTE_HOST_ACCOUNT_TOKEN",
		message: "connection password required",
	});
	vi.mocked(linkThisComputer).mockRejectedValueOnce(accountToken403);
	renderHook(() => useLinkThisComputerOnSignIn(), { wrapper });
	await vi.advanceTimersByTimeAsync(0);
	expect(linkThisComputer).toHaveBeenCalledTimes(1);
	// After advancing time, no retry should occur:
	await vi.advanceTimersByTimeAsync(60_000);
	expect(linkThisComputer).toHaveBeenCalledTimes(1);
});

test("HTTP 429 rate limit is treated as retryable by the link attempt", async () => {
	vi.useFakeTimers();
	fake.signedIn = true;
	vi.mocked(linkThisComputer).mockRejectedValueOnce(new AccountHostSyncError("Too Many Requests", 429));
	renderHook(() => useLinkThisComputerOnSignIn(), { wrapper });
	await vi.advanceTimersByTimeAsync(0);
	expect(linkThisComputer).toHaveBeenCalledTimes(1);
	await vi.advanceTimersByTimeAsync(30_000);
	expect(linkThisComputer).toHaveBeenCalledTimes(2);
});

test("a cancelled link attempt does not record completion on error", async () => {
	fake.signedIn = true;
	let rejectLink!: (err: Error) => void;
	vi.mocked(linkThisComputer).mockImplementationOnce(
		() => new Promise((_, reject) => { rejectLink = reject; }),
	);
	const { rerender } = renderHook(() => useLinkThisComputerOnSignIn(), { wrapper });
	await waitFor(() => expect(linkThisComputer).toHaveBeenCalledTimes(1));

	// User signs out while link is in flight; the effect cleanup marks the attempt cancelled.
	fake.signedIn = false;
	rerender();

	// Link attempt rejects with permanent error 403 after sign-out.
	rejectLink(new AccountHostSyncError("Forbidden", 403));
	await new Promise((resolve) => setTimeout(resolve, 10));

	// User signs back in to the same account on the same mounted hook:
	// it should retry and not be blocked by the cancelled attempt having recorded completion.
	fake.signedIn = true;
	rerender();
	await waitFor(() => expect(linkThisComputer).toHaveBeenCalledTimes(2));
});

