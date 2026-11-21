import { Pressable } from "react-native";
import Svg, { Circle } from "react-native-svg";
import { useTheme } from "../ThemeProvider";
import { touchTarget } from "../tokens";
import { contextReadout } from "./conversationChrome";
import type { ConversationUsage } from "./types";

const SIZE = 20;
const STROKE = 2.5;

/**
 * The desktop composer's context ring, beside the attach button: how full the
 * context window is, as a ring that fills clockwise. Quiet until 70%, amber
 * from there and red from 90%, as the meter chip turns. It opens the usage
 * sheet. Absent when the agent reports no context window.
 */
export function ContextRing({ usage, onPress }: { usage?: ConversationUsage; onPress?(): void }) {
	const t = useTheme();
	const readout = contextReadout(usage);
	if (!readout || readout.percent === undefined || readout.fillPercent === undefined) return null;
	const color = readout.severity === "critical" ? t.red : readout.severity === "warn" ? t.amber : t.textSecondary;
	const r = (SIZE - STROKE) / 2;
	const circumference = 2 * Math.PI * r;
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={`Context window, ${readout.percent}% used`}
			accessibilityHint="Opens context and usage"
			disabled={!onPress}
			onPress={onPress}
			hitSlop={6}
			style={{ width: 26, height: touchTarget, alignItems: "center", justifyContent: "center" }}
			testID="composer-context-ring"
		>
			<Svg width={SIZE} height={SIZE} style={{ transform: [{ rotate: "-90deg" }] }}>
				<Circle cx={SIZE / 2} cy={SIZE / 2} r={r} stroke={t.borderStrong} strokeWidth={STROKE} fill="none" />
				<Circle
					cx={SIZE / 2}
					cy={SIZE / 2}
					r={r}
					stroke={color}
					strokeWidth={STROKE}
					fill="none"
					strokeLinecap="round"
					strokeDasharray={`${circumference} ${circumference}`}
					strokeDashoffset={circumference * (1 - readout.fillPercent / 100)}
				/>
			</Svg>
		</Pressable>
	);
}
