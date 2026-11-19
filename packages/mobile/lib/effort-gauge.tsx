import { Pressable, StyleSheet } from "react-native";
import Svg, { Circle, Line, Path } from "react-native-svg";
import { haptics } from "./haptics";
import { effortGaugeNeedle, effortGaugeSegments, type EffortChoice } from "./modelEffort";
import { useTheme } from "./ThemeProvider";
import { iconSize, touchTarget } from "./tokens";
import { markTouchInside } from "./touchOutside";

/*
 * The effort's speedometer in the new task's composer, on both platforms: a
 * segment per level the model takes, lit in the theme's accent to the chosen
 * one, its needle on it. A tap opens the effort slider.
 */

/** The speedometer: an arc of the model's levels, lit to the chosen one, its needle on it. */
export function EffortGauge({ choices, size = iconSize.lg, color }: { choices: readonly EffortChoice[]; size?: number; color: string }) {
	const t = useTheme();
	const { count, span, gap } = effortGaugeSegments(choices.length);
	const index = choices.findIndex((choice) => choice.selected);
	const needle = effortGaugeNeedle(choices);
	const tip = point(needle, 6);
	return (
		<Svg width={size} height={size} viewBox="0 0 24 24">
			{Array.from({ length: count }, (_, i) => {
				const from = 180 - i * (span + gap);
				return (
					<Path
						key={i}
						d={arc(from, from - span, 9)}
						stroke={i <= index ? t.accent : color}
						strokeOpacity={i <= index ? 1 : 0.4}
						strokeWidth={2.4}
						strokeLinecap="round"
						fill="none"
					/>
				);
			})}
			<Line x1={CX} y1={CY} x2={tip.x} y2={tip.y} stroke={color} strokeWidth={2} strokeLinecap="round" />
			<Circle cx={CX} cy={CY} r={1.9} fill={color} />
		</Svg>
	);
}

/** The speedometer as the composer's trigger, a full touch target, naming the level for screen readers. */
export function EffortGaugeButton({ choices, open, onPress, disabled, testID }: { choices: readonly EffortChoice[]; open: boolean; onPress: () => void; disabled: boolean; testID: string }) {
	const t = useTheme();
	const selected = choices.find((choice) => choice.selected);
	return (
		<Pressable
			// Its own tap opens or closes the slider; it is no tap outside it.
			onTouchStart={markTouchInside}
			onPress={() => { haptics.tap(); onPress(); }}
			disabled={disabled}
			accessibilityRole="button"
			accessibilityLabel={`Effort, ${selected?.label ?? "default"}`}
			accessibilityState={{ expanded: open, disabled }}
			testID={testID}
			style={({ pressed }) => [gaugeStyles.target, (pressed || disabled) && gaugeStyles.dim]}
		>
			<EffortGauge choices={choices} color={open ? t.textPrimary : t.textSecondary} />
		</Pressable>
	);
}

// The gauge's centre sits low in its box, as a speedometer's pivot does.
const CX = 12;
const CY = 16;

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
