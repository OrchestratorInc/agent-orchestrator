/**
 * The effort control both composers share: the new task's and the chat's. It
 * is drawn as a speedometer whose needle sits at the chosen level, and opens
 * onto the model's levels. Pure, so the needle and the choices are testable.
 */
import type { IOSSymbol } from "./symbols";

export type EffortChoice = { value: string; label: string; selected: boolean };

/**
 * The speedometer for the chosen level: its needle low, mid or high by that
 * level's place among the model's, from the lowest to the highest. With no
 * level chosen, or none the model lists, it points to the middle. (The finer
 * needle positions are SF Symbols 5, newer than the iOS 16.4 the app targets.)
 */
export function effortGaugeSymbol(choices: readonly EffortChoice[]): IOSSymbol {
	const index = choices.findIndex((choice) => choice.selected);
	if (index < 0) return "gauge.medium";
	const fraction = choices.length > 1 ? index / (choices.length - 1) : 1;
	return fraction < 1 / 3 ? "gauge.low" : fraction > 2 / 3 ? "gauge.high" : "gauge.medium";
}

/** A level by name: "high" reads "High". */
export function effortLabel(level: string): string {
	return level ? level[0].toUpperCase() + level.slice(1) : level;
}

/**
 * A new task's effort choices: the levels the picked model takes, with the one
 * chosen, else the model's own default, checked. None when the model takes no
 * effort, so the composer draws no speedometer at all.
 */
export function spawnEffortChoices(model: { efforts?: readonly string[]; defaultEffort?: string } | undefined, chosen: string | undefined): EffortChoice[] {
	const levels = model?.efforts ?? [];
	const selected = chosen && levels.includes(chosen) ? chosen : model?.defaultEffort;
	return levels.map((value) => ({ value, label: effortLabel(value), selected: value === selected }));
}
