// Application errors carry a stable code independent of their display text.
export class AoError extends Error {
	constructor(readonly code: string, message: string, options?: ErrorOptions) {
		super(message, options);
	}
}

export function safeSentryCode(value: unknown): string | undefined {
	return typeof value === "string" && /^[A-Z][A-Z0-9_]{2,63}$/.test(value) ? value : undefined;
}

export function sentryErrorCode(error: unknown): string | undefined {
	// Bound traversal for cyclic causes and non-Error rejection objects.
	let fallback: string | undefined;
	for (let depth = 0; depth < 16 && error !== null && typeof error === "object"; depth++) {
		try {
			const candidate = error as { code?: unknown; cause?: unknown; telemetryCode?: unknown };
			const safe = safeSentryCode(candidate.code);
			if (safe) return safe;
			fallback ??= safeSentryCode(candidate.telemetryCode);
			error = candidate.cause;
		} catch {
			break;
		}
	}
	return fallback;
}
