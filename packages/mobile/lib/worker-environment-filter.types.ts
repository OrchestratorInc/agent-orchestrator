import type { WorkerEnvironment } from "./worker-controls";

export type WorkerEnvironmentFilterProps = {
	environments: readonly WorkerEnvironment[];
	selectedEnvironmentId: string;
	onSelectEnvironment: (environmentId: string) => void;
};
