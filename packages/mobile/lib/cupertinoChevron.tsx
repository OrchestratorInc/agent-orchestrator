import Svg, { Path } from "react-native-svg";
import { useTheme } from "./ThemeProvider";
import { iconSize } from "./tokens";

/**
 * The iOS back chevron: two strokes meeting at a point, rounded at the ends and
 * the corner, taller than it is wide, as the system's navigation bar draws it.
 */
export function CupertinoChevron({ color, size = iconSize.xl }: { color?: string; size?: number }) {
	const t = useTheme();
	return (
		<Svg width={size} height={size} viewBox="0 0 24 24">
			<Path d="M15.5 3.5 L7 12 L15.5 20.5" stroke={color ?? t.accent} strokeWidth={2.6} strokeLinecap="round" strokeLinejoin="round" fill="none" />
		</Svg>
	);
}
