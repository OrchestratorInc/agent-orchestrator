import { Host } from "@expo/ui";
import { Circle, Image, ZStack } from "@expo/ui/swift-ui";
import {
	accessibilityAddTraits,
	accessibilityElement,
	accessibilityIdentifier,
	accessibilityLabel,
	contentShape,
	foregroundStyle,
	frame,
	offset,
	onTapGesture,
	shapes,
} from "@expo/ui/swift-ui/modifiers";
import { glassCircle } from "./glass";
import { nativeHeaderGlyphs, type NativeHeaderButtonProps } from "./native-header-button.types";
import { useTheme, useThemeState } from "./ThemeProvider";
import { iconSize, touchTarget } from "./tokens";

export type { NativeHeaderButtonIcon, NativeHeaderButtonProps } from "./native-header-button.types";

// Every glass circle is this size. Not a preference: the four corner controls
// were three different sizes, and the reason was that a SwiftUI glass *button*
// reports an intrinsic size — it grew with its glyph, and `frame` on the button
// did not constrain the material behind it.
//
// So the material is ours now, drawn on a ZStack we size, with the gesture on the
// same stack. Geometry comes from this file, not from the control's content, and
// the size is the touch target: the whole circle is what takes the tap.
export const GLASS_CIRCLE_SIZE = touchTarget;
const GLYPH_SIZE = iconSize.lg;
const BADGE_SIZE = 8;

export function NativeHeaderButton({ icon, label, onPress, badge = 0 }: NativeHeaderButtonProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const glyph = (
		<Image
			systemName={nativeHeaderGlyphs[icon].ios}
			size={GLYPH_SIZE}
			color={t.textSecondary}
			modifiers={[frame({ width: GLYPH_SIZE, height: GLYPH_SIZE })]}
		/>
	);

	return (
		<Host style={{ width: GLASS_CIRCLE_SIZE, height: GLASS_CIRCLE_SIZE }} colorScheme={scheme}>
			<ZStack
				modifiers={[
					frame({ width: GLASS_CIRCLE_SIZE, height: GLASS_CIRCLE_SIZE }),
					glassCircle(),
					contentShape(shapes.circle(), ["interaction", "accessibility"]),
					onTapGesture(() => onPress()),
					// One element for the whole circle. A ZStack, not a Group: a Group
					// is no view of its own and hands its modifiers to its child, so
					// VoiceOver's element was the bare glyph (9×17pt for Back) even
					// with this. With a badge the glyph and dot would be two.
					accessibilityElement("ignore"),
					// The gesture is on the group rather than a Button, so nothing adds the
					// button trait by itself: VoiceOver read these as plain text and never
					// said "button". The trait is what a real Button would have contributed.
					accessibilityAddTraits(["isButton"]),
					accessibilityLabel(label),
					accessibilityIdentifier(`header-${icon}`),
				]}
			>
				{/* Clear, full size: VoiceOver sizes the element from its content, not
				    from `frame`, so without this its box was the bare glyph (9.7×17pt
				    for Back) though the whole circle takes the tap. */}
				<Circle modifiers={[frame({ width: GLASS_CIRCLE_SIZE, height: GLASS_CIRCLE_SIZE }), foregroundStyle("transparent")]} />
				{badge > 0 ? (
					// The dot sits on the glyph's shoulder, inside the circle, where the
					// system puts a badge on a bar button.
					<ZStack alignment="topTrailing">
						{glyph}
						<Circle modifiers={[frame({ width: BADGE_SIZE, height: BADGE_SIZE }), foregroundStyle(t.red), offset({ x: 2, y: -2 })]} />
					</ZStack>
				) : glyph}
			</ZStack>
		</Host>
	);
}
