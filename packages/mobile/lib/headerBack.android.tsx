import type { NativeStackNavigationOptions } from "expo-router";
import { useRouter } from "expo-router";
import { Pressable, type ColorValue } from "react-native";
import Svg, { Path } from "react-native-svg";
import { backOr } from "./backNavigation";
import { usePressFeedback } from "./pressFeedback";
import { useTheme } from "./ThemeProvider";
import { iconSize, touchTarget } from "./tokens";

/**
 * Android's back button drawn as iOS's chevron rather than Material's arrow,
 * in the bar's tint. A native back image did not load from the dev server and
 * left the bar with no way back, so the bar draws it.
 */
export function headerBackOptions(): NativeStackNavigationOptions {
	return { headerLeft: ({ canGoBack, tintColor }) => (canGoBack ? <HeaderBackButton tintColor={tintColor} /> : null) };
}

function HeaderBackButton({ tintColor }: { tintColor?: ColorValue }) {
	const router = useRouter();
	const feedback = usePressFeedback("icon");
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel="Back"
			android_ripple={feedback.android_ripple}
			onPress={() => backOr(router)}
			style={({ pressed }) => [target, pressed && feedback.pressed]}
			testID="header-back"
		>
			<CupertinoChevron color={typeof tintColor === "string" ? tintColor : undefined} />
		</Pressable>
	);
}

/**
 * The iOS back chevron: two strokes meeting at a point, rounded at the ends and
 * the corner, taller than it is wide, as the system's navigation bar draws it.
 */
function CupertinoChevron({ color }: { color?: string }) {
	const t = useTheme();
	return (
		<Svg width={iconSize.xl} height={iconSize.xl} viewBox="0 0 24 24">
			<Path d="M15.5 3.5 L7 12 L15.5 20.5" stroke={color ?? t.accent} strokeWidth={2.6} strokeLinecap="round" strokeLinejoin="round" fill="none" />
		</Svg>
	);
}

const target = { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" } as const;
