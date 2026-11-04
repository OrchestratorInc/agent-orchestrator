import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpStreamEvent } from "../../../main/cloud-cp-proxy";
import { CLOUD_MALFORMED_RESPONSE, CLOUD_UNREACHABLE } from "../../../shared/cloud-cp-errors";
import { createCloudCpClient } from "./client";
import { CloudCpAuthError, CloudCpError, cloudCpErrorFromFailure } from "./errors";
import { cloudCpFetch } from "./renderer-client";
import { subscribeSessionEventsBridged, type CloudCpStreamBridge } from "./stream-bridge";
import { createBridgedFetch } from "./transport";

const { noteCloudFailure } = vi.hoisted(() => ({ noteCloudFailure: vi.fn() }));
vi.mock("../cloud-error-report", () => ({ noteCloudFailure }));

const REQUEST_ID = "0f8fad5b-d9cb-469f-a165-70867728950e";
const HEADER_ID = "7c9e6679-7425-40de-944b-e07fc1f90ae7";

function client(fetchImpl: typeof fetch, onFailure = vi.fn()) {
	return { onFailure, client: createCloudCpClient({ baseUrl: "https://cp.test", getToken: async () => "token", fetchImpl, onFailure }) };
}

async function failure(promise: Promise<unknown>): Promise<CloudCpError> {
	const error = await promise.then(() => undefined, (cause: unknown) => cause);
	expect(error).toBeInstanceOf(CloudCpError);
	return error as CloudCpError;
}

beforeEach(() => noteCloudFailure.mockReset());

describe("cloud error origin in the client", () => {
	it("makes a fetch that never reached AO Cloud a network CloudCpError with CLOUD_UNREACHABLE, and reports it", async () => {
		const { client: cp, onFailure } = client(vi.fn(async () => { throw new TypeError("Failed to fetch"); }) as unknown as typeof fetch);
		const error = await failure(cp.me());
		expect(error).toMatchObject({ status: 0, code: CLOUD_UNREACHABLE, origin: "network" });
		expect(onFailure).toHaveBeenCalledWith(error);
	});

	it("reads the request id from the envelope, else the X-Request-ID header", async () => {
		const responses = [
			new Response(JSON.stringify({ error: "x", code: "WORKER_UNAVAILABLE", message: "down", requestId: REQUEST_ID }), { status: 503, headers: { "x-request-id": HEADER_ID } }),
			new Response("<html>Bad gateway</html>", { status: 502, headers: { "x-request-id": HEADER_ID } }),
		];
		const { client: cp, onFailure } = client(vi.fn(async () => responses.shift() as Response) as unknown as typeof fetch);
		expect(await failure(cp.me())).toMatchObject({ status: 503, code: "WORKER_UNAVAILABLE", requestId: REQUEST_ID, origin: "cloud" });
		expect(await failure(cp.me())).toMatchObject({ status: 502, code: undefined, requestId: HEADER_ID, origin: "cloud" });
		expect(onFailure).toHaveBeenCalledTimes(2);
	});

	it("makes a 2xx body it cannot read CLOUD_MALFORMED_RESPONSE with the response's id", async () => {
		const { client: cp, onFailure } = client(vi.fn(async () => new Response("<html>", { status: 200, headers: { "x-request-id": HEADER_ID } })) as unknown as typeof fetch);
		expect(await failure(cp.me())).toMatchObject({ status: 200, code: CLOUD_MALFORMED_RESPONSE, requestId: HEADER_ID, origin: "cloud" });
		expect(onFailure).toHaveBeenCalledOnce();
	});

	it("keeps an abort the caller's own AbortError, unreported", async () => {
		const controller = new AbortController();
		const fetchImpl = vi.fn(async () => {
			controller.abort();
			throw new DOMException("The operation was aborted.", "AbortError");
		});
		const { client: cp, onFailure } = client(fetchImpl as unknown as typeof fetch);
		await expect(cp.me({ signal: controller.signal })).rejects.toMatchObject({ name: "AbortError" });
		expect(onFailure).not.toHaveBeenCalled();
	});
});

