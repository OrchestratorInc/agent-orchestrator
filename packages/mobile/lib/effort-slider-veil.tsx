import { useMemo } from "react";
import { processColor, StyleSheet, View } from "react-native";
import { useTheme } from "./ThemeProvider";
import { space } from "./tokens";

/** How tall the fade above the overlay is, from the page's colour to clear. */
export const VEIL_RISE = 120;
/** How far past the composer's sides it reaches, so it meets the screen's edges. */
const VEIL_BLEED = 24;
/** How far the solid part reaches past the overlay's top, over a composer's padding it would otherwise leave showing. */
const SOLID_OVERHANG = space.md;
/** How much fainter the fade is than its eased curve: the curve's power. */
const FAINT = 2;

// The fade's opacity from its top to its foot, on an ease-in-out curve: a
// straight ramp reads as a hard band where it meets the solid part, and an
// 8-bit dark gradient bands visibly on Android with only two stops.
// Faint: the curve is held low through most of the rise, so the chat above
// stays readable and only thickens near the overlay, still meeting the solid
// part at full cover with no seam.
const FADE = [0, 0.03, 0.1, 0.21, 0.36, 0.53, 0.7, 0.84, 0.94, 1].map((opacity) => opacity ** FAINT);

/**
 * A fade of `color` as a native gradient, `stops` its opacities from where it
 * starts to where it ends. React Native's own gradient, drawn by the GPU with
 * the view: react-native-svg rasterised each gradient into a bitmap on the UI
 * thread, some 25 ms a frame for the veil's two, on the very frames an
 * overlay opens on, and its stops were native views of their own to mount.
 */
export function fadeGradient(color: string, stops: readonly number[], direction: "to bottom" | "to top"): string {
	const argb = Number(processColor(color) ?? 0) >>> 0;
	const [r, g, b] = [(argb >>> 16) & 255, (argb >>> 8) & 255, argb & 255];
	const alpha = ((argb >>> 24) & 255) / 255;
	const steps = stops.map((opacity, i) => `rgba(${r}, ${g}, ${b}, ${(opacity * alpha).toFixed(3)}) ${((i / (stops.length - 1)) * 100).toFixed(1)}%`);
	return `linear-gradient(${direction}, ${steps.join(", ")})`;
}

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
	const riseFade = useMemo(() => fadeGradient(t.bgBase, FADE, "to bottom"), [t.bgBase]);
	const fallFade = useMemo(() => fadeGradient(t.bgBase, FADE, "to top"), [t.bgBase]);
	return (
		<>
			<View pointerEvents="none" style={[styles.solid, { backgroundColor: t.bgBase }]} />
			{fall ? (
				<View
					nativeID={fallNativeID}
					collapsable={fallNativeID ? false : undefined}
					pointerEvents="none"
					style={[styles.fade, { bottom: -space.sm - fall, height: fall, experimental_backgroundImage: fallFade }]}
				/>
			) : null}
			<View pointerEvents="none" style={[styles.fade, { top: -SOLID_OVERHANG - rise, height: rise, experimental_backgroundImage: riseFade }]} />
		</>
	);
}

const styles = StyleSheet.create({
	solid: { position: "absolute", top: -SOLID_OVERHANG, bottom: -space.sm, left: -VEIL_BLEED, right: -VEIL_BLEED },
	fade: { position: "absolute", left: -VEIL_BLEED, right: -VEIL_BLEED },
});
