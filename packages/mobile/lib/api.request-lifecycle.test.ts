import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ApiError, apiRequest, getSessions, RESPONSE_BODY_STALL_MS } from "./api";
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

it("fails a stalled successful body no sooner than the request timeout", async () => {
	vi.mocked(fetch).mockResolvedValue(stalledResponse());
	let failure: unknown;
	void apiRequest(config, "/api/v1/sessions").catch((error) => { failure = error; });
	await vi.advanceTimersByTimeAsync(11_999);
	expect(failure).toBeUndefined();
	await vi.advanceTimersByTimeAsync(1);
	expect(failure).toBeInstanceOf(UnreachableError);
	expect(failure).toMatchObject({ reason: "timeout" });
});

it("fails a successful body once its bytes stop arriving after the request timeout", async () => {
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

it("never fails a body that keeps arriving", async () => {
	const body = streamingResponse();
	vi.mocked(fetch).mockResolvedValue(body.res);
	let result: Response | undefined;
	let failure: unknown;
	void apiRequest(config, "/api/v1/sessions").then((res) => { result = res; }, (error) => { failure = error; });
	await vi.advanceTimersByTimeAsync(1);
	body.push("[");
	for (let minute = 0; minute < 10 * 60_000; minute += RESPONSE_BODY_STALL_MS - 1) {
		await vi.advanceTimersByTimeAsync(RESPONSE_BODY_STALL_MS - 1);
		body.push(" ");
	}
	body.push("]");
	body.close();
	await vi.advanceTimersByTimeAsync(1);
	expect(failure).toBeUndefined();
	expect(await result?.json()).toEqual([]);
	expect(vi.getTimerCount()).toBe(0);
});

it("decodes UTF-8 split across body chunks, and invalid bytes as text() would", async () => {
	const bytes = new TextEncoder().encode('{"text":"naïve – 🚀 done"}');
	const split = bytes.indexOf(0xf0) + 2; // inside the 4-byte rocket
	vi.mocked(fetch).mockResolvedValueOnce(new Response(new ReadableStream({
		start(controller) {
			controller.enqueue(bytes.slice(0, split));
			controller.enqueue(bytes.slice(split));
			controller.close();
		},
	})));
	expect(await (await apiRequest(config, "/api/v1/a")).json()).toEqual({ text: "naïve – 🚀 done" });
	const invalid = new Uint8Array([0x22, 0xff, 0xc3, 0x22]);
	vi.mocked(fetch).mockResolvedValueOnce(new Response(invalid));
	expect(await (await apiRequest(config, "/api/v1/b")).text()).toBe(await new Response(invalid).text());
});

it("gives a response without a readable body the plain timeout", async () => {
	let finish!: (text: string) => void;
	vi.mocked(fetch).mockResolvedValue({
		ok: true, status: 200, statusText: "OK", headers: new Headers(), body: null,
		text: () => new Promise<string>((resolve) => { finish = resolve; }),
	} as unknown as Response);
	let result: Response | undefined;
	void apiRequest(config, "/api/v1/sessions").then((res) => { result = res; });
	await vi.advanceTimersByTimeAsync(11_000);
	finish("{}");
	await vi.advanceTimersByTimeAsync(1);
	expect(await result?.json()).toEqual({});

	let failure: unknown;
	void apiRequest(config, "/api/v1/sessions").catch((error) => { failure = error; });
	await vi.advanceTimersByTimeAsync(12_000);
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

it("cancels every board request with the board load's lifecycle", async () => {
	vi.mocked(fetch).mockImplementation(async (_url, init) => {
		if (init?.signal?.aborted) throw Object.assign(new Error("aborted"), { name: "AbortError" });
		return stalledResponse();
	});
	const controller = new AbortController();
	let failure: unknown;
	// Third argument scopes every request in this board load to one lifecycle.
	void getSessions(config, "all", { signal: controller.signal }).catch((error) => { failure = error; });
	await vi.advanceTimersByTimeAsync(1);
	// The /sessions headers accepted the credential, so the rest went out.
	expect(fetch).toHaveBeenCalledTimes(3);
	controller.abort();
	await vi.advanceTimersByTimeAsync(1);
	expect(failure).toMatchObject({ name: "AbortError" });
	expect(vi.mocked(fetch).mock.calls.every(([, init]) => init?.signal?.aborted)).toBe(true);
});

it("revalidates a polled GET with its ETag and reuses the body on 304", async () => {
	const body = JSON.stringify({ projects: [{ id: "p1", name: "One" }] });
	vi.mocked(fetch)
		.mockResolvedValueOnce(new Response(body, { status: 200, headers: { ETag: 'W/"v1"' } }))
		.mockResolvedValueOnce(new Response(null, { status: 304, headers: { ETag: 'W/"v1"' } }));
	const first = await apiRequest(config, "/api/v1/projects");
	expect(await first.json()).toEqual(JSON.parse(body));
	const second = await apiRequest(config, "/api/v1/projects");
	expect(second.status).toBe(200);
	expect(await second.json()).toEqual(JSON.parse(body));
	const headers = vi.mocked(fetch).mock.calls.map(([, init]) => (init?.headers as Record<string, string>)["If-None-Match"]);
	expect(headers).toEqual([undefined, 'W/"v1"']);
});
