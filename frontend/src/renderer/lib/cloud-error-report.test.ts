import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CLOUD_MALFORMED_RESPONSE, CLOUD_UNREACHABLE } from "../../shared/cloud-cp-errors";
import { CloudCpError } from "./cloud-cp/errors";
import { cloudFailureCategory, noteCloudFailure, resetCloudFailureReportsForTest } from "./cloud-error-report";

const capture = vi.hoisted(() => vi.fn());
vi.mock("./bridge", () => ({ aoBridge: { telemetry: { capture } } }));

const REQUEST_ID = "0f8fad5b-d9cb-469f-a165-70867728950e";
const ROUTE = "POST /orgs/{id}/sessions/{id}/messages";

beforeEach(() => resetCloudFailureReportsForTest());
afterEach(() => vi.clearAllMocks());

describe("which AO Cloud failures reach Sentry", () => {
	it("reports only AO Cloud failing: a 5xx it answered or a response the app could not read", () => {
		expect(cloudFailureCategory(new CloudCpError("x", { status: 500, code: "internal_error" }))).toBe("http_5xx");
		expect(cloudFailureCategory(new CloudCpError("x", { status: 599 }))).toBe("http_5xx");
		expect(cloudFailureCategory(new CloudCpError("x", { status: 200, code: CLOUD_MALFORMED_RESPONSE }))).toBe("malformed_response");
		// Plan and usage limits, sign-in, validation: the product working.
		for (const [status, code] of [[429, "WEEKLY_LIMIT_REACHED"], [402, "PLAN_REQUIRED"], [401, "no_token"], [400, "validation_error"], [404, undefined]] as const) {
			expect(cloudFailureCategory(new CloudCpError("x", { status, code }))).toBeUndefined();
		}
		// AO Cloud not reached, for any cause, and the app refusing a request itself.
		for (const networkCause of ["ECONNREFUSED", "UND_ERR_CONNECT_TIMEOUT", "ENOTFOUND", undefined]) {
			expect(cloudFailureCategory(new CloudCpError("x", { status: 0, code: CLOUD_UNREACHABLE, networkCause }))).toBeUndefined();
		}
		expect(cloudFailureCategory(new CloudCpError("Invalid request", { status: 0 }))).toBeUndefined();
	});

	it("sends an exception tagged cloud with route, status, code and request id, without the message, once per window", () => {
		const error = new CloudCpError("private session text", { status: 502, code: "WORKER_OPERATION_FAILED", requestId: REQUEST_ID });
		noteCloudFailure(error, { route: ROUTE, now: 1_000 });
		noteCloudFailure(error, { route: ROUTE, now: 2_000 });
		expect(capture).toHaveBeenCalledOnce();
		const sent = capture.mock.calls[0][0];
		expect(sent).toMatchObject({
			kind: "exception",
			message: "AO Cloud http_5xx",
			tags: { surface: "cloud", category: "http_5xx", code: "WORKER_OPERATION_FAILED", http_status: "502", origin: "cloud", route: ROUTE, request_id: REQUEST_ID },
			exception: { type: "CloudCpError" },
		});
		expect(JSON.stringify(sent)).not.toContain("private session text");
		noteCloudFailure(error, { now: 40_000 });
		expect(capture).toHaveBeenCalledTimes(2);
	});

	it("carries the app frames that led to the failure, awaited ones included", () => {
		const error = new CloudCpError("x", { status: 500, code: "internal_error" });
		error.stack = [
			"CloudCpError: x",
			"    at errorFromResponse (app://renderer/assets/index-AbCd1234.js:120:15)",
			"    at async send (app://renderer/assets/index-AbCd1234.js:140:7)",
			"    at async startTask (app://renderer/assets/TaskComposer-EfGh5678.js:33:9)",
			"    at somewhere (file:///Users/me/private.js:1:1)",
		].join("\n");
		noteCloudFailure(error);
		expect(capture.mock.calls[0][0].exception.frames).toEqual([
			{ function: "renderer_frame", filename: "assets/TaskComposer-EfGh5678.js", lineno: 33, colno: 9 },
			{ function: "renderer_frame", filename: "assets/index-AbCd1234.js", lineno: 140, colno: 7 },
			{ function: "renderer_frame", filename: "assets/index-AbCd1234.js", lineno: 120, colno: 15 },
		]);
	});

	it("keeps a typed 503 a breadcrumb, as the daemon's are", () => {
		noteCloudFailure(new CloudCpError("x", { status: 503, code: "WORKER_UNAVAILABLE" }));
		noteCloudFailure(new CloudCpError("x", { status: 503 }));
		expect(capture.mock.calls.map(([sent]) => sent.kind)).toEqual(["breadcrumb", "exception"]);
		expect(capture.mock.calls[0][0]).not.toHaveProperty("exception");
	});

	it("sends nothing for AO Cloud unreachable or a failure kept out, and no request id that is not opaque", () => {
		noteCloudFailure(new CloudCpError("x", { status: 0, code: CLOUD_UNREACHABLE, requestId: REQUEST_ID, networkCause: "UND_ERR_CONNECT_TIMEOUT" }));
		noteCloudFailure(new CloudCpError("x", { status: 500, code: "other" }), { report: false });
		expect(capture).not.toHaveBeenCalled();
		noteCloudFailure(new CloudCpError("x", { status: 500, requestId: "laptop.local/abc-01" }));
		expect(capture).toHaveBeenCalledOnce();
		expect(capture.mock.calls[0][0].tags).not.toHaveProperty("request_id");
	});
});
