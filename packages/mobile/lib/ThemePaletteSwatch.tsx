import { Circle, HStack } from "@expo/ui/swift-ui";
import { accessibilityHidden, foregroundStyle, frame } from "@expo/ui/swift-ui/modifiers";
import { paletteSwatchHues, SWATCH_DOT, SWATCH_GAP, type ThemePaletteSwatchProps } from "./ThemePaletteSwatch.types";

export type { ThemePaletteSwatchProps } from "./ThemePaletteSwatch.types";

/**
 * A theme's signature hues as a row of dots before its name, drawn in SwiftUI
 * so it can sit inside a native picker's option. The name beside it is what
 * VoiceOver reads; the dots are decoration.
 */
export function ThemePaletteSwatch({ theme }: ThemePaletteSwatchProps) {
	return (
		<HStack spacing={SWATCH_GAP} modifiers={[accessibilityHidden(true)]}>
			{paletteSwatchHues(theme).map((color, index) => (
				<Circle key={index} modifiers={[foregroundStyle(color), frame({ width: SWATCH_DOT, height: SWATCH_DOT })]} />
			))}
		</HStack>
	);
}
