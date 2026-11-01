import type { WorkerEnvironmentFilterProps } from "./worker-environment-filter.types";

export type { WorkerEnvironmentFilterProps } from "./worker-environment-filter.types";

/**
 * iOS has no row of filter chips under a list: the choice of machine is the
 * Environment submenu of the header's filter menu, so there is nothing to draw
 * here. Android's Material chips are ./worker-environment-filter.android.
 */
export function WorkerEnvironmentFilter(_props: WorkerEnvironmentFilterProps): null {
	return null;
}
