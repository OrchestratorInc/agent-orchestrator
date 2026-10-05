import { classifyConnectionFailure, describeConnectionFailure, type ConnectionErrorCopy } from "./connectionError";
import type { EnvironmentKind } from "./environment/types";
import type { OrchestratorProjectAction } from "./orchestratorView";

export type BoardInteractionMode = "full" | "open-only" | "read-only";

export function boardPresentation(environment: EnvironmentKind | null, configured: boolean): {
	state: "loading" | "cloud-unready" | "unpaired" | "board";
	interactionMode: BoardInteractionMode;
	localControls: boolean;
	spawnControls: boolean;
	showSidebarSessions: boolean;
} {
	return {
		state: environment === null ? "loading" : configured ? "board" : environment === "cloud" ? "cloud-unready" : "unpaired",
		interactionMode: environment === "local" ? "full" : environment === "cloud" ? "open-only" : "read-only",
		localControls: environment === "local",
		spawnControls: environment !== null && configured,
		showSidebarSessions: environment !== null && configured,
	};
}

export function workerInteractionProps<T>(mode: BoardInteractionMode, full: () => T):
	| { interactionMode: "read-only" }
	| { interactionMode: "open-only" }
	| ({ interactionMode: "full" } & T) {
	return mode === "full" ? { ...full(), interactionMode: "full" } : { interactionMode: mode };
}

/** A configured project's orchestrator action is available in both environments. */
export function canUseOrchestratorAction(
	environment: EnvironmentKind | null,
	_action: OrchestratorProjectAction,
): boolean {
	return environment === "local" || environment === "cloud";
}

export function projectDetailState(state: ReturnType<typeof boardPresentation>["state"], hasProject: boolean, loading: boolean, cloudError: boolean) {
	if (state === "loading" || state === "cloud-unready") return state;
	return hasProject ? "project" : loading ? "loading" : cloudError ? "cloud-error" : "not-found";
}

export function boardFailure(
	environment: EnvironmentKind | null,
	status: number | undefined,
	target: Parameters<typeof describeConnectionFailure>[1],
	tunnelRotated = false,
): ConnectionErrorCopy {
	if (environment === "cloud") {
		return {
			title: "Couldn't refresh Cloud",
			icon: "cloud",
			hint: "Check your internet connection and try again.",
			message: "Cloud data couldn't be loaded. Check your internet connection and try again.",
			showLocalNetworkHint: false,
		};
	}
	const reason = classifyConnectionFailure(status);
	return describeConnectionFailure(reason === "unreachable" && tunnelRotated ? "tunnel-rotated" : reason, target);
}

export function boardStaleMessage(environment: EnvironmentKind | null, error: boolean, host: string | undefined, age: string) {
	if (!error) return `Showing data from ${age}`;
	if (environment === "cloud") return "Couldn't refresh Cloud. Try again.";
	return `Can't reach ${host?.trim() || "your desktop"} — showing data from ${age}`;
}
