import { SymbolView } from "expo-symbols";
import type { AppSymbolProps } from "./AppSymbol.types";
import { resolveSymbol, symbolAccessibilityProps } from "./symbols";
import { useTheme } from "./ThemeProvider";
import { iconSize } from "./tokens";

/**
 * A chrome glyph drawn by the system: an SF Symbol on iOS. The native view
 * takes the accessibility props itself, so nothing wraps it here.
 */
export function AppSymbol({ name, size = iconSize.lg, color, weight, accessibilityLabel, style, testID }: AppSymbolProps) {
	const t = useTheme();
	return (
		<SymbolView
			name={resolveSymbol(name)}
			size={size}
			// Always set: without a tint the native view falls back to the system blue.
			tintColor={color ?? t.textSecondary}
			weight={weight}
			style={style}
			testID={testID}
			{...symbolAccessibilityProps(accessibilityLabel)}
		/>
	);
}
