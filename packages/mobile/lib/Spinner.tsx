import { ActivityIndicator, Platform, View, type AccessibilityProps, type ColorValue, type StyleProp, type ViewStyle } from "react-native";
import { useTheme } from "./ThemeProvider";

/** UIActivityIndicatorView's `medium` style is a fixed 20pt square. */
const IOS_SMALL_EDGE = 20;

export type SpinnerProps = {
	/**
	 * `small` and `large` are the system's own sizes. A number draws the
	 * indicator in a square of that edge, for one sitting in a line of text or
	 * in a glyph's slot: Android sizes its native view to it, and iOS, whose
	 * indicator comes in the two sizes only, scales the small one.
	 */
	size?: "small" | "large" | number;
	/** Defaults to the theme's `textSecondary`. */
	color?: ColorValue;
	/**
	 * Without one, screen readers skip the spinner and the text beside it
	 * carries the state. Give one only when the spinner stands alone.
	 */
	accessibilityLabel?: string;
	style?: StyleProp<ViewStyle>;
	testID?: string;
};

function spinnerAccessibility(label: string | undefined): AccessibilityProps {
	if (label) return { accessible: true, accessibilityRole: "progressbar", accessibilityLabel: label, accessibilityState: { busy: true } };
	return { accessible: false, accessibilityElementsHidden: true, importantForAccessibility: "no-hide-descendants" };
}

/**
 * The system's activity indicator, in the theme's colour: UIActivityIndicatorView
 * on iOS and the Material circular indicator on Android. Use it for anything in
 * progress rather than turning a glyph: it is what each platform's users read as
 * working, and the OS decides how it behaves when motion is reduced.
 */
export function Spinner({ size = "small", color, accessibilityLabel, style, testID }: SpinnerProps) {
	const t = useTheme();
	const tint = color ?? t.textSecondary;
	const a11y = spinnerAccessibility(accessibilityLabel);
	if (typeof size === "number" && Platform.OS === "ios") {
		return (
			<View style={[{ width: size, height: size, alignItems: "center", justifyContent: "center" }, style]} testID={testID} {...a11y}>
				<ActivityIndicator size="small" color={tint} style={{ transform: [{ scale: size / IOS_SMALL_EDGE }] }} />
			</View>
		);
	}
	return <ActivityIndicator size={size} color={tint} style={style} testID={testID} {...a11y} />;
}
