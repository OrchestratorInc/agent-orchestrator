import {
	splitTurnArtifacts as splitShared,
	type TurnArtifact as SharedTurnArtifact,
} from "../../../../packages/shared/artifacts/turn-artifacts";
import type { DiffFile, TurnArtifactChange, TurnDiff } from "../types/conversation";
import type { SessionArtifact } from "../types/workspace";

export { artifactRelativePath } from "../../../../packages/shared/artifacts/turn-artifacts";

export type TurnArtifact = SharedTurnArtifact<SessionArtifact>;

export function splitTurnArtifacts(
	diff: TurnDiff | undefined,
	sessionId: string | undefined,
	artifacts: readonly SessionArtifact[] | undefined,
	resolve?: (path: string) => string,
	reported: readonly TurnArtifactChange[] = [],
): { artifacts: TurnArtifact[]; files: DiffFile[] } {
	return splitShared(diff?.files, sessionId, artifacts, resolve, reported);
}
