import type { ServerConfig } from "../config";
import type { ConnectOptions } from "../connectRuntime";
import type { Endpoint } from "../endpoints";
import { configLoadPlan } from "./configLoad";
import type { EnvironmentKind } from "./types";

export type ConfigLoadPublication = {
	config: ServerConfig;
	endpoints: Endpoint[];
	raced: boolean;
};

export type ConfigLoadControllerDeps = {
	loadSaved(): Promise<ServerConfig>;
	resolveActive(options?: ConnectOptions): Promise<ServerConfig | null>;
	loadEndpoints(): Promise<Endpoint[]>;
};

/**
 * Owns config-load dispatch and invalidation. The legacy environment gate is
 * retained for older callers; the combined board explicitly enables Local
 * resolution regardless of the selected Cloud view.
 */
export class ConfigLoadController {
	private environment: EnvironmentKind | null = null;
	private localEnabled: boolean | null = null;
	private generation = 0;

	constructor(
		private readonly deps: ConfigLoadControllerDeps,
		private readonly publish: (result: ConfigLoadPublication) => void,
	) {}

	setEnvironment(environment: EnvironmentKind | null): void {
		if (this.environment === environment) return;
		this.environment = environment;
		if (this.localEnabled === null) this.generation += 1;
	}

	/** New combined board: resolve the paired desktop independently of the saved view. */
	setLocalEnabled(enabled: boolean): void {
		if (this.localEnabled === enabled) return;
		this.localEnabled = enabled;
		this.generation += 1;
	}

	async reload(options?: ConnectOptions): Promise<ServerConfig | null> {
		const environment = this.localEnabled === null ? this.environment : this.localEnabled ? "local" : "cloud";
		const plan = configLoadPlan(environment);
		if (plan === "wait" || environment === null) return null;
		const generation = this.generation;
		const current = () => this.generation === generation;

		if (plan === "hydrate") {
			const config = await this.deps.loadSaved();
			if (current()) this.publish({ config, endpoints: [], raced: false });
			return current() ? config : null;
		}

		const config = (await this.deps.resolveActive(options)) ?? (current() ? await this.deps.loadSaved() : null);
		if (!config || !current()) return null;
		const endpoints = await this.deps.loadEndpoints();
		if (!current()) return null;
		this.publish({ config, endpoints, raced: true });
		return config;
	}
}
