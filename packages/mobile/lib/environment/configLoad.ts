import type { EnvironmentKind } from "./types";

export type ConfigLoadPlan = "wait" | "hydrate" | "resolve";

/**
 * Cloud keeps the saved Local pairing for a later switch, but never probes it.
 * Endpoint migration and racing only begin after Local has been selected.
 */
export function configLoadPlan(environment: EnvironmentKind | null): ConfigLoadPlan {
	if (environment === null) return "wait";
	return environment === "local" ? "resolve" : "hydrate";
}
