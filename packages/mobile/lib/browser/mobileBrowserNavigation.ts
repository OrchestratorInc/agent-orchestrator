import type { MobileBrowserCommandResult } from "./mobileBrowserRuntime";

export type PendingMobileBrowserNavigation = {
	requestId: string;
	started: boolean;
	resolve: (result: MobileBrowserCommandResult) => void;
	timer: ReturnType<typeof setTimeout>;
};

export function failPendingMobileBrowserNavigation(
	pending: PendingMobileBrowserNavigation | null,
	error: { code: string; message: string },
): boolean {
	if (!pending?.started) return false;
	clearTimeout(pending.timer);
	pending.resolve({ ok: false, error });
	return true;
}
