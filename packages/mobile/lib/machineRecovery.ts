import type { ConnectionFailure } from "./connectionError";

/** Failures where a fresh desktop QR code can replace stale endpoints or credentials. */
export function canRepairWithQr(failure: ConnectionFailure | null): boolean {
	return failure === "unreachable" || failure === "tunnel-rotated" || failure === "auth";
}
