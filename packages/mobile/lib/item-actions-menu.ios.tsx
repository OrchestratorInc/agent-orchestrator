import { Host } from "@expo/ui";
import { Button, Image, Menu } from "@expo/ui/swift-ui";
import { accessibilityLabel as a11yLabel, buttonStyle, contentShape, disabled as disabledModifier, frame, shapes, tint } from "@expo/ui/swift-ui/modifiers";
import { StyleSheet, View } from "react-native";
import { itemActionIcons, type ItemActionsMenuProps } from "./item-actions-menu.types";
import { Spinner } from "./Spinner";
import { symbols } from "./symbols";
import { useTheme, useThemeState } from "./ThemeProvider";
import { iconSize, touchTarget } from "./tokens";

/** A per-item "⋯" native pull-down, like desktop's per-comment and per-review menus. */
export function ItemActionsMenu({ actions, accessibilityLabel, disabled = false, loading = false }: ItemActionsMenuProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	if (loading) return <View style={styles.slot}><Spinner /></View>;
	if (!actions.length) return null;
	return (
		<Host style={styles.slot} colorScheme={scheme}>
			<Menu
				// The label is the hit area: sized and shaped, or only the glyph (16×5pt) takes a tap.
				label={<Image systemName={symbols.moreHorizontal.ios} size={iconSize.md} modifiers={[frame({ width: touchTarget, height: touchTarget }), contentShape(shapes.rectangle())]} />}
				modifiers={[buttonStyle("plain"), tint(t.textSecondary), frame({ width: touchTarget, height: touchTarget }), disabledModifier(disabled), a11yLabel(accessibilityLabel)]}
			>
				{actions.map((action) => (
					<Button
						key={action.id}
						label={action.label}
						systemImage={action.icon ? itemActionIcons[action.icon].ios : undefined}
						role={action.destructive ? "destructive" : undefined}
						onPress={action.onPress}
					/>
				))}
			</Menu>
		</Host>
	);
}

const styles = StyleSheet.create({
	slot: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
});
