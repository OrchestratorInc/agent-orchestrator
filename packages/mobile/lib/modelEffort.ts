/**
 * The effort control both composers share: the new task's and the chat's. It
 * is drawn as a speedometer whose needle sits at the chosen level, and opens
 * onto the model's levels. Pure, so the needle and the choices are testable.
 */

export type EffortChoice = { value: string; label: string; selected: boolean };

/** A level by name: "high" reads "High". */
export function effortLabel(level: string): string {
	return level ? level[0].toUpperCase() + level.slice(1) : level;
}

/**
 * A new task's effort choices: the levels the picked model takes, with the one
 * chosen, else the model's own default, else the fallback level, checked. None when the model takes no
 * effort, so the composer draws no speedometer at all.
 */
export function spawnEffortChoices(model: { efforts?: readonly string[]; defaultEffort?: string } | undefined, chosen: string | undefined): EffortChoice[] {
	const levels = model?.efforts ?? [];
	const selected = chosen && levels.includes(chosen) ? chosen : model?.defaultEffort;
	return withFallbackEffort(levels.map((value) => ({ value, label: effortLabel(value), selected: value === selected })));
}

/**
 * The level to show when nothing names one, neither a pick nor the model's
 * default: Medium where the model has it, else the middle level, the lower of
 * the two middles for an even count.
 */
export function fallbackEffortIndex(levels: readonly string[]): number {
	const medium = levels.findIndex((level) => level.toLowerCase() === "medium");
	return medium >= 0 ? medium : Math.floor((levels.length - 1) / 2);
}

/** The choices with the fallback level checked when none is. */
export function withFallbackEffort(choices: readonly EffortChoice[]): EffortChoice[] {
	if (!choices.length || choices.some((choice) => choice.selected)) return [...choices];
	const index = fallbackEffortIndex(choices.map((choice) => choice.value));
	return choices.map((choice, i) => ({ ...choice, selected: i === index }));
}

/**
 * The custom speedometer's segments, one per level across its half circle:
 * each one's sweep and the gap between them, in degrees.
 */
export function effortGaugeSegments(count: number): { count: number; span: number; gap: number } {
	const n = Math.max(count, 1);
	const gap = n > 1 ? 14 : 0;
	return { count: n, span: (180 - gap * (n - 1)) / n, gap };
}

/**
 * Where the speedometer's needle points, in degrees from the right (0) over
 * the top to the left (180): the middle of the chosen level's segment, or
 * straight up with no level chosen.
 */
export function effortGaugeNeedle(choices: readonly EffortChoice[]): number {
	const index = choices.findIndex((choice) => choice.selected);
	if (index < 0) return 90;
	const { span, gap } = effortGaugeSegments(choices.length);
	return 180 - index * (span + gap) - span / 2;
}

/**
 * The effort slider's stop nearest a point `x` along its track, the stops
 * `step` apart from `start`.
 */
export function nearestStop(x: number, start: number, step: number, count: number): number {
	if (count <= 1 || step <= 0) return 0;
	return Math.min(count - 1, Math.max(0, Math.round((x - start) / step)));
}

/**
 * How far the slider's blob sits from its nearest stop when the finger is
 * `offset` from it: pulled in, hardest near the stop, so the blob clings to a
 * level and snaps over to the next past halfway. It meets the finger exactly
 * at the halfway point, so the blob never jumps.
 */
export function magneticOffset(offset: number, step: number): number {
	const half = step / 2;
	if (half <= 0) return 0;
	const reach = Math.min(Math.abs(offset) / half, 1);
	return Math.sign(offset) * half * reach * reach;
}
