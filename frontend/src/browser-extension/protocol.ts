// Frames between the AO Browser Bridge extension and `ao browser-host`
// (Chrome native messaging). Commands and results mirror the daemon's browser
// runtime protocol, so the extension returns the same result shapes as AO's
// Browser panel and `ao browser` renders them unchanged.

export const HOST_NAME = "dev.ao.browser_bridge";
export const HOST_PROTOCOL = 1;

export type CommandFrame = {
	type: "command";
	requestId: string;
	sessionId: string;
	action: string;
	args?: Record<string, unknown>;
};

export type CancelFrame = { type: "cancel"; requestId: string };

export type HostFrame =
	| CommandFrame
	| { type: "ping" }
	| CancelFrame
	| { type: "ready"; protocol: number }
	| { type: "error"; code: string; message?: string; protocol?: number };

export type ResultFrame = {
	type: "result";
	requestId: string;
	ok: boolean;
	result?: unknown;
	error?: { code: string; message: string };
};

/** A failure with a stable code the daemon maps to an HTTP status. */
export class BridgeError extends Error {
	readonly code: string;

	constructor(code: string, message: string) {
		super(message);
		this.name = "BridgeError";
		this.code = code;
	}
}

export function bridgeError(code: string, message: string): BridgeError {
	return new BridgeError(code, message);
}

export function errorFrame(requestId: string, error: unknown): ResultFrame {
	if (error instanceof BridgeError) {
		return { type: "result", requestId, ok: false, error: { code: error.code, message: error.message } };
	}
	const message = error instanceof Error ? error.message : String(error);
	return { type: "result", requestId, ok: false, error: { code: "BROWSER_COMMAND_FAILED", message } };
}

export function isRecord(value: unknown): value is Record<string, unknown> {
	return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

export function stringArg(args: Record<string, unknown>, name: string, code: string, message: string, allowEmpty = false): string {
	const value = args[name];
	if (typeof value !== "string" || (!allowEmpty && !value.trim())) throw bridgeError(code, message);
	return allowEmpty ? value : value.trim();
}
