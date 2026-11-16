import type { SymbolWeight } from "expo-symbols";
import type { ColorValue, StyleProp, ViewStyle } from "react-native";
import type { AppSymbolName, PlatformSymbol } from "./symbols";

export type AppSymbolProps = {
	/** A registry name (`"settings"`), or a one-off `{ ios, android }` pair. */
	name: AppSymbolName | PlatformSymbol;
	/**
	 * Edge of the square the glyph draws in. Defaults to each platform's chrome
	 * size: `iconSize.lg` on iOS, and M3's 24dp (`iconSize.xl`) on Android.
	 */
	size?: number;
	/** Defaults to the theme's `textSecondary`, the tint chrome glyphs use. */
	color?: ColorValue;
	/**
	 * iOS, and on Android only the composer's send arrow (see `composerGlyphs`).
	 * Android otherwise draws Material Symbols' regular weight: it is the one M3
	 * specifies, and the only face loaded before the first frame.
	 */
	weight?: SymbolWeight;
	/**
	 * Without one, screen readers skip the glyph and the control around it must
	 * carry the label. Give one only when the glyph stands alone.
	 */
	accessibilityLabel?: string;
	style?: StyleProp<ViewStyle>;
	testID?: string;
};
