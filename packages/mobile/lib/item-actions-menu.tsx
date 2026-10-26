import { DropdownMenu, DropdownMenuItem, Host, Icon, IconButton, Text } from "@expo/ui/jetpack-compose";
import { testID } from "@expo/ui/jetpack-compose/modifiers";
import { useState } from "react";
import { StyleSheet, View } from "react-native";
import { itemActionIcons, type ItemActionsMenuProps } from "./item-actions-menu.types";
import { reviewDrawable } from "./review-drawables";
import { Spinner } from "./Spinner";
import { useTheme, useThemeState } from "./ThemeProvider";
import { iconSize, touchTarget } from "./tokens";

/**
 * Android's per-item "⋯": Material 3's icon button anchoring a dropdown menu,
 * the same Compose `DropdownMenu` that MenuView draws. It is built here rather
 * than through MenuView because MenuView wraps its trigger in a React Native
 * Pressable, which would leave the overflow without the icon button's 48dp
 * target and ripple. Every action is listed, and Back or a tap outside closes it.
 */
export function ItemActionsMenu({ actions, accessibilityLabel, disabled = false, loading = false }: ItemActionsMenuProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const [expanded, setExpanded] = useState(false);
	if (loading) return <View style={styles.slot}><Spinner size={iconSize.lg} /></View>;
	if (!actions.length) return null;
	return (
		<Host matchContents colorScheme={scheme}>
			<DropdownMenu expanded={expanded && !disabled} onDismissRequest={() => setExpanded(false)} color={t.bgElevated}>
				<DropdownMenu.Trigger>
					<IconButton enabled={!disabled} onClick={() => setExpanded(true)} modifiers={[testID("item-actions-menu")]}>
						<Icon source={reviewDrawable("more_horiz")} size={iconSize.xl} tint={t.textSecondary} contentDescription={accessibilityLabel} />
					</IconButton>
				</DropdownMenu.Trigger>
				<DropdownMenu.Items>
					{actions.map((action) => (
						<DropdownMenuItem
							key={action.id}
							elementColors={action.destructive
								? { textColor: t.red, leadingIconColor: t.red }
								: { textColor: t.textPrimary, leadingIconColor: t.textSecondary }}
							onClick={() => {
								setExpanded(false);
								action.onPress();
							}}
						>
							<DropdownMenuItem.Text><Text>{action.label}</Text></DropdownMenuItem.Text>
							{action.icon ? (
								<DropdownMenuItem.LeadingIcon>
									<Icon source={reviewDrawable(itemActionIcons[action.icon].android)} size={iconSize.xl} />
								</DropdownMenuItem.LeadingIcon>
							) : null}
						</DropdownMenuItem>
					))}
				</DropdownMenu.Items>
			</DropdownMenu>
		</Host>
	);
}

const styles = StyleSheet.create({
	slot: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
});
