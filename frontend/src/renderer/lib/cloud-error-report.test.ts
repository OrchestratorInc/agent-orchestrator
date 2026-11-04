import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CLOUD_MALFORMED_RESPONSE, CLOUD_UNREACHABLE } from "../../shared/cloud-cp-errors";
import { CloudCpError } from "./cloud-cp/errors";
import { cloudFailureCategory, noteCloudFailure, resetCloudFailureReportsForTest } from "./cloud-error-report";

const capture = vi.hoisted(() => vi.fn());
vi.mock("./bridge", () => ({ aoBridge: { telemetry: { capture } } }));

const REQUEST_ID = "0f8fad5b-d9cb-469f-a165-70867728950e";

beforeEach(() => resetCloudFailureReportsForTest());
afterEach(() => vi.clearAllMocks());

describe("which AO Cloud failures reach Sentry", () => {
	it("reports AO Cloud failing, unreachable while online, or unreadable; never an expected refusal or offline", () => {
		expect(cloudFailureCategory(new CloudCpError("x", { status: 500, code: "internal_error" }))).toBe("http_5xx");
		expect(cloudFailureCategory(new CloudCpError("x", { status: 0, code: CLOUD_UNREACHABLE, networkCause: "ECONNREFUSED" }), true)).toBe("network_error");
		expect(cloudFailureCategory(new CloudCpError("x", { status: 200, code: CLOUD_MALFORMED_RESPONSE }))).toBe("malformed_response");
		// Plan and usage limits, sign-in, validation: the product working.
		for (const [status, code] of [[429, "WEEKLY_LIMIT_REACHED"], [402, "PLAN_REQUIRED"], [401, "no_token"], [400, "validation_error"], [404, undefined]] as const) {
			expect(cloudFailureCategory(new CloudCpError("x", { status, code }))).toBeUndefined();
		}
		// This computer offline, by the browser or by the failure's cause.
		expect(cloudFailureCategory(new CloudCpError("x", { status: 0, code: CLOUD_UNREACHABLE }), false)).toBeUndefined();
		expect(cloudFailureCategory(new CloudCpError("x", { status: 0, code: CLOUD_UNREACHABLE, networkCause: "ENOTFOUND" }), true)).toBeUndefined();
		expect(cloudFailureCategory(new CloudCpError("x", { status: 0, code: CLOUD_UNREACHABLE, networkCause: "net::ERR_INTERNET_DISCONNECTED" }), true)).toBeUndefined();
		// Status 0 without the code is the app refusing a request itself.
		expect(cloudFailureCategory(new CloudCpError("Invalid request", { status: 0 }), true)).toBeUndefined();
	});

	it("tags origin, status, code and request id, without the message, once per window", () => {
		const error = new CloudCpError("private session text", { status: 502, code: "WORKER_OPERATION_FAILED", requestId: REQUEST_ID });
		noteCloudFailure(error, { now: 1_000 });
		noteCloudFailure(error, { now: 2_000 });
		expect(capture).toHaveBeenCalledOnce();
		const sent = capture.mock.calls[0][0];
		expect(sent).toMatchObject({
			kind: "message",
			message: "AO Cloud http_5xx",
			tags: { surface: "cloud", category: "http_5xx", code: "WORKER_OPERATION_FAILED", http_status: "502", origin: "cloud", request_id: REQUEST_ID },
		});
		expect(JSON.stringify(sent)).not.toContain("private session text");
		noteCloudFailure(error, { now: 40_000 });
		expect(capture).toHaveBeenCalledTimes(2);
	});

	it("keeps a typed 503 a breadcrumb, as the daemon's are", () => {
		noteCloudFailure(new CloudCpError("x", { status: 503, code: "WORKER_UNAVAILABLE" }));
		noteCloudFailure(new CloudCpError("x", { status: 503 }));
		expect(capture.mock.calls.map(([sent]) => sent.kind)).toEqual(["breadcrumb", "message"]);
	});

	it("sends an unreachable AO Cloud as a warning with its request id, and nothing for a failure kept out", () => {
		noteCloudFailure(new CloudCpError("x", { status: 0, code: CLOUD_UNREACHABLE, requestId: REQUEST_ID, networkCause: "UND_ERR_CONNECT_TIMEOUT" }));
		expect(capture).toHaveBeenLastCalledWith(expect.objectContaining({ kind: "message", level: "warning", tags: expect.objectContaining({ origin: "network", http_status: "0", request_id: REQUEST_ID }) }));
		noteCloudFailure(new CloudCpError("x", { status: 500, code: "other" }), { report: false });
		noteCloudFailure(new CloudCpError("x", { status: 500, requestId: "laptop.local/abc-01" }));
		expect(capture).toHaveBeenCalledTimes(2);
		expect(capture.mock.calls[1][0].tags).not.toHaveProperty("request_id");
	});
});
