import type { components } from "../../api/schema";
import { prBlockReason, type PRBlockReason } from "./pr-display";

export type PRStackInfo = components["schemas"]["GetPRStackResponse"];
export type PRStack = components["schemas"]["PRStackResponse"];
export type PRStackLayer = components["schemas"]["PRStackLayerResponse"];

/**
 * What merging PR `number` lands: the unmerged layers up to it, bottom first.
 * Nothing when it isn't in an open stack, so it merges on its own.
 */
export function stackLayersToLand(stack: PRStack | undefined, number: number): PRStackLayer[] | undefined {
	if (!stack?.open) return undefined;
	const position = stack.layers.findIndex((layer) => layer.number === number);
	if (position < 0) return undefined;
	const layers = stack.layers.slice(0, position + 1).filter((layer) => layer.state !== "merged");
	return layers.length > 0 ? layers : undefined;
}

/** The first layer below this PR that stops the stack merging, and why. */
export function stackBlocker(layers: PRStackLayer[], number: number): { number: number; reason: PRBlockReason } | undefined {
	for (const layer of layers) {
		if (layer.number === number) continue;
		const reason = prBlockReason({
			state: layer.state,
			ci: layer.ci ?? "unknown",
			mergeability: layer.mergeability ?? "unknown",
			review: layer.reviewDecision ?? "none",
			noChecks: layer.noChecks,
		});
		if (reason) return { number: layer.number, reason };
	}
	return undefined;
}

/** Only the top of a stack can take a new layer, and a PR with no stack can start one. */
export function canStackOn(stack: PRStack | undefined, number: number): boolean {
	if (!stack) return true;
	return stack.open && stack.layers[stack.layers.length - 1]?.number === number;
}
