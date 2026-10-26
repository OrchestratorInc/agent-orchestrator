import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ApiError, apiRequest, getSessions, RESPONSE_BODY_STALL_MS, RESPONSE_BODY_TIMEOUT_MS } from "./api";
import { UnreachableError } from "./connectionError";

vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() } }));
vi.mock("expo-secure-store", () => ({ getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn() }));

const config = { host: "ao.test", httpPort: "3011", muxPort: "3011", password: "secret" };
beforeEach(() => { vi.useFakeTimers(); vi.stubGlobal("fetch", vi.fn()); });
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

function stalledResponse(status = 200) {
	return new Response(new ReadableStream({ start() {} }), { status });
}

function streamingResponse() {
	let push!: (chunk: string) => void;
	let close!: () => void;
	const res = new Response(new ReadableStream({
		start(controller) {
			push = (chunk) => controller.enqueue(new TextEncoder().encode(chunk));
			close = () => controller.close();
		},
	}));
	return { res, push: (chunk: string) => push(chunk), close: () => close() };
}

it("fails a successful response once its body stops arriving", async () => {
	const body = streamingResponse();
	vi.mocked(fetch).mockResolvedValue(body.res);
	let failure: unknown;
	void apiRequest(config, "/api/v1/sessions", undefined, 100).catch((error) => { failure = error; });
	await vi.advanceTimersByTimeAsync(1);
	body.push('{"sessions":');
	await vi.advanceTimersByTimeAsync(RESPONSE_BODY_STALL_MS - 1);
	expect(failure).toBeUndefined();
	await vi.advanceTimersByTimeAsync(1);
	expect(failure).toBeInstanceOf(UnreachableError);
	expect(failure).toMatchObject({ reason: "timeout" });
});

it("lets a trickling body run until the overall cap", async () => {
	const body = streamingResponse();
	vi.mocked(fetch).mockResolvedValue(body.res);
	let failure: unknown;
	void apiRequest(config, "/api/v1/sessions", undefined, 100).catch((error) => { failure = error; });
	const step = RESPONSE_BODY_STALL_MS - 1_000;
	let elapsed = 0;
	while (elapsed + step < RESPONSE_BODY_TIMEOUT_MS) {
		await vi.advanceTimersByTimeAsync(step);
		elapsed += step;
		body.push(" ");
		expect(failure).toBeUndefined();
	}
	await vi.advanceTimersByTimeAsync(RESPONSE_BODY_TIMEOUT_MS - elapsed);
	expect(failure).toMatchObject({ reason: "timeout" });
});

it("times out a request whose headers never arrive", async () => {
	vi.mocked(fetch).mockImplementation((_url, init) => new Promise((_resolve, reject) => {
		init?.signal?.addEventListener("abort", () => reject(Object.assign(new Error("aborted"), { name: "AbortError" })));
	}));
	let failure: unknown;
	void apiRequest(config, "/api/v1/sessions", undefined, 100).catch((error) => { failure = error; });
	await vi.advanceTimersByTimeAsync(100);
	expect(failure).toMatchObject({ reason: "timeout" });
});

// On 3G a large body outlasts the header timeout. That is a slow link, not a
// dead one, and must not be reported as unreachable (which re-races and reloads).
it("lets a slow body finish after the header timeout has passed", async () => {
	let push!: (chunk: string) => void;
	let close!: () => void;
	vi.mocked(fetch).mockResolvedValue(new Response(new ReadableStream({
		start(controller) {
			push = (chunk) => controller.enqueue(new TextEncoder().encode(chunk));
			close = () => controller.close();
		},
	})));
	let result: Response | undefined;
	let failure: unknown;
	void apiRequest(config, "/api/v1/sessions", undefined, 100).then((res) => { result = res; }, (error) => { failure = error; });
	await vi.advanceTimersByTimeAsync(1);
	push('{"sessions":');
	await vi.advanceTimersByTimeAsync(5_000);
	push("[]}");
	close();
	await vi.advanceTimersByTimeAsync(1);
	expect(failure).toBeUndefined();
	expect(await result?.json()).toEqual({ sessions: [] });
	expect(vi.getTimerCount()).toBe(0);
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
