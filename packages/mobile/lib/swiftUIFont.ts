import { font } from "@expo/ui/swift-ui/modifiers";
import { type, type TypeName, type TypeToken } from "./tokens";

// SwiftUI's Font.custom finds a face by its PostScript name. expo-font loads
// DM Sans under the aliases in tokens' `font` (DMSans_400Regular, ...), which only
// React Native's text resolves: the alias lives in a UIFont family-name
// swizzle that Font.custom never consults, so an alias here would quietly draw
// SF Pro. These are the names inside the bundled files.
const appFaces: Record<TypeToken["fontWeight"], string> = {
	"400": "DMSans-Regular",
	"500": "DMSans-Medium",
	"600": "DMSans-SemiBold",
};
const geistMono = "GeistMono-Regular";

const swiftUITextStyles = {
	largeTitle: "largeTitle",
	title1: "title",
	title2: "title2",
	title3: "title3",
	headline: "headline",
	body: "body",
	callout: "callout",
	subheadline: "subheadline",
	footnote: "footnote",
	caption1: "caption",
	caption2: "caption2",
} as const satisfies Record<TypeName, string>;

/**
 * The one way to set SwiftUI text in the app's family: a `font` modifier for a step of the
 * app's type ramp. The size is the ramp's, scaled with Dynamic Type relative to
 * the matching system text style, so native text grows with the user's setting
 * like the rest. The weight defaults to the step's own; `mono` draws Geist Mono.
 * Never pass `family` to `font` directly.
 */
export function appFont(step: TypeName, options: { weight?: TypeToken["fontWeight"]; mono?: boolean } = {}) {
	const token = type[step];
	return font({
		family: options.mono ? geistMono : appFaces[options.weight ?? token.fontWeight],
		size: token.fontSize,
		textStyle: swiftUITextStyles[step],
	});
}
