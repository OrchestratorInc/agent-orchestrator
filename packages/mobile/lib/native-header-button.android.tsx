import { Badge, BadgedBox, Host, Icon, IconButton } from "@expo/ui/jetpack-compose";
import { testID } from "@expo/ui/jetpack-compose/modifiers";
import { nativeHeaderGlyphs, type NativeHeaderButtonProps } from "./native-header-button.types";
import { useTheme, useThemeState } from "./ThemeProvider";
import { materialDrawable } from "./toolbarIcon.android";
import { iconSize } from "./tokens";

export type { NativeHeaderButtonIcon, NativeHeaderButtonProps } from "./native-header-button.types";

/**
 * Material 3's icon button: a 48dp target with the system's ripple around a
 * 24dp Material Symbol, and M3's badge for an unread dot. Compose owns the
 * touch, so a press lands even while the header is moving with the page.
 */
export function NativeHeaderButton({ icon, label, onPress, badge = 0 }: NativeHeaderButtonProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const glyph = (
		<Icon
			source={materialDrawable(nativeHeaderGlyphs[icon].android)}
			size={iconSize.xl}
			tint={t.textSecondary}
			contentDescription={label}
		/>
	);
	return (
		<Host matchContents colorScheme={scheme}>
			<IconButton onClick={onPress} modifiers={[testID(`header-${icon}`)]}>
				{badge > 0 ? (
					<BadgedBox>
						{glyph}
						<BadgedBox.Badge>
							<Badge containerColor={t.red} />
						</BadgedBox.Badge>
					</BadgedBox>
				) : glyph}
			</IconButton>
		</Host>
	);
}
