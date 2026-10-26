import { Host } from "@expo/ui";
import { Button, HStack, Label, ProgressView, Text } from "@expo/ui/swift-ui";
import { accessibilityValue, buttonBorderShape, buttonStyle, controlSize, disabled as disabledModifier, foregroundStyle, tint } from "@expo/ui/swift-ui/modifiers";
import type { ReviewActionButtonProps } from "./review-action-button.types";
import { geistFont } from "./swiftUIFont";
import { useTheme, useThemeState } from "./ThemeProvider";
import { space } from "./tokens";

/**
 * A row's action as a small capsule system button: prominent for the live
 * action, bordered for the rest, red with the destructive role for one that
 * stops something. The system draws the pressed and disabled states.
 */
export function ReviewActionButton({ label, icon, tone, busy = false, disabled = false, onPress }: ReviewActionButtonProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	// The accent is near white on the dark theme, where the prominent style's
	// white label would vanish; the accent's own ink reads on it in both.
	const ink = tone === "filled" ? [foregroundStyle(t.onAccent)] : [];
	return (
		<Host matchContents colorScheme={scheme}>
			<Button
				role={tone === "destructive" ? "destructive" : undefined}
				onPress={onPress}
				modifiers={[
					buttonStyle(tone === "filled" ? "borderedProminent" : "bordered"),
					buttonBorderShape("capsule"),
					controlSize("small"),
					tint(tone === "destructive" ? t.red : t.accent),
					geistFont("footnote", { weight: "600" }),
					disabledModifier(disabled || busy),
					...(busy ? [accessibilityValue("In progress")] : []),
				]}
			>
				{busy
					? <HStack spacing={space.xs} modifiers={ink}><ProgressView modifiers={[controlSize("mini"), tint(tone === "filled" ? t.onAccent : t.textSecondary)]} /><Text>{label}</Text></HStack>
					: <Label title={label} systemImage={icon.ios} modifiers={ink} />}
			</Button>
		</Host>
	);
}
