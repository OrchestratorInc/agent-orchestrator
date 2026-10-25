import { Host } from "@expo/ui";
import { Circle, Group, Image, ZStack } from "@expo/ui/swift-ui";
import {
	accessibilityAddTraits,
	accessibilityElement,
	accessibilityIdentifier,
	accessibilityLabel,
	foregroundStyle,
	frame,
	offset,
	onTapGesture,
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
// So the material is ours now, drawn on a Group we size, with the gesture on the
// same group. Geometry comes from this file, not from the control's content, and
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
			<Group
				modifiers={[
					frame({ width: GLASS_CIRCLE_SIZE, height: GLASS_CIRCLE_SIZE }),
					glassCircle(),
					onTapGesture(() => onPress()),
					// With a badge the glyph and dot are two views; one element keeps
					// VoiceOver on a single button.
					...(badge > 0 ? [accessibilityElement("ignore")] : []),
					// The gesture is on the group rather than a Button, so nothing adds the
					// button trait by itself: VoiceOver read these as plain text and never
					// said "button". The trait is what a real Button would have contributed.
					accessibilityAddTraits(["isButton"]),
					accessibilityLabel(label),
					accessibilityIdentifier(`header-${icon}`),
				]}
			>
				{badge > 0 ? (
					// The dot sits on the glyph's shoulder, inside the circle, where the
					// system puts a badge on a bar button.
					<ZStack alignment="topTrailing">
						{glyph}
						<Circle modifiers={[frame({ width: BADGE_SIZE, height: BADGE_SIZE }), foregroundStyle(t.red), offset({ x: 2, y: -2 })]} />
					</ZStack>
				) : glyph}
			</Group>
		</Host>
	);
}
