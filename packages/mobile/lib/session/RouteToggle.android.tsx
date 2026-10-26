import { FilledIconToggleButton, Host, Icon } from "@expo/ui/jetpack-compose";
import { useTheme, useThemeState } from "../ThemeProvider";
import { materialDrawable } from "../toolbarIcon.android";
import { iconSize } from "../tokens";
import { ROUTE_TOGGLE_LABEL, type RouteToggleProps } from "./RouteToggle.types";

export type { RouteToggleProps } from "./RouteToggle.types";

/**
 * Material 3's filled icon toggle button: a tonal container while the composer
 * messages the agent, the accent fill while it writes to the terminal. Compose
 * gives it the 48dp target, the ripple and TalkBack's on/off state.
 */
export function RouteToggle({ on, onChange }: RouteToggleProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	return (
		<Host matchContents colorScheme={scheme}>
			<FilledIconToggleButton
				checked={on}
				onCheckedChange={onChange}
				// The accent is not one of the Material scheme's roles, so the pair is named here.
				colors={{ containerColor: t.accentTint, contentColor: t.textSecondary, checkedContainerColor: t.accent, checkedContentColor: t.onAccent }}
			>
				{/* No tint: the glyph takes the button's content colour for each state. */}
				<Icon source={materialDrawable("terminal")} size={iconSize.xl} contentDescription={ROUTE_TOGGLE_LABEL} />
			</FilledIconToggleButton>
		</Host>
	);
}
