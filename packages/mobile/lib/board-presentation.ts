import { classifyConnectionFailure, describeConnectionFailure } from "./connectionError";
import type { EnvironmentKind } from "./environment/types";

export type BoardInteractionMode = "full" | "read-only";

export function boardPresentation(environment: EnvironmentKind | null, configured: boolean): {
	state: "loading" | "cloud-unready" | "unpaired" | "board";
	interactionMode: BoardInteractionMode;
	localControls: boolean;
} {
	return {
		state: environment === null ? "loading" : configured ? "board" : environment === "cloud" ? "cloud-unready" : "unpaired",
		interactionMode: environment === "local" ? "full" : "read-only",
		localControls: environment === "local",
	};
}

export function workerInteractionProps<T>(mode: BoardInteractionMode, full: () => T):
	| { interactionMode: "read-only" }
	| ({ interactionMode: "full" } & T) {
	return mode === "read-only" ? { interactionMode: "read-only" } : { ...full(), interactionMode: "full" };
}

export function boardFailure(
	environment: EnvironmentKind | null,
	status: number | undefined,
	target: Parameters<typeof describeConnectionFailure>[1],
	tunnelRotated = false,
) {
	if (environment === "cloud") {
		return {
			title: "Couldn't refresh Cloud",
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
