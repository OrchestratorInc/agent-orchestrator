// Where a failure came from, as the main process's control-plane proxy, the
// renderer's cloud client and the diagnostics (logs, Sentry, Report a problem)
// name it: AO Cloud answered with an error, AO Cloud could not be reached, or
// the local daemon refused.

/** cloud: AO Cloud answered with an error. network: it could not be reached. daemon: the local daemon answered. */
export type ErrorOrigin = "cloud" | "network" | "daemon";

/**
 * The control plane could not be reached (DNS, TLS, offline, a timeout or a
 * dropped connection), so there is no HTTP status: the error carries status 0.
 */
export const CLOUD_UNREACHABLE = "CLOUD_UNREACHABLE";

/** The control plane answered, but with a body or stream frame the app could not read. */
export const CLOUD_MALFORMED_RESPONSE = "CLOUD_MALFORMED_RESPONSE";

/** The header carrying a request's correlation id, sent by the desktop and echoed by the control plane. */
export const REQUEST_ID_HEADER = "x-request-id";

/** The header naming the control plane's release on every response. */
export const RELEASE_HEADER = "x-ao-release";

/** An opaque UUID: the only request id shape diagnostics forward, so a host name or free text never rides along. */
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function isOpaqueRequestId(value: unknown): value is string {
	return typeof value === "string" && UUID.test(value);
}

/** A control-plane release label: a short version or build token, never free text. */
const RELEASE = /^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$/;

export function safeCloudRelease(value: unknown): string | undefined {
	return typeof value === "string" && RELEASE.test(value) ? value : undefined;
}

/** A machine error code from an error envelope: an identifier, never a sentence. */
const ERROR_CODE = /^[A-Za-z][A-Za-z0-9_]{0,63}$/;

export function safeErrorCode(value: unknown): string | undefined {
	return typeof value === "string" && ERROR_CODE.test(value) ? value : undefined;
}

/** Failures Sentry drops as expected noise: an abort, or Chromium reporting the network changed or gone. */
export const EXPECTED_NETWORK_NOISE = /^(?:AbortError|net::ERR_NETWORK_CHANGED|net::ERR_INTERNET_DISCONNECTED|net::ERR_NETWORK_IO_SUSPENDED)$/i;

/** Node's names for this computer having no DNS answer or no route: offline, not AO Cloud down. */
const OFFLINE_CAUSES = new Set(["ENOTFOUND", "EAI_AGAIN", "ENETDOWN", "ENETUNREACH", "EHOSTUNREACH", "EHOSTDOWN"]);

/** A fetch failure cause that is offline noise, never a Sentry issue. */
export function isOfflineNetworkCause(cause: string | undefined): boolean {
	return cause !== undefined && (OFFLINE_CAUSES.has(cause) || EXPECTED_NETWORK_NOISE.test(cause));
}

/** A failure cause token (a Node or undici error code, or an error name), never a message. */
const CAUSE = /^[A-Za-z][A-Za-z0-9_:.-]{0,63}$/;

/**
 * The bounded cause of a failed fetch: fetch says only "fetch failed", its
 * cause's code says what failed (ENOTFOUND, ECONNREFUSED, a certificate code).
 */
export function networkFailureCause(error: unknown): string {
	if ((error as { name?: unknown } | null)?.name === "TimeoutError") return "TimeoutError";
	const cause = (error as { cause?: unknown } | null)?.cause as { code?: unknown; name?: unknown } | undefined;
	for (const candidate of [cause?.code, (error as { code?: unknown } | null)?.code, cause?.name, (error as { name?: unknown } | null)?.name]) {
		if (typeof candidate === "string" && CAUSE.test(candidate) && candidate !== "Error" && candidate !== "TypeError") return candidate;
	}
	return "fetch_failed";
}

export function safeNetworkCause(value: unknown): string | undefined {
	return typeof value === "string" && CAUSE.test(value) ? value : undefined;
}
