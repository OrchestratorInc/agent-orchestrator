// The transcript line where one agent handed the chat to another, read the way
// desktop's ProviderSwitchMarker reads it. Pure, so it is unit-testable.
import { agentLabel } from "../../../product-ui/src/agents";
import { effortLabel } from "../modelEffort";
import { menuModelName } from "../spawnModel";
import type { ActivityDetail } from "./types";

export type ProviderSwitchSide = {
	harness: string;
	/** The model id the daemon recorded, when it recorded one. */
	model?: string;
	effort?: string;
	compactWindow?: string;
};

export type ProviderSwitch = { source: ProviderSwitchSide; target: ProviderSwitchSide };

/**
 * Both sides of a switch marker, or undefined for one recorded without harness
 * names (desktop then draws the plain "Switched" label).
 */
export function readProviderSwitch(detail: ActivityDetail | undefined): ProviderSwitch | undefined {
	if (detail?.event !== "provider_switch") return undefined;
	const text = (value: unknown) => (typeof value === "string" && value.trim() ? value.trim() : undefined);
	const source = text(detail.sourceHarness);
	const target = text(detail.targetHarness);
	if (!source || !target) return undefined;
	return {
		source: { harness: source, model: text(detail.sourceModel) },
		target: { harness: target, model: text(detail.targetModel), effort: text(detail.targetEffort), compactWindow: text(detail.targetAutoCompactWindow) },
	};
}

/**
 * One side's name: its model by its catalog name when one was recorded, else
 * the agent's name (desktop's recordedModelName). The marker stores the id.
 */
export function switchSideName(side: ProviderSwitchSide, models: readonly { id: string; label: string }[] = []): string {
	if (!side.model) return agentLabel(side.harness);
	const id = side.model.replace(/\[.*$/, "");
	const label = models.find((entry) => entry.id === id)?.label
		// Claude Code's "opus" is the newest model of that family.
		?? (side.harness === "claude-code" ? models.find((entry) => entry.id.includes(`-${id}-`))?.label : undefined);
	return menuModelName(label ?? id).replace(/\s*\b1M\b/g, "");
}

/** The target side's extras after its name: the effort, then the auto-compact window. */
export function switchSideExtras(side: ProviderSwitchSide): string[] {
	return [
		...(side.effort ? [effortLabel(side.effort)] : []),
		...(side.compactWindow ? [`· ${side.compactWindow.toUpperCase()}`] : []),
	];
}

/** What a screen reader hears for the line, as on desktop. */
export function providerSwitchLabel(change: ProviderSwitch | undefined): string {
	return change ? `Switched from ${agentLabel(change.source.harness)} to ${agentLabel(change.target.harness)}` : "Switched";
}
