import { FieldGroup, Host, ListItem } from "@expo/ui";
import { Gauge, Image, RNHostView, Text } from "@expo/ui/swift-ui";
import { accessibilityAddTraits, accessibilityHidden, background, buttonStyle, disabled as disabledModifier, font, foregroundStyle, frame, gaugeStyle, listRowBackground, scaleEffect, scrollContentBackground, tint } from "@expo/ui/swift-ui/modifiers";
import { Image as RNImage, StyleSheet, View } from "react-native";
import PULL_REQUEST_GLYPH from "../../assets/icons/pull-request.png";
import { appFont } from "../swiftUIFont";
import { useTheme, useThemeState } from "../ThemeProvider";
import { iconSize, rowIconWidth, space } from "../tokens";
import type { ConversationMenuItem } from "./conversationMenuModel";
import type { ConversationActionsPageProps } from "./ConversationActionsPage.types";

export type { ConversationActionsPageProps } from "./ConversationActionsPage.types";

/**
 * The conversation's actions as a bottom sheet, on iOS a grouped SwiftUI Form
 * like Settings: each action a row with its glyph, the running ones saying so,
 * Compact with its context use as a ring, and Archive last and in red. A
 * pull-down menu had grown too long to read at a glance; a sheet keeps every
 * action one tap away with room for its hint.
 */
export function ConversationActionsPage({ groups, onSelect }: ConversationActionsPageProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	return (
		// The sheet has no title bar, so the first section keeps a gap under the grabber.
		<View style={[styles.fill, styles.page, { backgroundColor: t.bgBase }]}>
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
		</View>
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
			leading={item.glyph === "pullRequest" ? (
				// The app's pull-request glyph, as the board's header draws it; no SF
				// Symbol is one. Centred in the symbols' column.
				<RNHostView matchContents>
					<View style={styles.glyphColumn}>
						<RNImage source={PULL_REQUEST_GLYPH} style={[styles.glyph, { tintColor: item.destructive ? t.red : t.textSecondary }]} accessibilityElementsHidden />
					</View>
				</RNHostView>
			) : (
				<Image systemName={item.icon.ios} color={item.destructive ? t.red : t.textSecondary} modifiers={[font({ textStyle: "body" }), frame({ width: rowIconWidth }), accessibilityHidden(true)]} />
			)}
			supportingText={item.subtitle ? <Text modifiers={[appFont("footnote"), foregroundStyle(t.textSecondary)]}>{item.subtitle}</Text> : undefined}
			trailing={item.meter === undefined ? undefined : (
				// The system's capacity ring, the percentage inside it; the subtitle
				// says the same for VoiceOver.
				<Gauge value={item.meter} min={0} max={100} currentValueLabel={<Text>{`${item.meter}`}</Text>} modifiers={[gaugeStyle("circularCapacity"), tint(t.accent), scaleEffect(0.8), accessibilityHidden(true)]} />
			)}
		>
			<Text modifiers={[foregroundStyle(ink)]}>{item.label}</Text>
		</ListItem>
	);
}

const styles = StyleSheet.create({
	fill: { flex: 1 },
	page: { paddingTop: space.huge },
	glyphColumn: { width: rowIconWidth, alignItems: "center", justifyContent: "center" },
	glyph: { width: iconSize.lg, height: iconSize.lg },
});
