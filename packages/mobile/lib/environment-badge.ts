import type { TextStyle } from "react-native";
import type { Theme } from "./theme";
import { type } from "./tokens";

/** Desktop-style outline for the source label shared by projects and workers. */
export function environmentBadgeStyle(t: Theme): TextStyle {
	return {
		color: t.textPrimary,
		borderColor: t.borderStrong,
		borderWidth: 1,
		borderRadius: 999,
		minHeight: 16,
		paddingHorizontal: 6,
		fontFamily: type.caption2.fontFamily,
		fontSize: type.caption2.fontSize,
		lineHeight: type.caption2.lineHeight,
		fontWeight: type.caption2.fontWeight,
		textAlignVertical: "center",
		includeFontPadding: false,
		overflow: "hidden",
	};
}
