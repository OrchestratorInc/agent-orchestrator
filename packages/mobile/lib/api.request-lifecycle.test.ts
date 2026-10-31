import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ApiError, apiRequest, getSessions } from "./api";
import { UnreachableError } from "./connectionError";

vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() } }));
vi.mock("expo-secure-store", () => ({ getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn() }));

const config = { host: "ao.test", httpPort: "3011", muxPort: "3011", password: "secret" };
beforeEach(() => { vi.useFakeTimers(); vi.stubGlobal("fetch", vi.fn()); });
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

function stalledResponse(status = 200) {
	return new Response(new ReadableStream({ start() {} }), { status });
}

it("bounds a successful response whose headers arrive but body stalls", async () => {
	vi.mocked(fetch).mockResolvedValue(stalledResponse());
	let failure: unknown;
	void apiRequest(config, "/api/v1/sessions", undefined, 100).catch((error) => { failure = error; });
	await vi.advanceTimersByTimeAsync(100);
	expect(failure).toBeInstanceOf(UnreachableError);
	expect(failure).toMatchObject({ reason: "timeout" });
});

it("retains a known auth status even when its error body stalls", async () => {
	vi.mocked(fetch).mockResolvedValue(stalledResponse(401));
	let failure: unknown;
	void apiRequest(config, "/api/v1/sessions", undefined, 100).catch((error) => { failure = error; });
	await vi.advanceTimersByTimeAsync(100);
	expect(failure).toBeInstanceOf(ApiError);
	expect(failure).toMatchObject({ status: 401 });
});

it("cancels a pending body read when the caller leaves the host", async () => {
	vi.mocked(fetch).mockResolvedValue(stalledResponse());
	const controller = new AbortController();
	let failure: unknown;
	void apiRequest(config, "/api/v1/sessions", { signal: controller.signal }).catch((error) => { failure = error; });
	await vi.advanceTimersByTimeAsync(1);
	controller.abort();
	await vi.advanceTimersByTimeAsync(1);
	expect(failure).toMatchObject({ name: "AbortError" });
});

it("does not send a request when the caller has already cancelled", async () => {
	const controller = new AbortController();
	controller.abort();
	vi.mocked(fetch).mockResolvedValue(new Response("{}"));
	await expect(apiRequest(config, "/api/v1/sessions", { signal: controller.signal })).rejects.toMatchObject({ name: "AbortError" });
	expect(fetch).not.toHaveBeenCalled();
});

it("keeps the daemon error envelope and clears completed request timers", async () => {
	vi.mocked(fetch).mockResolvedValue(new Response(JSON.stringify({ message: "Denied", code: "unauthorized", requestId: "req-1" }), { status: 401 }));
	await expect(apiRequest(config, "/api/v1/sessions")).rejects.toMatchObject({ status: 401, detail: "Denied", code: "unauthorized", requestId: "req-1" });
	expect(vi.getTimerCount()).toBe(0);
});

it("does not fan out board requests after the sessions body is cancelled", async () => {
	vi.mocked(fetch).mockImplementation(async (_url, init) => {
		if (init?.signal?.aborted) throw Object.assign(new Error("aborted"), { name: "AbortError" });
		return stalledResponse();
	});
	const controller = new AbortController();
	let failure: unknown;
	// Third argument scopes every request in this board load to one lifecycle.
	void getSessions(config, "all", { signal: controller.signal }).catch((error) => { failure = error; });
	await vi.advanceTimersByTimeAsync(1);
	controller.abort();
	await vi.advanceTimersByTimeAsync(1);
	expect(failure).toMatchObject({ name: "AbortError" });
	expect(fetch).toHaveBeenCalledTimes(1);
});
