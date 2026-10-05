import type { ViewStyle } from "react-native";

/** Keeps source icons aligned without drawing a badge around them. */
export function environmentBadgeStyle(): ViewStyle {
	return {
		width: 26,
		height: 20,
		alignItems: "center",
		justifyContent: "center",
	};
}
