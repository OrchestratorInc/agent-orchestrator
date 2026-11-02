// Renderer-facing close state deliberately excludes provider identities.
export interface PendingSideClose {
	id: string;
	sessionId: string;
	label: string;
}
