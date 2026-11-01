import { Host, Icon, LazyColumn } from "@expo/ui/jetpack-compose";
import { fillMaxSize, testID } from "@expo/ui/jetpack-compose/modifiers";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { GroupedRow, GroupedSection, groupedListPadding } from "../composeGroupedList.android";
import { useTheme, useThemeState } from "../ThemeProvider";
import { materialDrawable } from "../toolbarIcon.android";
import { iconSize, space } from "../tokens";
import type { ConversationActionsPageProps } from "./ConversationActionsPage.types";

export type { ConversationActionsPageProps } from "./ConversationActionsPage.types";

/**
 * The conversation's actions as a page, on Android a Material 3 grouped list
 * in the app's surfaces: each action a row led by its glyph, with its hint
 * under it, and Archive last in the error colour.
 */
export function ConversationActionsPage({ groups, onSelect }: ConversationActionsPageProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const insets = useSafeAreaInsets();
	return (
		<Host style={{ flex: 1, backgroundColor: t.bgBase }} colorScheme={scheme}>
			<LazyColumn
				contentPadding={{ ...groupedListPadding, bottom: groupedListPadding.bottom + insets.bottom }}
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
