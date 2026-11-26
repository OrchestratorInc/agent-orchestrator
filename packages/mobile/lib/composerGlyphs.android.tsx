import type { SymbolWeight } from "expo-symbols";
import type { ColorValue } from "react-native";
import Svg, { Path, Rect } from "react-native-svg";

/**
 * The composer's round-button glyphs drawn as SF Symbols draws them on iOS:
 * the bold up arrow that sends, the filled microphone that dictates and the
 * filled square that stops.
 *
 * Material Symbols' face is loaded at its regular weight alone, so on Android
 * the arrow came out a hairline and the microphone an outline, the one place
 * the two apps' composers still differed. These follow the SF shapes, rounded
 * caps and all, and take the weight the call site asks iOS for.
 */
export const COMPOSER_GLYPHS = ["arrow.up", "mic.fill", "stop.fill"] as const;

export type ComposerGlyphName = (typeof COMPOSER_GLYPHS)[number];

export function isComposerGlyph(name: string): name is ComposerGlyphName {
	return (COMPOSER_GLYPHS as readonly string[]).includes(name);
}

/** The arrow's stroke on a 24-unit grid, by the weight iOS would draw it at. */
export function arrowStroke(weight: SymbolWeight | undefined): number {
	if (weight === "heavy" || weight === "black") return 3.1;
	if (weight === "bold") return 2.8;
	if (weight === "semibold") return 2.5;
	return 2.2;
}

export function ComposerGlyph({ name, size, color, weight }: { name: ComposerGlyphName; size: number; color: ColorValue; weight?: SymbolWeight }) {
	if (name === "arrow.up") {
		return (
			<Svg width={size} height={size} viewBox="0 0 24 24">
				<Path d="M12 20V4.6M5.4 11.2 12 4.6l6.6 6.6" stroke={color} strokeWidth={arrowStroke(weight)} strokeLinecap="round" strokeLinejoin="round" fill="none" />
			</Svg>
		);
	}
	if (name === "mic.fill") {
		return (
			<Svg width={size} height={size} viewBox="0 0 24 24">
				<Rect x={8.25} y={1.75} width={7.5} height={13} rx={3.75} fill={color} />
				<Path d="M5.25 11.25a6.75 6.75 0 0 0 13.5 0M12 18v3.5" stroke={color} strokeWidth={2} strokeLinecap="round" fill="none" />
			</Svg>
		);
	}
	return (
		<Svg width={size} height={size} viewBox="0 0 24 24">
			<Rect x={5} y={5} width={14} height={14} rx={3} fill={color} />
		</Svg>
	);
}
