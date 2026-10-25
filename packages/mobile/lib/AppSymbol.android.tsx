import { SymbolView } from "expo-symbols";
import { useWindowDimensions, View } from "react-native";
import type { AppSymbolProps } from "./AppSymbol.types";
import { resolveSymbol, symbolAccessibilityProps } from "./symbols";
import { useTheme } from "./ThemeProvider";
import { iconSize } from "./tokens";

/**
 * A chrome glyph drawn by the system: a Material Symbol on Android.
 *
 * expo-symbols draws it as one character of the Material Symbols font inside a
 * plain View, and passes that View nothing but its style, so the accessibility
 * props and the caller's style sit on a wrapper here instead.
 */
export function AppSymbol({ name, size = iconSize.xl, color, accessibilityLabel, style, testID }: AppSymbolProps) {
	const t = useTheme();
	// The glyph is Text, and Text follows the system font scale, which would grow
	// it past its box. An icon keeps its size at any scale (M3 and SF Symbols
	// both do), so the font size is divided back out while the box stays put.
	const { fontScale } = useWindowDimensions();
	const box = { width: size, height: size };
	return (
		<View style={[box, style]} testID={testID} {...symbolAccessibilityProps(accessibilityLabel)}>
			<SymbolView
				name={resolveSymbol(name)}
				size={size / (fontScale || 1)}
				tintColor={color ?? t.textSecondary}
				style={box}
			/>
		</View>
	);
}
