import type { components } from "../../api/schema";
import { prBlockReason, type PRBlockReason } from "./pr-display";

export type PRStackInfo = components["schemas"]["GetPRStackResponse"];
export type PRStack = components["schemas"]["PRStackResponse"];
export type PRStackLayer = components["schemas"]["PRStackLayerResponse"];

export function stackLayersToLand(stack: PRStack | undefined, number: number): PRStackLayer[] | undefined {
	if (!stack?.open) return undefined;
	const position = stack.layers.findIndex((layer) => layer.number === number);
	if (position < 0) return undefined;
	const layers = stack.layers.slice(0, position + 1).filter((layer) => layer.state !== "merged");
	return layers.length > 0 ? layers : undefined;
}

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

export function canStackOn(stack: PRStack | undefined, number: number): boolean {
	if (!stack) return true;
	return stack.open && stack.layers[stack.layers.length - 1]?.number === number;
}
