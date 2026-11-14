import { Badge, BadgedBox, Box, Column, Host, Icon, Row, Text } from "@expo/ui/jetpack-compose";
import {
	background,
	clickable,
	clip,
	dropShadow,
	paddingAll,
	selectable,
	selectableGroup,
	semantics,
	Shapes,
	size,
	testID,
} from "@expo/ui/jetpack-compose/modifiers";
import { StyleSheet, useWindowDimensions, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { BOARD_BAR_HEIGHT, BOARD_BAR_MARGIN } from "./board-bar-layout";
import { boardTabs, type BoardTabName } from "./board-tabs";
import type { BoardTabBarProps } from "./BoardTabBar.types";
import { symbols } from "./symbols";
import { useTheme, useThemeState } from "./ThemeProvider";
import { materialDrawable } from "./toolbarIcon.android";
import { font, fontScaleCap, iconSize, space } from "./tokens";

export type { BoardTabBarProps } from "./BoardTabBar.types";

/** What the badge prints; past this it stops growing. */
const MAX_BADGE = 99;
const PILL_PADDING = space.xxs;
const ITEM_HEIGHT = BOARD_BAR_HEIGHT - PILL_PADDING * 2;
const ITEM_WIDTH = 84;
// Room inside each host for the shadow, which a host sized to the shape alone
// would cut off at its edges.
const SHADOW_ROOM = space.sm;

const ICONS: Record<BoardTabName, Parameters<typeof materialDrawable>[0]> = {
	"(workers)": symbols.workers.android,
	"(projects)": symbols.projects.android,
};

/**
 * The board's bar on Android, laid out as iOS lays out its own: Workers and
 * Projects in a pill at the start, and New task as a round button of its own
 * at the end. Pull Requests and Settings open from the header, as on iOS.
 *
 * Material's navigation bar shares the full width out between its items, so it
 * cannot hold two tabs at one end and a button at the other. NativeTabs keeps
 * every tab mounted with its bar hidden, and this draws the bar over the page.
 * Each piece has a host of its own so the gap between them still scrolls the
 * list under it.
 */
export function BoardTabBar({ selected, onSelect, waiting, onNewTask }: BoardTabBarProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const insets = useSafeAreaInsets();
	// Past the chrome cap the labels no longer fit their tabs, so the tabs keep
	// their icons and TalkBack still reads each one's name.
	const labelled = useWindowDimensions().fontScale <= fontScaleCap.chrome;
	const shadow = { radius: 12, offsetY: 2, alpha: scheme === "dark" ? 0.4 : 0.12 };
	return (
		<View
			pointerEvents="box-none"
			style={[styles.bar, { bottom: insets.bottom + BOARD_BAR_MARGIN - SHADOW_ROOM }]}
		>
			<Host matchContents colorScheme={scheme}>
				<Row
					verticalAlignment="center"
					modifiers={[
						paddingAll(SHADOW_ROOM),
						dropShadow(Shapes.RoundedCorner(BOARD_BAR_HEIGHT / 2), shadow),
						clip(Shapes.RoundedCorner(BOARD_BAR_HEIGHT / 2)),
						background(t.bgElevated),
						paddingAll(PILL_PADDING),
						selectableGroup(),
					]}
				>
					{boardTabs.map((tab) => {
						const active = tab.name === selected;
						const tint = active ? t.accent : t.textSecondary;
						const icon = <Icon source={materialDrawable(ICONS[tab.name])} size={iconSize.xl} tint={tint} />;
						const badge = tab.name === "(workers)" && waiting > 0 ? (waiting > MAX_BADGE ? `${MAX_BADGE}+` : String(waiting)) : null;
						return (
							<Column
								key={tab.name}
								horizontalAlignment="center"
								verticalArrangement="center"
								modifiers={[
									size(ITEM_WIDTH, ITEM_HEIGHT),
									clip(Shapes.RoundedCorner(ITEM_HEIGHT / 2)),
									...(active ? [background(t.accentTint)] : []),
									selectable(active, () => onSelect(tab.name), "tab"),
									...(labelled ? [] : [semantics({ contentDescription: tab.title })]),
									testID(`board-tab-${tab.title.toLowerCase()}`),
								]}
							>
								{badge ? (
									<BadgedBox>
										{icon}
										<BadgedBox.Badge>
											<Badge containerColor={t.accent} contentColor={t.bgBase}>
												<Text>{badge}</Text>
											</Badge>
										</BadgedBox.Badge>
									</BadgedBox>
								) : icon}
								{labelled ? <Text color={tint} maxLines={1} style={{ fontFamily: font.medium, fontSize: 12 }}>{tab.title}</Text> : null}
							</Column>
						);
					})}
				</Row>
			</Host>
			<Host matchContents colorScheme={scheme}>
				<Box
					contentAlignment="center"
					modifiers={[
						paddingAll(SHADOW_ROOM),
						dropShadow(Shapes.Circle, shadow),
						size(BOARD_BAR_HEIGHT, BOARD_BAR_HEIGHT),
						clip(Shapes.Circle),
						background(t.bgElevated),
						clickable(onNewTask),
						semantics({ contentDescription: "New task" }),
						testID("spawn-worker"),
					]}
				>
					<Icon source={materialDrawable("add")} size={iconSize.xl} tint={t.textPrimary} />
				</Box>
			</Host>
		</View>
	);
}

const styles = StyleSheet.create({
	bar: {
		position: "absolute",
		start: BOARD_BAR_MARGIN - SHADOW_ROOM,
		end: BOARD_BAR_MARGIN - SHADOW_ROOM,
		flexDirection: "row",
		justifyContent: "space-between",
		alignItems: "flex-end",
	},
});
