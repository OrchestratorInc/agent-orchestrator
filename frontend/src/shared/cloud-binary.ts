// The main process's binary channel to the AO Cloud control plane
// (cloudCp:requestBinary), which carries session attachments both ways.

/** The most bytes one binary request carries either way. */
export const MAX_CLOUD_CP_BINARY_BYTES = 32 * 1024 * 1024;

/** How long a binary request may take when its caller names no limit. */
export const DEFAULT_CLOUD_CP_BINARY_TIMEOUT_MS = 120_000;

/** The longest a caller may give a binary request. */
export const MAX_CLOUD_CP_BINARY_TIMEOUT_MS = 15 * 60_000;

/** The error code main answers with when a body or response is over MAX_CLOUD_CP_BINARY_BYTES. */
export const CLOUD_CP_BINARY_TOO_LARGE = "attachment_too_large";

/** The error code main answers with when a request outlives its time. */
export const CLOUD_CP_BINARY_TIMEOUT = "request_timeout";
