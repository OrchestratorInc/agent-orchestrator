import { CircularProgressIndicator, FilledTonalButton, Host, Icon, OutlinedButton, Spacer, Text } from "@expo/ui/jetpack-compose";
import { semantics, size, width } from "@expo/ui/jetpack-compose/modifiers";
import type { ReviewActionButtonProps } from "./review-action-button.types";
import { useTheme, useThemeState } from "./ThemeProvider";
import { materialDrawable } from "./toolbarIcon.android";
import { font, space } from "./tokens";

// Material's 18dp icon inside a button, and its padding for a button that leads with one.
const BUTTON_ICON = 18;
const ICON_PADDING = { start: space.lg, end: space.xxl, top: space.sm, bottom: space.sm };

/**
 * A row's action as a Material 3 button: tonal for the live action, outlined
 * for the rest, outlined in red for one that stops something. Compose draws
 * the ripple, the 48dp touch target and the disabled state.
 */
export function ReviewActionButton({ label, accessibilityLabel, icon, tone, busy = false, disabled = false, onPress }: ReviewActionButtonProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const ink = tone === "filled" ? t.accent : tone === "destructive" ? t.red : t.textPrimary;
	const MaterialButton = tone === "filled" ? FilledTonalButton : OutlinedButton;
	return (
		<Host matchContents colorScheme={scheme}>
			<MaterialButton
				enabled={!disabled && !busy}
				onClick={onPress}
				colors={tone === "filled" ? { containerColor: t.accentTint, contentColor: ink } : { contentColor: ink }}
				contentPadding={ICON_PADDING}
				modifiers={accessibilityLabel ? [semantics({ contentDescription: accessibilityLabel })] : undefined}
			>
				{busy
					? <CircularProgressIndicator color={ink} strokeWidth={2} modifiers={[size(BUTTON_ICON, BUTTON_ICON)]} />
					: <Icon source={materialDrawable(icon.android)} size={BUTTON_ICON} />}
				<Spacer modifiers={[width(space.sm)]} />
				{/* A style replaces the button's own text style, so it names the label step. */}
				<Text style={{ typography: "labelLarge", fontFamily: font.semibold }}>{label}</Text>
			</MaterialButton>
		</Host>
	);
}
