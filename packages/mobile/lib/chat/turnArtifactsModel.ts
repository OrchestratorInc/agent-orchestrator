import { artifactRelativePath, splitTurnArtifacts, type TurnArtifact } from "../../../shared/artifacts/turn-artifacts";
import type { SessionArtifact } from "../artifacts";
import type { ConversationTurn, DiffFile } from "./types";

/**
 * Whether any loaded turn changed the session's artifacts, so a chat that never
 * made one does not read (or, for a session the board does not list, poll) the
 * artifact list.
 */
export function turnsTouchArtifacts(turns: readonly ConversationTurn[] | undefined, sessionId: string): boolean {
	return Boolean(sessionId) && Boolean(turns?.some((turn) =>
		turn.artifacts?.length || turn.diff?.files.some((file) => artifactRelativePath(file.path, sessionId) !== undefined)));
}

/**
 * What the end of a turn shows: the deliverables it made as tiles, and the
 * changed files the card lists. Tiles are a settled result, as on desktop, so
 * only a completed turn has them. A file leaves the changed files only for the
 * tile that stands for it: a running, failed, interrupted, cancelled or
 * recovered turn lists its whole diff, so nothing it changed goes unshown.
 */
export function turnResults(
	turn: Pick<ConversationTurn, "state" | "diff" | "artifacts">,
	source: { sessionId: string; artifacts?: readonly SessionArtifact[] } | null | undefined,
): { artifacts: TurnArtifact<SessionArtifact>[]; files: DiffFile[] } {
	const files = turn.diff?.files ?? [];
	if (!source || turn.state !== "completed") return { artifacts: [], files };
	return splitTurnArtifacts(turn.diff?.files, source.sessionId, source.artifacts, undefined, turn.artifacts);
}
