import type { EnvironmentKind } from "./types";

export type ConfigLoadPlan = "wait" | "hydrate" | "resolve";

export type ConfigLoadRequest = {
	environment: EnvironmentKind;
	generation: number;
};

/**
 * Cloud keeps the saved Local pairing for a later switch, but never probes it.
 * Endpoint migration and racing only begin after Local has been selected.
 */
export function configLoadPlan(environment: EnvironmentKind | null): ConfigLoadPlan {
	if (environment === null) return "wait";
	return environment === "local" ? "resolve" : "hydrate";
}

/** A Local request that finishes after an environment change must not publish. */
export function shouldPublishConfigLoad(
	request: ConfigLoadRequest,
	current: { environment: EnvironmentKind | null; generation: number },
): boolean {
	return request.environment === current.environment && request.generation === current.generation;
}
