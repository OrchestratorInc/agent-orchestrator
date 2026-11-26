import { StyleSheet, View } from "react-native";
import Svg, { Defs, LinearGradient, Rect, Stop } from "react-native-svg";
import { useTheme } from "./ThemeProvider";
import { space } from "./tokens";

/** How tall the fade above the overlay is, from the page's colour to clear. */
export const VEIL_RISE = 120;
/** How far past the composer's sides it reaches, so it meets the screen's edges. */
const VEIL_BLEED = 24;
/** How far the solid part reaches past the overlay's top, over a composer's padding it would otherwise leave showing. */
const SOLID_OVERHANG = space.md;

// The fade's opacity from its top to its foot, on an ease-in-out curve: a
// straight ramp reads as a hard band where it meets the solid part, and an
// 8-bit dark gradient bands visibly on Android with only two stops.
const FADE = [0, 0.03, 0.1, 0.21, 0.36, 0.53, 0.7, 0.84, 0.94, 1];

/**
 * A veil of the page's own colour behind a composer overlay, edge to edge:
 * solid behind the overlay, so whatever it sits on (the composer it covers,
 * the chat it floats over) is hidden rather than ghosting through, then a
 * fade above it into the conversation, even across the screen's width. It
 * takes no touches, so a tap on the chat through it still closes the overlay.
 */
export function EffortSliderVeil({ rise = VEIL_RISE, fall = 0, fallNativeID }: {
	rise?: number;
	/** How far it also fades down past the overlay's foot: over the keys' top rows, for an overlay above the keyboard. */
	fall?: number;
	/** Names the fall's view for a native window that draws it (Android's over-keyboard overlay). */
	fallNativeID?: string;
}) {
	const t = useTheme();
	return (
		<>
			<View pointerEvents="none" style={[styles.solid, { backgroundColor: t.bgBase }]} />
			{fall ? (
				<View nativeID={fallNativeID} collapsable={fallNativeID ? false : undefined} pointerEvents="none" style={[styles.fade, { bottom: -space.sm - fall, height: fall }]}>
					<Svg width="100%" height="100%" preserveAspectRatio="none" viewBox="0 0 1 1">
						<Defs>
							<LinearGradient id="composer-overlay-veil-fall" x1="0" y1="1" x2="0" y2="0">
								{FADE.map((opacity, i) => (
									<Stop key={i} offset={i / (FADE.length - 1)} stopColor={t.bgBase} stopOpacity={opacity} />
								))}
							</LinearGradient>
						</Defs>
						<Rect x="0" y="0" width="1" height="1" fill="url(#composer-overlay-veil-fall)" />
					</Svg>
				</View>
			) : null}
			<View pointerEvents="none" style={[styles.fade, { top: -SOLID_OVERHANG - rise, height: rise }]}>
				<Svg width="100%" height="100%" preserveAspectRatio="none" viewBox="0 0 1 1">
					<Defs>
						<LinearGradient id="composer-overlay-veil" x1="0" y1="0" x2="0" y2="1">
							{FADE.map((opacity, i) => (
								<Stop key={i} offset={i / (FADE.length - 1)} stopColor={t.bgBase} stopOpacity={opacity} />
							))}
						</LinearGradient>
					</Defs>
					<Rect x="0" y="0" width="1" height="1" fill="url(#composer-overlay-veil)" />
				</Svg>
			</View>
		</>
	);
}

const styles = StyleSheet.create({
	solid: { position: "absolute", top: -SOLID_OVERHANG, bottom: -space.sm, left: -VEIL_BLEED, right: -VEIL_BLEED },
	fade: { position: "absolute", left: -VEIL_BLEED, right: -VEIL_BLEED },
});
