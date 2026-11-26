import { Badge, BadgedBox, Box, Column, Host, Icon, Row, Text } from "@expo/ui/jetpack-compose";
import {
	background,
	clickable,
	clip,
	defaultMinSize,
	dropShadow,
	height,
	padding,
	paddingAll,
	selectable,
	selectableGroup,
	semantics,
	Shapes,
	size,
	testID,
	width,
} from "@expo/ui/jetpack-compose/modifiers";
import { StyleSheet, useWindowDimensions, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { BOARD_BAR_BOTTOM, BOARD_BAR_HEIGHT, BOARD_BAR_MARGIN } from "./board-bar-layout";
import { boardTabs, type BoardTabName } from "./board-tabs";
import type { BoardTabBarProps } from "./BoardTabBar.types";
import { symbols } from "./symbols";
import { useTheme, useThemeState } from "./ThemeProvider";
import { materialDrawable } from "./toolbarIcon.android";
import { font, fontScaleCap, space } from "./tokens";

export type { BoardTabBarProps } from "./BoardTabBar.types";

/** What the badge prints; past this it stops growing. */
const MAX_BADGE = 99;
const PILL_PADDING = space.xxs;
const ITEM_HEIGHT = BOARD_BAR_HEIGHT - PILL_PADDING * 2;
/** Material 3's active indicator, a little fuller than its 32dp: a pill behind the icon alone, not the whole tab. */
const INDICATOR_WIDTH = 64;
const INDICATOR_HEIGHT = 36;
/** Each tab: the pill and a little room, so the two sit close together. */
const ITEM_WIDTH = INDICATOR_WIDTH + space.sm;
/** The New task pill: slimmer than the bar, as an extended button set beside tabs. */
const NEW_TASK_HEIGHT = 44;
/** Material 3's gap between the indicator and the label. */
const LABEL_GAP = 4;
// Room inside each host for the shadow, which a host sized to the shape alone
// would cut off at its edges.
const SHADOW_ROOM = space.sm;

const ICONS: Record<BoardTabName, Parameters<typeof materialDrawable>[0]> = {
	"(workers)": symbols.workers.android,
	"(projects)": symbols.projects.android,
};

/**
 * The board's bar on Android, laid out as iOS lays out its own: Workers and
 * Projects at the start, laid out as Material 3's navigation bar items (an
 * indicator pill behind the selected icon, the label under it), and New task as a round
 * button of its own at the end. Pull Requests and Settings open from the header, as on iOS.
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
			style={[styles.bar, { bottom: insets.bottom + BOARD_BAR_BOTTOM - SHADOW_ROOM }]}
		>
			<Host matchContents colorScheme={scheme}>
				<Row
					verticalAlignment="center"
					// No surface of its own: the tabs are their icons and labels,
					// over the bar's frost, as Material's navigation bar draws them.
					modifiers={[
						paddingAll(SHADOW_ROOM),
						paddingAll(PILL_PADDING),
						selectableGroup(),
					]}
				>
					{boardTabs.map((tab) => {
						const active = tab.name === selected;
						const tint = active ? t.accent : t.textSecondary;
						// The selected icon sits on the inverted pill, in the pill's own ink.
						const icon = <Icon source={materialDrawable(ICONS[tab.name])} size={24} tint={active ? t.onAccent : t.textSecondary} />;
						const badge = tab.name === "(workers)" && waiting > 0 ? (waiting > MAX_BADGE ? `${MAX_BADGE}+` : String(waiting)) : null;
						return (
							<Column
								key={tab.name}
								horizontalAlignment="center"
								verticalArrangement="center"
								modifiers={[
									// At 1.0x the indicator, gap and label fill ITEM_HEIGHT
									// exactly (36 + 4 + 16); the label's line grows with the
									// font scale up to the chrome cap, so the tab grows with
									// it rather than clipping the label's descenders.
									width(ITEM_WIDTH),
									defaultMinSize({ minHeight: ITEM_HEIGHT }),
									// No clip: the badge stands past the tab's top edge.
									selectable(active, () => onSelect(tab.name), "tab"),
									// Takes the touch before `selectable` (Compose hands it to the
									// innermost handler first), so no ripple draws; `selectable`
									// still names the tab and its state for TalkBack, and its own
									// click serves TalkBack's double-tap.
									clickable(() => onSelect(tab.name), { indication: false }),
									...(labelled ? [] : [semantics({ contentDescription: tab.title })]),
									testID(`board-tab-${tab.title.toLowerCase()}`),
								]}
							>
								{/* Material 3's layout in the app's own colours: the selected
								    tab's indicator is a solid surface, not a translucent tint. */}
								{/* The pill is a layer of its own under the icon, so its clip
								    shapes the pill alone and the badge can stand past its edge. */}
								<Box contentAlignment="center" modifiers={[size(INDICATOR_WIDTH, INDICATOR_HEIGHT)]}>
									<Box
										modifiers={[
											size(INDICATOR_WIDTH, INDICATOR_HEIGHT),
											clip(Shapes.RoundedCorner(INDICATOR_HEIGHT / 2)),
											// Inverted: the app's solid ink, under an icon in its paper.
											...(active ? [background(t.accent)] : []),
											testID(`board-tab-indicator-${tab.title.toLowerCase()}`),
										]}
									/>
									{badge ? (
										<BadgedBox>
											{icon}
											<BadgedBox.Badge>
												{/* Inverted too on the selected tab, or it would vanish into the pill. */}
												<Badge containerColor={active ? t.onAccent : t.accent} contentColor={active ? t.accent : t.onAccent}>
													<Text>{badge}</Text>
												</Badge>
											</BadgedBox.Badge>
										</BadgedBox>
									) : icon}
								</Box>
								{labelled ? <Text color={tint} maxLines={1} style={{ fontFamily: font.medium, fontSize: 12, lineHeight: 16 }} modifiers={[padding(0, LABEL_GAP, 0, 0)]}>{tab.title}</Text> : null}
							</Column>
						);
					})}
				</Row>
			</Host>
			<Host matchContents colorScheme={scheme}>
				{/* Android's extended floating action button, slimmed to a pill and
				    inverted to match the selected tab: the app's solid ink with
				    "+ New" in its paper. */}
				<Row
					verticalAlignment="center"
					horizontalArrangement={{ spacedBy: space.xs }}
					modifiers={[
						paddingAll(SHADOW_ROOM),
						dropShadow(Shapes.RoundedCorner(NEW_TASK_HEIGHT / 2), shadow),
						height(NEW_TASK_HEIGHT),
						clip(Shapes.RoundedCorner(NEW_TASK_HEIGHT / 2)),
						background(t.accent),
						clickable(onNewTask, { indication: false }),
						semantics({ contentDescription: "New task" }),
						padding(space.md, 0, space.lg, 0),
						testID("spawn-worker"),
					]}
				>
					<Icon source={materialDrawable("add")} size={20} tint={t.onAccent} />
					{/* Past the chrome cap it keeps its + alone, as the tabs keep their icons. */}
					{labelled ? <Text color={t.onAccent} maxLines={1} style={{ fontFamily: font.semibold, fontSize: 15, lineHeight: 20 }}>New</Text> : null}
				</Row>
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
		// The slim New pill sits level with the tabs' middle.
		alignItems: "center",
	},
});