describe("cloud error origin over the main-process bridge", () => {
	const unreachable = {
		status: 0,
		headers: { "content-type": "application/json", "x-request-id": REQUEST_ID },
		body: JSON.stringify({ error: "cloud_unreachable", code: CLOUD_UNREACHABLE, message: "AO Cloud could not be reached.", requestId: REQUEST_ID, cause: "ECONNREFUSED" }),
	};

	it("turns main's status-0 answer into a typed error through the bridged fetch, keeping the id and cause", async () => {
		const { client: cp } = client(createBridgedFetch({ request: async () => unreachable }));
		expect(await failure(cp.me())).toMatchObject({ status: 0, code: CLOUD_UNREACHABLE, requestId: REQUEST_ID, networkCause: "ECONNREFUSED", origin: "network" });
	});

	it("does the same through the renderer's own fetch", async () => {
		const w = window as unknown as { ao?: { cloudCp?: { request: () => Promise<typeof unreachable> } } };
		const before = w.ao;
		w.ao = { ...before, cloudCp: { request: async () => unreachable } };
		try {
			const error = await failure(cloudCpFetch("https://cp.test/api/cloud/v1/me"));
			expect(error).toMatchObject({ status: 0, code: CLOUD_UNREACHABLE, requestId: REQUEST_ID });
		} finally {
			w.ao = before;
		}
	});

	it("builds the typed error from main's fields: 401 is a sign-in error, status 0 defaults to CLOUD_UNREACHABLE", () => {
		expect(cloudCpErrorFromFailure({ status: 401, code: "no_token", message: "Sign in" })).toBeInstanceOf(CloudCpAuthError);
		expect(cloudCpErrorFromFailure({ status: 0, message: "" })).toMatchObject({ code: CLOUD_UNREACHABLE, message: "AO Cloud could not be reached." });
		expect(cloudCpErrorFromFailure({ status: 503, message: "x", code: "a sentence, not a code" }).code).toBeUndefined();
	});
});

describe("cloud error origin on bridged streams", () => {
	function bridge(open: CloudCpStreamBridge["openStream"]) {
		let listener: ((event: CloudCpStreamEvent) => void) | undefined;
		return {
			openStream: vi.fn(open),
			closeStream: vi.fn(),
			onStreamEvent: vi.fn((_id: string, next: (event: CloudCpStreamEvent) => void) => {
				listener = next;
				return () => { listener = undefined; };
			}),
			emit: (event: CloudCpStreamEvent) => listener?.(event),
		};
	}

	it("keeps the status, code and request id of a stream the control plane refused", async () => {
		const onError = vi.fn();
		await subscribeSessionEventsBridged({
			baseUrl: "https://cp.test", orgId: "o", sessionId: "s", onEvent: vi.fn(), onError,
			bridge: bridge(async () => ({ error: { status: 503, code: "WORKER_UNAVAILABLE", message: "down", requestId: REQUEST_ID } })),
		});
		expect(onError.mock.calls[0][0]).toMatchObject({ status: 503, code: "WORKER_UNAVAILABLE", requestId: REQUEST_ID, message: "down", origin: "cloud" });
		expect(noteCloudFailure).toHaveBeenCalledWith(onError.mock.calls[0][0]);
	});

	it("keeps a sign-in refusal a CloudCpAuthError", async () => {
		const onError = vi.fn();
		await subscribeSessionEventsBridged({
			baseUrl: "https://cp.test", orgId: "o", sessionId: "s", onEvent: vi.fn(), onError,
			bridge: bridge(async () => ({ error: { status: 401, code: "no_token", message: "Sign in" } })),
		});
		expect(onError.mock.calls[0][0]).toBeInstanceOf(CloudCpAuthError);
	});

	it("keeps a dropped stream's code and request id, kept out of Sentry", async () => {
		const onError = vi.fn();
		const fake = bridge(async () => ({ streamId: "stream-1" }));
		const done = subscribeSessionEventsBridged({ baseUrl: "https://cp.test", orgId: "o", sessionId: "s", onEvent: vi.fn(), onError, bridge: fake });
		await vi.waitFor(() => expect(fake.onStreamEvent).toHaveBeenCalled());
		fake.emit({ type: "error", status: 0, code: CLOUD_UNREACHABLE, message: "terminated", requestId: REQUEST_ID, cause: "UND_ERR_SOCKET" });
		await done;
		expect(onError.mock.calls[0][0]).toMatchObject({ status: 0, code: CLOUD_UNREACHABLE, requestId: REQUEST_ID, networkCause: "UND_ERR_SOCKET", origin: "network" });
		expect(noteCloudFailure).toHaveBeenCalledWith(onError.mock.calls[0][0], { report: false });
	});

	it("names a frame it cannot read CLOUD_MALFORMED_RESPONSE", async () => {
		const onError = vi.fn();
		const fake = bridge(async () => ({ streamId: "stream-1" }));
		const done = subscribeSessionEventsBridged({ baseUrl: "https://cp.test", orgId: "o", sessionId: "s", onEvent: vi.fn(), onError, bridge: fake });
		await vi.waitFor(() => expect(fake.onStreamEvent).toHaveBeenCalled());
		fake.emit({ type: "chunk", data: "data: {not json\n\n" });
		fake.emit({ type: "end" });
		await done;
		expect(onError.mock.calls[0][0]).toMatchObject({ status: 200, code: CLOUD_MALFORMED_RESPONSE });
	});
});
