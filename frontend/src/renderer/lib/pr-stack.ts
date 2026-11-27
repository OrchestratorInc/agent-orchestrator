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

/** Why a stack can't merge: a layer's own block, or a layer still to be rebased onto the one below. */
export type StackBlockReason = PRBlockReason | "rebase";

/**
 * The first layer that stops the stack merging, and why: one below this PR that
 * isn't ready, or any layer up to this one that is behind the layer below it.
 */
export function stackBlocker(layers: PRStackLayer[], number: number): { number: number; reason: StackBlockReason } | undefined {
	for (const layer of layers) {
		if ((layer.behindParent ?? 0) > 0) return { number: layer.number, reason: "rebase" };
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

/**
 * The layer to rebase next: the lowest open one missing commits from the layer
 * below it. Each rebase moves its head, so the layer above shows behind next.
 */
export function stackLayerToRebase(stack: PRStack | undefined): { layer: PRStackLayer; below: PRStackLayer } | undefined {
	if (!stack?.open) return undefined;
	for (let i = 1; i < stack.layers.length; i++) {
		const layer = stack.layers[i];
		if ((layer.behindParent ?? 0) > 0 && (layer.state === "open" || layer.state === "draft")) return { layer, below: stack.layers[i - 1] };
	}
	return undefined;
}

/** What the agent on `layer` is asked to do to bring it back onto `below`. */
export function stackRebaseMessage(layer: PRStackLayer, below: PRStackLayer): string {
	const branch = `\`${layer.headBranch}\``;
	const onto = `\`origin/${below.headBranch}\``;
	return [
		`#${layer.number} (${branch}) is behind #${below.number} (\`${below.headBranch}\`), the pull request below it in its stack.`,
		`Rebase ${branch} onto ${onto}: run \`git fetch origin\`, then \`git rebase origin/${below.headBranch}\`.`,
		`If #${below.number}'s commits were rewritten, move only this branch's own commits with \`git rebase --onto origin/${below.headBranch} <the commit this branch started from>\`, so its old copies don't come along.`,
		"Resolve any conflicts, then push with `git push --force-with-lease`. Leave the other layers alone.",
	].join("\n");
}
