// Renderer-facing close state deliberately excludes provider identities.
export interface PendingSideClose {
	id: string;
	sessionId: string;
	label: string;
	phase: "unconfirmed" | "cleanup_pending";
}

export type SideCloseOutcome = "closed" | PendingSideClose["phase"];

export interface SideCloseEvidence {
	pending: PendingSideClose[];
	closedIds: string[];
}

export const SIDE_CLOSE_PROTOCOL_VERSION = 1 as const;
export const SIDE_CLOSE_OPERATION_TIMEOUT_MS = 45_000;
export const SIDE_CLOSE_QUERY_TIMEOUT_MS = 3_000;
export type SideCloseStatus = "closing" | "rejected_open" | "unconfirmed" | "cleanup_pending" | "closed";
export interface SideCloseEntry {
	id: string;
	sessionId: string;
	label: string;
	version: number;
	operationId?: string;
	status: SideCloseStatus;
	error?: { code?: string; requestId?: string };
}
export interface SideCloseState {
	protocolVersion: typeof SIDE_CLOSE_PROTOCOL_VERSION;
	sessionId: string | null;
	epoch: string;
	revision: number;
	recovery: "ready" | "restoring" | "unavailable";
	sides: SideCloseEntry[];
}
export interface SideCloseRequest {
	sessionId: string;
	sideId: string;
	requestId: string;
	epoch: string;
	expectedVersion: number;
}
