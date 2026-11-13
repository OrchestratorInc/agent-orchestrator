import { Box, Row } from "@expo/ui/jetpack-compose";
import { background, clip, Shapes, size } from "@expo/ui/jetpack-compose/modifiers";
import { paletteSwatchHues, SWATCH_DOT, SWATCH_GAP, type ThemePaletteSwatchProps } from "./ThemePaletteSwatch.types";

export type { ThemePaletteSwatchProps } from "./ThemePaletteSwatch.types";

/**
 * A theme's signature hues as a row of dots, drawn in Compose so it can sit in
 * a Material list row. The dots carry no semantics; TalkBack reads the row's
 * name.
 */
export function ThemePaletteSwatch({ theme }: ThemePaletteSwatchProps) {
	return (
		<Row horizontalArrangement={{ spacedBy: SWATCH_GAP }} verticalAlignment="center">
			{paletteSwatchHues(theme).map((color, index) => (
				<Box key={index} modifiers={[size(SWATCH_DOT, SWATCH_DOT), clip(Shapes.Circle), background(color)]} />
			))}
		</Row>
	);
}
