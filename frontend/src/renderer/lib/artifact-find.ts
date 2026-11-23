import { createArtifactSearchEngine } from "../../../../backend/internal/renderpage/artifact_find.js";
import foldExceptions from "./chat-find-fold.json";

export const createArtifactFindEngine = () => createArtifactSearchEngine(foldExceptions);
export { foldExceptions };

export type ArtifactFindResult = {
	total: number;
	/** Preserve the same live DOM occurrence after sorting or content updates. */
	active?: number;
	/** A warning means the count does not cover the whole document. */
	warning?: "partial" | "unavailable" | "noText";
	paint: (active: number, reveal: boolean) => void;
	clear: () => void;
};
export type ArtifactFindAdapter = {
	search: (query: string, signal: AbortSignal) => Promise<ArtifactFindResult>;
};

export interface ArtifactFindMessage {
	jsonrpc: "2.0";
	method: "ao/find-configure" | "ao/find" | "ao/find-started" | "ao/find-cancel" | "ao/find-error" | "ao/find-result" | "ao/find-open" | "ao/find-close";
	params: Record<string, unknown>;
}

/** Ignore another frame, another request, and malformed or implausible counts. */
export function readArtifactFindResult(data: unknown, generation: number): { total: number; active?: number } | undefined {
	if (!data || typeof data !== "object") return;
	const message = data as Partial<ArtifactFindMessage>;
	if (message.jsonrpc !== "2.0" || message.method !== "ao/find-result" || message.params?.generation !== generation) return;
	const total = message.params.total;
	const active = message.params.active;
	if (typeof total === "number" && Number.isSafeInteger(total) && total >= 0) {
		if (active === undefined) return { total };
		if (typeof active === "number" && Number.isSafeInteger(active) && active >= 0 && active < Math.max(1, total)) return { total, active };
	}
	return undefined;
}
