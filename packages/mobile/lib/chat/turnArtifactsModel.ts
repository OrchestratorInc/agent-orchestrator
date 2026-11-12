import { artifactRelativePath } from "../../../shared/artifacts/turn-artifacts";
import type { ConversationTurn } from "./types";

/**
 * Whether any loaded turn changed the session's artifacts, so a chat that never
 * made one does not read (or, for a session the board does not list, poll) the
 * artifact list.
 */
export function turnsTouchArtifacts(turns: readonly ConversationTurn[] | undefined, sessionId: string): boolean {
	return Boolean(sessionId) && Boolean(turns?.some((turn) =>
		turn.artifacts?.length || turn.diff?.files.some((file) => artifactRelativePath(file.path, sessionId) !== undefined)));
}
