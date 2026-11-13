// Renderer-facing close state deliberately excludes provider identities.
export interface PendingSideClose {
	id: string;
	sessionId: string;
	label: string;
	phase: "unconfirmed" | "cleanup_pending";
}

export type SideCloseOutcome = "closed" | PendingSideClose["phase"];

export interface SideCloseState {
	pending: PendingSideClose[];
	closedIds: string[];
}
