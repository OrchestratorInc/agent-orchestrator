import type { NativeStackNavigationOptions } from "expo-router";
import { useRouter } from "expo-router";
import { Pressable, type ColorValue } from "react-native";
import { backOr } from "./backNavigation";
import { CupertinoChevron } from "./cupertinoChevron";
import { usePressFeedback } from "./pressFeedback";
import { touchTarget } from "./tokens";

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

const target = { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" } as const;
