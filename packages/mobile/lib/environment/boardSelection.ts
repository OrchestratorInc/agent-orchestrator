import type { EnvironmentKind } from "./types";
import { sourceKey, type SourceRef } from "./scopedBoard";

export type SourceRequest = Readonly<{ source: SourceRef; generation: number }>;

export function acceptSourceResult(input: { requested: SourceRequest; current: SourceRequest }): boolean {
	return input.requested.generation === input.current.generation &&
		sourceKey(input.requested.source) === sourceKey(input.current.source);
}

export function sourceMatchesCurrent(
	ref: SourceRef,
	current: { localId: string | null; cloudId: string | null },
	requiredKind?: EnvironmentKind,
): boolean {
	if (requiredKind && ref.kind !== requiredKind) return false;
	return ref.kind === "local" ? ref.id === current.localId : ref.id === current.cloudId;
}

export interface BoardState<Project, Session, Orchestrator = never> {
	projects: Project[];
	sessions: Session[];
	orchestrators: Orchestrator[];
	loading: boolean;
	error: string | null;
}

export type BoardSelection<Project, Session, Orchestrator = never> = {
	kind: "local" | "cloud" | "none";
	state: BoardState<Project, Session, Orchestrator>;
};

/** A Cloud source and its invalidation generation must travel together. */
export type CloudBoardRequest<Source> = {
	source: Source;
	generation: number;
	/** Monotonic within this source; a pull-to-refresh supersedes older polls. */
	sequence?: number;
};

/** Readiness belongs to the selected environment, not a retained Local pairing. */
export function isBoardConfigured(environment: EnvironmentKind | null, sourceKind: EnvironmentKind | undefined, localConfigured: boolean): boolean {
	return environment === "cloud" ? sourceKind === "cloud" : localConfigured;
}

export function boardReadiness(environment: EnvironmentKind | null, sourceKind: EnvironmentKind | undefined, localConfigured: boolean) {
	const configured = isBoardConfigured(environment, sourceKind, localConfigured);
	return { configured, localConfigured };
}

/** Select the board visible for the resolved environment without mixing slices. */
export function selectBoardState<Project, Session, Orchestrator>(input: {
	environment: EnvironmentKind | null;
	sourceKind: EnvironmentKind | undefined;
	local: BoardState<Project, Session, Orchestrator>;
	cloud: BoardState<Project, Session, Orchestrator>;
	empty: BoardState<Project, Session, Orchestrator>;
}): BoardSelection<Project, Session, Orchestrator> {
	if (input.environment === "cloud") {
		return input.sourceKind === "cloud"
			? { kind: "cloud", state: input.cloud }
			: { kind: "none", state: input.empty };
	}
	return { kind: "local", state: input.local };
}

type CloudBoardResult<Project, Session, Orchestrator> =
	| { kind: "success"; projects: Project[]; sessions: Session[]; orchestrators: Orchestrator[] }
	| { kind: "failure"; error: string };

/**
 * Applies a Cloud request result only if the request still belongs to the
 * active source generation and latest request sequence. A failure deliberately
 * preserves the last board.
 */
export function publishCloudBoardResult<Project, Session, Orchestrator>(input: {
	current: BoardState<Project, Session, Orchestrator>;
	requestGeneration: number;
	currentGeneration: number;
	requestSequence?: number;
	currentSequence?: number;
	result: CloudBoardResult<Project, Session, Orchestrator>;
}): BoardState<Project, Session, Orchestrator> | undefined {
	if (input.requestGeneration !== input.currentGeneration) return undefined;
	if (input.requestSequence !== input.currentSequence) return undefined;
	if (input.result.kind === "success") {
		return {
			projects: input.result.projects,
			sessions: input.result.sessions,
			orchestrators: input.result.orchestrators,
			loading: false,
			error: null,
		};
	}
	return { ...input.current, loading: false, error: input.result.error };
}

/**
 * Resolves a request at invocation time so a callback retained across a source
 * switch uses the current committed source instead of its captured predecessor.
 */
export async function dispatchCurrentCloudBoardRequest<Source>(
	current: () => CloudBoardRequest<Source> | undefined,
	load: (request: CloudBoardRequest<Source>) => Promise<void>,
): Promise<void> {
	const request = current();
	if (request) {
		request.sequence = (request.sequence ?? 0) + 1;
		await load({ ...request });
	}
}

/** Blocks daemon-only mutations whenever Local is no longer the active environment. */
export function assertLocalEnvironment(environment: EnvironmentKind | null): asserts environment is "local" {
	if (environment !== "local") {
		throw new Error("Local actions are unavailable outside the Local environment.");
	}
}
