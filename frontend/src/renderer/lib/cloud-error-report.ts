// Which AO Cloud failures are worth a Sentry issue: only AO Cloud itself
// failing, that is a 5xx it answered or a response the app could not read.
// The control plane's expected refusals (a plan or usage limit, a sign-in that
// expired, a validation error: every 4xx) are the product working, and AO
// Cloud not being reached is this computer's network or an outage the control
// plane's uptime alarm already reports, so neither is sent. Main logs every
// failure and keeps the last few for Report a problem (main/cloud-cp-proxy.ts).

import { CLOUD_MALFORMED_RESPONSE, isOpaqueRequestId } from "../../shared/cloud-cp-errors";
import type { CloudCpError } from "./cloud-cp/errors";
import { captureCloudErrorToSentry, type CloudFailureCategory } from "./sentry";

// One report per (category, status, code) per window: an outage fails every
// polling query at once and on every retry.
const REPORT_DEDUPE_MS = 30_000;
const lastReportedAt = new Map<string, number>();

/** The Sentry category of an AO Cloud failure worth reporting, or undefined. */
export function cloudFailureCategory(error: CloudCpError): CloudFailureCategory | undefined {
	if (error.origin !== "cloud") return undefined;
	if (error.code === CLOUD_MALFORMED_RESPONSE) return "malformed_response";
	return error.status >= 500 && error.status <= 599 ? "http_5xx" : undefined;
}

/**
 * Called with every failed AO Cloud request (cloud-cp/renderer-client.ts) and
 * stream (cloud-cp/stream-bridge.ts), with the request's route when known
 * (cloudRoute). `report: false` keeps a routine failure, such as a live stream
 * dropping, out of Sentry.
 */
export function noteCloudFailure(error: CloudCpError, options: { report?: boolean; route?: string; now?: number } = {}): void {
	if (options.report === false) return;
	const category = cloudFailureCategory(error);
	if (category === undefined) return;
	const key = `${category}|${error.status}|${error.code ?? ""}`;
	const now = options.now ?? Date.now();
	const last = lastReportedAt.get(key);
	if (last !== undefined && now - last < REPORT_DEDUPE_MS) return;
	lastReportedAt.set(key, now);
	captureCloudErrorToSentry(category, error, {
		status: error.status,
		...(error.code ? { code: error.code } : {}),
		...(isOpaqueRequestId(error.requestId) ? { requestId: error.requestId } : {}),
		...(options.route ? { route: options.route } : {}),
	});
}

/** Test seam. */
export function resetCloudFailureReportsForTest(): void {
	lastReportedAt.clear();
}
