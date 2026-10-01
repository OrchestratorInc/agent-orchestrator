import type { DashboardSession, OrchestratorLink, ProjectInfo } from "../api";
import type { SessionSourceBoard } from "./board";
import type { EnvironmentKind } from "./types";

/** A source identity is stable only for the currently paired machine or signed-in organization. */
export type SourceRef = Readonly<{ kind: EnvironmentKind; id: string }>;
export type Scoped<T> = Readonly<{ source: SourceRef; value: T }>;

export const sourceKey = (source: SourceRef): string => JSON.stringify([source.kind, source.id]);
export const resourceKey = (source: SourceRef, id: string): string => JSON.stringify([source.kind, source.id, id]);

export type SourceStatus = Readonly<{
	resolved: boolean;
	available: boolean;
	loading: boolean;
	stale: boolean;
	error: string | null;
}>;

export type SourceBoardInput = Readonly<{
	status: SourceStatus;
	snapshot?: Readonly<{ source: SourceRef; board: SessionSourceBoard }>;
}>;

export type ScopedBoard = Readonly<{
	projects: Scoped<ProjectInfo>[];
	sessions: Scoped<DashboardSession>[];
	orchestrators: Scoped<OrchestratorLink>[];
	sources: { local: SourceStatus; cloud: SourceStatus };
}>;

export function composeBoards(input: { local: SourceBoardInput; cloud: SourceBoardInput }): ScopedBoard {
	const slices = [input.local.snapshot, input.cloud.snapshot].filter(
		(slice): slice is NonNullable<SourceBoardInput["snapshot"]> => slice !== undefined,
	);
	const collect = <T,>(pick: (board: SessionSourceBoard) => T[]): Scoped<T>[] =>
		slices.flatMap((slice) => pick(slice.board).map((value) => ({ source: slice.source, value })));
	return {
		projects: collect((board) => board.projects),
		sessions: collect((board) => board.sessions),
		orchestrators: collect((board) => board.orchestrators),
		sources: { local: input.local.status, cloud: input.cloud.status },
	};
}

export function filterScopedWorkers(
	workers: readonly Scoped<DashboardSession>[],
	environment: "all" | EnvironmentKind,
	projectKey: string,
): Scoped<DashboardSession>[] {
	return workers.filter(({ source, value }) =>
		(environment === "all" || source.kind === environment) &&
		(projectKey === "all" || resourceKey(source, value.projectId) === projectKey));
}

export function resolveUnscopedId<T extends { id: string }>(
	id: string,
	entries: readonly Scoped<T>[],
): { kind: "missing" } | { kind: "ambiguous" } | { kind: "found"; entry: Scoped<T> } {
	const matches = entries.filter((entry) => entry.value.id === id);
	if (matches.length === 0) return { kind: "missing" };
	if (matches.length > 1) return { kind: "ambiguous" };
	return { kind: "found", entry: matches[0] };
}
