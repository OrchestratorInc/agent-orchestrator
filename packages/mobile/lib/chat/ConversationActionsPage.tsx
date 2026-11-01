import { FieldGroup, Host, ListItem } from "@expo/ui";
import { Image, Text } from "@expo/ui/swift-ui";
import { accessibilityAddTraits, accessibilityHidden, background, buttonStyle, disabled as disabledModifier, font, foregroundStyle, listRowBackground, scrollContentBackground } from "@expo/ui/swift-ui/modifiers";
import { StyleSheet } from "react-native";
import { appFont } from "../swiftUIFont";
import { useTheme, useThemeState } from "../ThemeProvider";
import type { ConversationMenuItem } from "./conversationMenuModel";
import type { ConversationActionsPageProps } from "./ConversationActionsPage.types";

export type { ConversationActionsPageProps } from "./ConversationActionsPage.types";

/**
 * The conversation's actions as a page, on iOS a grouped SwiftUI Form like
 * Settings: each action a row with its glyph, the running ones saying so, and
 * Archive last and in red. A pull-down menu had grown too long to read at a
 * glance; a page keeps every action one tap away with room for its hint.
 */
export function ConversationActionsPage({ groups, onSelect }: ConversationActionsPageProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	return (
		<Host style={[styles.fill, { backgroundColor: t.bgBase }]} colorScheme={scheme}>
			<FieldGroup testID="conversation-actions" modifiers={[scrollContentBackground("hidden"), background(t.bgBase), appFont("body"), foregroundStyle(t.textPrimary)]}>
				{groups.map((group) => (
					<FieldGroup.Section key={group.title}>
						<FieldGroup.SectionHeader>
							<Text modifiers={[appFont("footnote"), foregroundStyle(t.textSecondary), accessibilityAddTraits(["isHeader"])]}>{group.title}</Text>
						</FieldGroup.SectionHeader>
						{group.items.map((item) => <ActionRow key={item.action} item={item} onSelect={onSelect} />)}
					</FieldGroup.Section>
				))}
			</FieldGroup>
		</Host>
	);
}

function ActionRow({ item, onSelect }: { item: ConversationMenuItem; onSelect: ConversationActionsPageProps["onSelect"] }) {
	const t = useTheme();
	const ink = item.destructive ? t.red : t.textPrimary;
	return (
		<ListItem
			onPress={() => onSelect(item.action)}
			testID={`conversation-action-${item.action}`}
			// The list's own button style, for the row highlight a plain button lacks.
			modifiers={[buttonStyle("automatic"), listRowBackground(t.bgElevated), ...(item.disabled ? [disabledModifier(true)] : [])]}
			leading={<Image systemName={item.icon.ios} color={item.destructive ? t.red : t.textSecondary} modifiers={[font({ textStyle: "body" }), accessibilityHidden(true)]} />}
			supportingText={item.subtitle ? <Text modifiers={[appFont("footnote"), foregroundStyle(t.textSecondary)]}>{item.subtitle}</Text> : undefined}
		>
			<Text modifiers={[foregroundStyle(ink)]}>{item.label}</Text>
		</ListItem>
	);
}

const styles = StyleSheet.create({
	fill: { flex: 1 },
});
