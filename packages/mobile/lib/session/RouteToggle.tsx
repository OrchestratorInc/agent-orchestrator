import { Host } from "@expo/ui";
import { Image, Toggle } from "@expo/ui/swift-ui";
import { accessibilityLabel, background, buttonStyle, contentShape, frame, shapes, toggleStyle } from "@expo/ui/swift-ui/modifiers";
import { StyleSheet } from "react-native";
import { useTheme, useThemeState } from "../ThemeProvider";
import { iconSize, touchTarget } from "../tokens";
import { ROUTE_TOGGLE_LABEL, type RouteToggleProps } from "./RouteToggle.types";

export type { RouteToggleProps } from "./RouteToggle.types";

/**
 * The composer's terminal route as a SwiftUI toggle in button style, so
 * VoiceOver reads it as a toggle with its on/off value. It draws its own
 * states rather than the button style's tint: with the hue-less accent the
 * tinted "on" was barely apart from "off". On fills the circle, the way a
 * selected bar item does; off is the bare glyph. Android draws Material's
 * filled icon toggle button (./RouteToggle.android).
 */
export function RouteToggle({ on, onChange }: RouteToggleProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	return (
		<Host style={styles.slot} colorScheme={scheme}>
			<Toggle
				isOn={on}
				onIsOnChange={onChange}
				modifiers={[toggleStyle("button"), buttonStyle("plain"), accessibilityLabel(ROUTE_TOGGLE_LABEL)]}
			>
				<Image
					systemName="terminal"
					size={iconSize.lg}
					color={on ? t.onAccent : t.textSecondary}
					modifiers={[
						frame({ width: touchTarget, height: touchTarget }),
						...(on ? [background(t.accent, shapes.circle())] : []),
						// The whole circle takes the tap, not only the glyph's strokes.
						contentShape(shapes.circle()),
					]}
				/>
			</Toggle>
		</Host>
	);
}

const styles = StyleSheet.create({
	slot: { width: touchTarget, height: touchTarget },
});
