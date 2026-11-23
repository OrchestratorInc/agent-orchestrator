import { StyleSheet, View } from "react-native";
import Svg, { Defs, Ellipse, RadialGradient, Stop } from "react-native-svg";
import { useTheme } from "./ThemeProvider";
import { space } from "./tokens";

/** How far above the slider's heading the veil reaches before it is gone. */
export const VEIL_RISE = 112;
/** How far past the composer's sides it reaches, so it meets the screen's edges. */
const VEIL_BLEED = 24;

/**
 * A veil of the page's own colour behind the effort slider, from the card up
 * past its heading and across the whole screen: solid behind the card and the
 * words, then a soft arch fading into the conversation above, highest in the
 * middle and curving down at both ends, so the heading
 * reads over a busy chat without a box around it. It takes no touches, so a
 * tap on the chat through it still closes the slider.
 */
export function EffortSliderVeil() {
	const t = useTheme();
	return (
		<View pointerEvents="none" style={styles.veil}>
			<Svg width="100%" height="100%" preserveAspectRatio="none" viewBox="0 0 100 100">
				<Defs>
					{/* An arch rather than a flat edge: an ellipse centred on the bottom
					    middle and wider than the screen, the gradient fitted to it, so it
					    rises highest behind the heading and its two ends curve down
					    toward the screen's sides. (A gradient given its own rx and ry is
					    drawn as a circle on Android.) */}
					<RadialGradient id="effort-slider-veil" cx="50%" cy="50%" r="50%" gradientUnits="objectBoundingBox">
						<Stop offset="0" stopColor={t.bgBase} stopOpacity={1} />
						<Stop offset="0.58" stopColor={t.bgBase} stopOpacity={1} />
						<Stop offset="0.72" stopColor={t.bgBase} stopOpacity={0.88} />
						<Stop offset="0.87" stopColor={t.bgBase} stopOpacity={0.42} />
						<Stop offset="1" stopColor={t.bgBase} stopOpacity={0} />
					</RadialGradient>
				</Defs>
				<Ellipse cx="50" cy="100" rx="72" ry="100" fill="url(#effort-slider-veil)" />
			</Svg>
		</View>
	);
}

const styles = StyleSheet.create({
	// Down to the composer, across the gap the card floats above it.
	veil: { position: "absolute", top: -VEIL_RISE, bottom: -space.sm, left: -VEIL_BLEED, right: -VEIL_BLEED },
});
