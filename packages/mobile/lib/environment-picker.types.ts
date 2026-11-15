import type { WorkerEnvironment } from "./worker-controls";

export type EnvironmentPickerProps = {
	environments: readonly WorkerEnvironment[];
	selectedEnvironmentId: string;
	onSelectEnvironment: (environmentId: string) => void;
};
