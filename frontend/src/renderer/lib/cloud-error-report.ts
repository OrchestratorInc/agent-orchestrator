// Which AO Cloud failures are worth a Sentry issue. The control plane's
// expected refusals (a plan or usage limit, a sign-in that expired, a
// validation error: every 4xx) are the product working; what is reported is
// AO Cloud failing (5xx), AO Cloud unreachable while this computer is online,
// and a response the app could not read. Main logs every failure and keeps the
// last few for Report a problem (main/cloud-cp-proxy.ts).

import { CLOUD_MALFORMED_RESPONSE, CLOUD_UNREACHABLE, isOfflineNetworkCause, isOpaqueRequestId } from "../../shared/cloud-cp-errors";
import type { CloudCpError } from "./cloud-cp/errors";
import { captureCloudErrorToSentry, type CloudFailureCategory } from "./sentry";

// One report per (category, status, code) per window: an outage fails every
// polling query at once and on every retry.
const REPORT_DEDUPE_MS = 30_000;
const lastReportedAt = new Map<string, number>();

function isOnline(): boolean {
	return typeof navigator === "undefined" || navigator.onLine !== false;
}

/** The Sentry category of an unexpected AO Cloud failure, or undefined for an expected one. */
export function cloudFailureCategory(error: CloudCpError, online = isOnline()): CloudFailureCategory | undefined {
	if (error.code === CLOUD_MALFORMED_RESPONSE) return "malformed_response";
	if (error.status === 0) {
		// Status 0 without the code is the app refusing a request locally.
		if (error.code !== CLOUD_UNREACHABLE) return undefined;
		// Offline is this computer's network, not AO Cloud.
		return online && !isOfflineNetworkCause(error.networkCause) ? "network_error" : undefined;
	}
	return error.status >= 500 ? "http_5xx" : undefined;
}

/**
 * Called with every failed AO Cloud request (cloud-cp/renderer-client.ts) and
 * stream (cloud-cp/stream-bridge.ts). `report: false` keeps a routine failure,
 * such as a live stream dropping, out of Sentry.
 */
export function noteCloudFailure(error: CloudCpError, options: { report?: boolean; now?: number } = {}): void {
	if (options.report === false) return;
	const category = cloudFailureCategory(error);
	if (category === undefined) return;
	const key = `${category}|${error.status}|${error.code ?? ""}`;
	const now = options.now ?? Date.now();
	const last = lastReportedAt.get(key);
	if (last !== undefined && now - last < REPORT_DEDUPE_MS) return;
	lastReportedAt.set(key, now);
	captureCloudErrorToSentry(category, {
		origin: error.origin,
		status: error.status,
		...(error.code ? { code: error.code } : {}),
		...(isOpaqueRequestId(error.requestId) ? { requestId: error.requestId } : {}),
	});
}

/** Test seam. */
export function resetCloudFailureReportsForTest(): void {
	lastReportedAt.clear();
}
