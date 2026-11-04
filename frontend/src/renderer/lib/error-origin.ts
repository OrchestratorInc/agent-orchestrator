// Where a failed action's error came from, for the UI hint and the telemetry
// `source` property: AO Cloud answering with an error ("cloud"), AO Cloud not
// reached ("network"), or the local daemon ("daemon").

import { isOpaqueRequestId, safeErrorCode, type ErrorOrigin } from "../../shared/cloud-cp-errors";
import { CloudCpError } from "./cloud-cp/errors";

/** What the UI shows beside an AO Cloud error: which kind it is and the request id to quote. */
export type CloudErrorReference = { origin: "cloud" | "network"; requestId?: string };

/** An error that carries an AO Cloud failure's origin after being rewrapped (TaskComposer's TaskCreateError). */
type CarriesCloudOrigin = { cloudOrigin?: "cloud" | "network"; requestId?: unknown };

/** The AO Cloud origin and request id of an error, or undefined when it is not an AO Cloud failure. */
export function cloudErrorReference(error: unknown): CloudErrorReference | undefined {
	if (error instanceof CloudCpError) {
		return { origin: error.origin, ...(isOpaqueRequestId(error.requestId) ? { requestId: error.requestId } : {}) };
	}
	const carried = error as CarriesCloudOrigin | null | undefined;
	if (carried?.cloudOrigin === "cloud" || carried?.cloudOrigin === "network") {
		return { origin: carried.cloudOrigin, ...(isOpaqueRequestId(carried.requestId) ? { requestId: carried.requestId } : {}) };
	}
	return undefined;
}

/**
 * The failure event properties of an action that called AO Cloud or the
 * daemon (`side`): `source` and the error's machine code. A cloud-side action
 * that failed before any request (the app refused it) has no source.
 */
export function failureTelemetry(error: unknown, side: "cloud" | "daemon"): { source?: ErrorOrigin; error_code?: string } {
	const reference = cloudErrorReference(error);
	const source: ErrorOrigin | undefined = reference?.origin ?? (side === "daemon" ? "daemon" : undefined);
	const code = safeErrorCode((error as { code?: unknown } | null | undefined)?.code);
	return { ...(source ? { source } : {}), ...(source && code ? { error_code: code } : {}) };
}
