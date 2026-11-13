import {
	splitTurnArtifacts as splitShared,
	type TurnArtifact as SharedTurnArtifact,
} from "../../../../packages/shared/artifacts/turn-artifacts";
import type { DiffFile, TurnArtifactChange, TurnDiff } from "../types/conversation";
import type { SessionArtifact } from "../types/workspace";

export { artifactRelativePath } from "../../../../packages/shared/artifacts/turn-artifacts";

/** A deliverable this turn created or edited, with how the turn changed it. */
export type TurnArtifact = SharedTurnArtifact<SessionArtifact>;

/**
 * Splits what a turn changed into the deliverables it touched and every other
 * changed file. The rule lives in packages/shared so the phone draws the same
 * cards; see its splitTurnArtifacts.
 */
export function splitTurnArtifacts(
	diff: TurnDiff | undefined,
	sessionId: string | undefined,
	artifacts: readonly SessionArtifact[] | undefined,
	resolve?: (path: string) => string,
	reported: readonly TurnArtifactChange[] = [],
): { artifacts: TurnArtifact[]; files: DiffFile[] } {
	return splitShared(diff?.files, sessionId, artifacts, resolve, reported);
}
