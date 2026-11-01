import { Box, CircularProgressIndicator, Host, Icon, LazyColumn, Text } from "@expo/ui/jetpack-compose";
import { fillMaxSize, size, testID } from "@expo/ui/jetpack-compose/modifiers";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { GroupedRow, GroupedSection, groupedListPadding } from "../composeGroupedList.android";
import { useTheme, useThemeState } from "../ThemeProvider";
import { materialDrawable } from "../toolbarIcon.android";
import { font, iconSize, space } from "../tokens";
import type { ConversationActionsPageProps } from "./ConversationActionsPage.types";

export type { ConversationActionsPageProps } from "./ConversationActionsPage.types";

/**
 * The conversation's actions as a bottom sheet, on Android a Material 3
 * grouped list in the sheet's surface: each action a row led by its glyph, with
 * its hint under it, Compact with its context use as a ring, and Archive last
 * in the error colour.
 */
export function ConversationActionsPage({ groups, onSelect }: ConversationActionsPageProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const insets = useSafeAreaInsets();
	return (
		<Host style={{ flex: 1, backgroundColor: t.bgSurface }} colorScheme={scheme}>
			<LazyColumn
				contentPadding={{ ...groupedListPadding, top: space.lg, bottom: groupedListPadding.bottom + insets.bottom }}
				verticalArrangement={{ spacedBy: space.lg }}
				modifiers={[fillMaxSize(), testID("conversation-actions")]}
			>
				{groups.map((group) => (
					<GroupedSection key={group.title} title={group.title}>
						{group.items.map((item) => (
							<GroupedRow
								key={item.action}
								headline={item.label}
								supporting={item.subtitle}
								leading={<Icon source={materialDrawable(item.icon.android)} size={iconSize.xl} tint={item.destructive ? t.red : t.textSecondary} />}
								trailing={item.meter === undefined ? undefined : <ContextRing percent={item.meter} />}
								onPress={() => onSelect(item.action)}
								disabled={item.disabled}
								destructive={item.destructive}
								testID={`conversation-action-${item.action}`}
							/>
						))}
					</GroupedSection>
				))}
			</LazyColumn>
		</Host>
	);
}

const RING = 36;

/** The context in use as Material's ring, the percentage in its middle; the row's subtitle says it for TalkBack. */
function ContextRing({ percent }: { percent: number }) {
	const t = useTheme();
	return (
		<Box contentAlignment="center" modifiers={[size(RING, RING)]}>
			<CircularProgressIndicator progress={percent / 100} color={t.accent} trackColor={t.bgElevatedHover} strokeWidth={3} modifiers={[size(RING, RING)]} />
			<Text color={t.textSecondary} style={{ typography: "labelSmall", fontFamily: font.medium }}>{`${percent}`}</Text>
		</Box>
	);
}
