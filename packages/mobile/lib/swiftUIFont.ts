import { font } from "@expo/ui/swift-ui/modifiers";
import { type, type TypeName, type TypeToken } from "./tokens";

// SwiftUI's Font.custom finds a face by its PostScript name, not by the alias
// expo-font registers for React Native, so the bundled Geist files are named
// here by what is inside them.
const geistFaces: Record<TypeToken["fontWeight"], string> = {
	"400": "Geist-Regular",
	"500": "Geist-Medium",
	"600": "Geist-SemiBold",
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
 * A SwiftUI `font` modifier for a step of the app's type ramp, in Geist. The
 * size is the ramp's, scaled with Dynamic Type relative to the matching system
 * text style, so native text grows with the user's setting like the rest.
 */
export function geistFont(step: TypeName, options: { weight?: TypeToken["fontWeight"]; mono?: boolean } = {}) {
	const token = type[step];
	return font({
		family: options.mono ? geistMono : geistFaces[options.weight ?? token.fontWeight],
		size: token.fontSize,
		textStyle: swiftUITextStyles[step],
	});
}
