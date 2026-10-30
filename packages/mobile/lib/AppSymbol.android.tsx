import { isLoaded } from "expo-font";
import { SymbolView } from "expo-symbols";
import regular from "expo-symbols/androidWeights/regular";
import { Text, useWindowDimensions, View } from "react-native";
import type { AppSymbolProps } from "./AppSymbol.types";
import { materialGlyph } from "./symbolCodepoints";
import { resolveSymbol, symbolAccessibilityProps } from "./symbols";
import { useTheme } from "./ThemeProvider";
import { iconSize } from "./tokens";

/**
 * A chrome glyph drawn by the system: a Material Symbol on Android.
 *
 * The glyph is one character of the Material Symbols font. expo-symbols'
 * `SymbolView` draws it too, but every `SymbolView` mounts as if the font were
 * missing and paints an empty box until a load it starts after mounting
 * resolves, even when the root layout loaded the face under the splash. Once
 * the face is in, the character is drawn here directly, styled as `SymbolView`
 * styles it, so an icon arrives with its screen or list row. `SymbolView` stays
 * the fallback for a name without a known codepoint, or a face that has not
 * loaded.
 *
 * `SymbolView` passes its View nothing but its style, so the accessibility
 * props and the caller's style sit on a wrapper either way.
 */
export function AppSymbol({ name, size = iconSize.xl, color, accessibilityLabel, style, testID }: AppSymbolProps) {
	const t = useTheme();
	const { fontScale } = useWindowDimensions();
	const symbol = resolveSymbol(name);
	const tint = color ?? t.textSecondary;
	const glyph = isLoaded(regular.name) ? materialGlyph(symbol.android) : undefined;
	const box = { width: size, height: size };
	return (
		<View style={[box, style]} testID={testID} {...symbolAccessibilityProps(accessibilityLabel)}>
			{glyph !== undefined ? (
				// An icon keeps its size at any system font scale, as M3 and SF
				// Symbols both do, so the character opts out of scaling.
				<Text
					allowFontScaling={false}
					style={{ fontFamily: regular.name, color: tint, fontSize: size, lineHeight: size }}
				>
					{glyph}
				</Text>
			) : (
				<SymbolView
					name={symbol}
					// SymbolView's Text follows the system font scale and offers no
					// way out, so the scale is divided back out of the font size
					// while the box stays put.
					size={size / (fontScale || 1)}
					tintColor={tint}
					style={box}
				/>
			)}
		</View>
	);
}
