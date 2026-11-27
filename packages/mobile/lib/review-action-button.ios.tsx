import { Host } from "@expo/ui";
import { Button, HStack, Label, ProgressView, Text } from "@expo/ui/swift-ui";
import { accessibilityLabel as accessibilityLabelModifier, accessibilityValue, buttonBorderShape, buttonStyle, contentShape, controlSize, disabled as disabledModifier, foregroundStyle, frame, onTapGesture, shapes, tint } from "@expo/ui/swift-ui/modifiers";
import type { ReviewActionButtonProps } from "./review-action-button.types";
import { appFont } from "./swiftUIFont";
import { useTheme, useThemeState } from "./ThemeProvider";
import { space, touchTarget } from "./tokens";

/**
 * A row's action as a small capsule system button: prominent for the live
 * action, bordered for the rest, red with the destructive role for one that
 * stops something. The system draws the pressed and disabled states.
 */
export function ReviewActionButton({ label, accessibilityLabel, icon, tone, busy = false, disabled = false, onPress }: ReviewActionButtonProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const enabled = !disabled && !busy;
	// The accent is near white on the dark theme, where the prominent style's
	// white label would vanish; the accent's own ink reads on it in both.
	const ink = tone === "filled" ? [foregroundStyle(t.onAccent)] : [];
	// The Host's height is the touch target's and only its width follows the
	// label. Sized to its content both ways, it measured some buttons 52pt tall
	// with the capsule at the top and others 44pt with it centred, so two buttons
	// in one row could sit 17pt apart.
	return (
		<Host matchContents={HOST_SIZING} colorScheme={scheme} style={HOST_STYLE}>
			<Button
				role={tone === "destructive" ? "destructive" : undefined}
				onPress={onPress}
				modifiers={[
					buttonStyle(tone === "filled" ? "borderedProminent" : "bordered"),
					buttonBorderShape("capsule"),
					controlSize("small"),
					tint(tone === "destructive" ? t.red : t.accent),
					appFont("footnote", { weight: "600" }),
					disabledModifier(!enabled),
					...(accessibilityLabel ? [accessibilityLabelModifier(accessibilityLabel)] : []),
					...(busy ? [accessibilityValue("In progress")] : []),
					// The small capsule is about 28pt tall, and a Button takes taps only on
					// its own shape. The frame grows the Host to a full touch target around
					// it and a tap in that band runs the same action; on the capsule the
					// Button's own gesture wins over this outer one.
					frame({ minHeight: touchTarget }),
					contentShape(shapes.rectangle()),
					...(enabled ? [onTapGesture(onPress)] : []),
				]}
			>
				{busy
					? <HStack spacing={space.xs} modifiers={ink}><ProgressView modifiers={[controlSize("mini"), tint(tone === "filled" ? t.onAccent : t.textSecondary)]} /><Text>{label}</Text></HStack>
					: <Label title={label} systemImage={icon.ios} modifiers={ink} />}
			</Button>
		</Host>
	);
}

const HOST_SIZING = { horizontal: true } as const;
const HOST_STYLE = { height: touchTarget } as const;
