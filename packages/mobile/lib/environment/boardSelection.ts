import type { EnvironmentKind } from "./types";

export interface BoardState<Project, Session> {
	projects: Project[];
	sessions: Session[];
	loading: boolean;
	error: string | null;
}

export type BoardSelection<Project, Session> = {
	kind: "local" | "cloud" | "none";
	state: BoardState<Project, Session>;
};

/** A Cloud source and its invalidation generation must travel together. */
export type CloudBoardRequest<Source> = {
	source: Source;
	generation: number;
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
export function selectBoardState<Project, Session>(input: {
	environment: EnvironmentKind | null;
	sourceKind: EnvironmentKind | undefined;
	local: BoardState<Project, Session>;
	cloud: BoardState<Project, Session>;
	empty: BoardState<Project, Session>;
}): BoardSelection<Project, Session> {
	if (input.environment === "cloud") {
		return input.sourceKind === "cloud"
			? { kind: "cloud", state: input.cloud }
			: { kind: "none", state: input.empty };
	}
	return { kind: "local", state: input.local };
}

type CloudBoardResult<Project, Session> =
	| { kind: "success"; projects: Project[]; sessions: Session[] }
	| { kind: "failure"; error: string };

/**
 * Applies a Cloud request result only if the request still belongs to the
 * active source generation. A failure deliberately preserves the last board.
 */
export function publishCloudBoardResult<Project, Session>(input: {
	current: BoardState<Project, Session>;
	requestGeneration: number;
	currentGeneration: number;
	result: CloudBoardResult<Project, Session>;
}): BoardState<Project, Session> | undefined {
	if (input.requestGeneration !== input.currentGeneration) return undefined;
	if (input.result.kind === "success") {
		return { projects: input.result.projects, sessions: input.result.sessions, loading: false, error: null };
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
	if (request) await load(request);
}

/** Blocks daemon-only mutations whenever Local is no longer the active environment. */
export function assertLocalEnvironment(environment: EnvironmentKind | null): asserts environment is "local" {
	if (environment !== "local") {
		throw new Error("Local actions are unavailable outside the Local environment.");
	}
}
