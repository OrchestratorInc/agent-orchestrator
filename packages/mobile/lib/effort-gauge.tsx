import { Pressable, StyleSheet } from "react-native";
import Svg, { Circle, Defs, Line, LinearGradient, Path, Stop } from "react-native-svg";
import type { FastToggle } from "./effort-slider";
import { haptics } from "./haptics";
import { effortGaugeNeedle, effortGaugeSegments, isUltraEffort, type EffortChoice } from "./modelEffort";
import { useTheme } from "./ThemeProvider";
import { iconSize, touchTarget, ultraGradient } from "./tokens";
import { markTouchInside } from "./touchOutside";
import { useTouchDownPress } from "./useTouchDownPress";

/*
 * The effort's speedometer in the composers, on both platforms: a segment
 * per level the model takes, lit in the theme's accent to the chosen one, its
 * needle on it. A tap opens the effort slider. The slider's switches show on
 * it while they are on, as desktop's model pill shows them: Fast mode as a
 * bolt at its foot, Ultracode as the lit segments in Ultracode's gradient.
 */

/**
 * The slider's switches as the gauge shows them: each one there where it is
 * offered, true while it is on. None where neither is offered.
 */
export type EffortGaugeToggles = { fast?: boolean; ultracode?: boolean };

/** The switches the slider carries, as the gauge shows them; none where it carries neither. */
export function effortGaugeToggles(fast: FastToggle | undefined, ultracode: FastToggle | undefined): EffortGaugeToggles | undefined {
	return fast || ultracode ? { fast: fast?.on, ultracode: ultracode?.on } : undefined;
}

/**
 * What a screen reader hears for the gauge: the level, and each switch that
 * is on. A model with no levels opens the slider only for its switches, so
 * the gauge is named after them rather than after an effort it does not have.
 */
export function effortGaugeLabel(choices: readonly EffortChoice[], toggles?: EffortGaugeToggles): string {
	const on = [toggles?.ultracode && "Ultracode on", toggles?.fast && "Fast mode on"].filter((part): part is string => Boolean(part));
	const offered = [toggles?.ultracode !== undefined && "Ultracode", toggles?.fast !== undefined && "Fast mode"].filter((part): part is string => Boolean(part));
	const lead = choices.length || !offered.length ? `Effort, ${choices.find((choice) => choice.selected)?.label ?? "default"}` : offered.join(" and ");
	return [lead, ...on].join(", ");
}

/** The speedometer: an arc of the model's levels, lit to the chosen one, its needle on it. */
export function EffortGauge({ choices, size = iconSize.lg, color, toggles }: { choices: readonly EffortChoice[]; size?: number; color: string; toggles?: EffortGaugeToggles }) {
	const t = useTheme();
	const { count, span, gap } = effortGaugeSegments(choices.length);
	// Lit in the Ultracode gradient for Claude's Ultracode, and for an agent's
	// own Ultra level (Codex's), as the slider's track is.
	const ultracode = Boolean(toggles?.ultracode) || isUltraEffort(choices.find((choice) => choice.selected)?.value);
	// With no levels the one segment stands for the slider's switches, so it
	// lights for Ultracode.
	const index = choices.length ? choices.findIndex((choice) => choice.selected) : ultracode ? 0 : -1;
	const needle = effortGaugeNeedle(choices);
	const tip = point(needle, 6);
	return (
		<Svg width={size} height={size} viewBox="0 0 24 24">
			{ultracode ? (
				<Defs>
					{/* Across the whole arc, so each lit segment takes its stretch of it. */}
					<LinearGradient id="effort-gauge-ultra" x1={CX - 9} y1="0" x2={CX + 9} y2="0" gradientUnits="userSpaceOnUse">
						{ultraGradient.map((stop, i) => <Stop key={i} offset={i / (ultraGradient.length - 1)} stopColor={stop} />)}
					</LinearGradient>
				</Defs>
			) : null}
			{Array.from({ length: count }, (_, i) => {
				const from = 180 - i * (span + gap);
				return (
					<Path
						key={i}
						d={arc(from, from - span, 9)}
						stroke={i <= index ? (ultracode ? "url(#effort-gauge-ultra)" : t.accent) : color}
						strokeOpacity={i <= index ? 1 : 0.4}
						strokeWidth={2.4}
						strokeLinecap="round"
						fill="none"
					/>
				);
			})}
			<Line x1={CX} y1={CY} x2={tip.x} y2={tip.y} stroke={color} strokeWidth={2} strokeLinecap="round" />
			<Circle cx={CX} cy={CY} r={1.9} fill={color} />
			{/* Fast mode's bolt, filled as the slider's is while on, in the
			    corner under the arc's right end. */}
			{toggles?.fast ? <Path d={BOLT} fill={color} /> : null}
		</Svg>
	);
}

/** The speedometer as the composer's trigger, a full touch target, naming the level and the switches on for screen readers. */
export function EffortGaugeButton({ choices, toggles, open, onPress, disabled, testID }: { choices: readonly EffortChoice[]; toggles?: EffortGaugeToggles; open: boolean; onPress: () => void; disabled: boolean; testID: string }) {
	const t = useTheme();
	// A finger toggles the slider as it lands, not as it lifts, once per
	// touch; a screen reader or keyboard still toggles it (see useTouchDownPress).
	const press = useTouchDownPress(() => { haptics.tap(); onPress(); });
	return (
		<Pressable
			{...press}
			// Its own tap opens or closes the slider; it is no tap outside it.
			onTouchStart={markTouchInside}
			disabled={disabled}
			accessibilityRole="button"
			accessibilityLabel={effortGaugeLabel(choices, toggles)}
			accessibilityState={{ expanded: open, disabled }}
			testID={testID}
			style={({ pressed }) => [gaugeStyles.target, (pressed || disabled) && gaugeStyles.dim]}
		>
			<EffortGauge choices={choices} toggles={toggles} color={open ? t.textPrimary : t.textSecondary} />
		</Pressable>
	);
}

// The gauge's centre sits low in its box, as a speedometer's pivot does.
const CX = 12;
const CY = 16;
// A small bolt in the box's lower right, clear of the arc's end above it.
const BOLT = "M20.4 17.8 L16.9 21.4 H19.1 L18.4 23.8 L22 20 H19.8 Z";

function point(degrees: number, r: number) {
	const radians = (degrees * Math.PI) / 180;
	return { x: CX + r * Math.cos(radians), y: CY - r * Math.sin(radians) };
}

// An arc over the top from one angle to a lower one, clockwise on screen.
function arc(from: number, to: number, r: number): string {
	const a = point(from, r);
	const b = point(to, r);
	return `M ${a.x.toFixed(2)} ${a.y.toFixed(2)} A ${r} ${r} 0 0 1 ${b.x.toFixed(2)} ${b.y.toFixed(2)}`;
}

const gaugeStyles = StyleSheet.create({
	target: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
	dim: { opacity: 0.5 },
});
