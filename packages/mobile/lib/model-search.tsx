import Star from "lucide-react-native/icons/star";
import { useEffect, useMemo, useState } from "react";
import { Modal, Pressable, SectionList, StyleSheet, Text, TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { AgentLogo } from "./AgentLogo";
import { AppSymbol } from "./AppSymbol";
import type { WheelAgent } from "./chat/useChatModelWheel";
import { haptics } from "./haptics";
import type { WheelItem } from "./model-list";
import { textSelectionColors } from "./textSelection";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles, useThemeState } from "./ThemeProvider";
import { font, iconSize, radius, space, type } from "./tokens";

/*
 * The model picker's search: a page of its own over the chat, its field
 * focused as it opens, so the keys stay up and typing starts at once. Every
 * agent's models, filtered as typed and grouped under their agent; a tap, or
 * Go for the first match, picks one and closes it.
 */

/** One agent's models that match, under its name. */
export type ModelSearchSection = { agent: WheelAgent; locked: boolean; data: WheelItem[] };

/**
 * The models matching `query`, under their agents in the picker's order: every
 * word typed must appear in the model's name or id, or its agent's. While the
 * agent works (`locked`), the other agents' models show but cannot be picked.
 */
export function matchModels(agents: readonly WheelAgent[], query: string, chosenAgent: string, locked: boolean): ModelSearchSection[] {
	const words = query.toLowerCase().split(/\s+/).filter(Boolean);
	return agents
		.map((agent) => ({
			agent,
			locked: locked && agent.id !== chosenAgent,
			data: agent.models.filter((model) => {
				const text = `${model.label} ${model.key} ${agent.label} ${agent.id}`.toLowerCase();
				return words.every((word) => text.includes(word));
			}),
		}))
		.filter((section) => section.data.length > 0);
}

/** The first model a search can pick: Go picks it. */
export function firstPickable(sections: readonly ModelSearchSection[]): { agent: string; model: string } | undefined {
	const section = sections.find((item) => !item.locked);
	const model = section?.data[0];
	return section && model ? { agent: section.agent.id, model: model.key } : undefined;
}

const ROW = 48;
const STAR = 13;
const MARK = 18;
const FIELD = 40;

export function ModelSearchSheet({ visible, agents, chosenAgent, chosenModel, agentsLocked, onPick, onClose, onDismissed }: {
	visible: boolean;
	agents: readonly WheelAgent[];
	chosenAgent: string;
	chosenModel?: string;
	agentsLocked: boolean;
	onPick: (agentId: string, modelKey: string) => void;
	onClose: () => void;
	/** The page has finished leaving (iOS); the composer can take the keys back. */
	onDismissed?: () => void;
}) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const insets = useSafeAreaInsets();
	const [query, setQuery] = useState("");
	// Each search starts empty.
	useEffect(() => { if (visible) setQuery(""); }, [visible]);
	const sections = useMemo(() => matchModels(agents, query, chosenAgent, agentsLocked), [agents, query, chosenAgent, agentsLocked]);
	const pick = (agent: string, model: string) => { haptics.select(); onPick(agent, model); };

	return (
		<Modal visible={visible} animationType="slide" presentationStyle="fullScreen" onRequestClose={onClose} onDismiss={onDismissed} statusBarTranslucent>
			<View style={[styles.page, { paddingTop: insets.top + space.sm }]} testID="model-search">
				<View style={styles.bar}>
					<View style={styles.field}>
						<AppSymbol name="search" size={iconSize.sm} color={t.textTertiary} />
						<TextInput
							autoFocus
							onChangeText={setQuery}
							placeholder="Search models"
							placeholderTextColor={t.textTertiary}
							{...textSelectionColors(t)}
							keyboardAppearance={scheme}
							autoCapitalize="none"
							autoCorrect={false}
							spellCheck={false}
							clearButtonMode="while-editing"
							returnKeyType="go"
							// Go picks the first match; the keys stay up for another try when nothing matches.
							submitBehavior="submit"
							onSubmitEditing={() => {
								const first = firstPickable(sections);
								if (first) pick(first.agent, first.model);
							}}
							accessibilityLabel="Search models"
							testID="model-search-field"
							style={styles.input}
						/>
					</View>
					<Pressable onPress={onClose} hitSlop={space.sm} accessibilityRole="button" testID="model-search-cancel">
						<Text style={styles.cancel}>Cancel</Text>
					</Pressable>
				</View>
				<SectionList
					sections={sections}
					keyExtractor={(item, index) => `${item.key}:${index}`}
					// The keys stay up through a scroll and a tap: the list is read with them up.
					keyboardShouldPersistTaps="always"
					keyboardDismissMode="none"
					automaticallyAdjustKeyboardInsets
					stickySectionHeadersEnabled={false}
					contentContainerStyle={[styles.content, { paddingBottom: insets.bottom + space.lg }]}
					renderSectionHeader={({ section }) => (
						<View style={styles.header} accessibilityRole="header">
							<AgentLogo harness={section.agent.id} size={MARK} />
							<Text style={styles.headerLabel} numberOfLines={1}>{section.agent.label}</Text>
							{section.locked ? <Text style={styles.headerNote} numberOfLines={1}>Switch when idle</Text> : null}
						</View>
					)}
					renderItem={({ item, section }) => {
						const on = section.agent.id === chosenAgent && item.key === chosenModel;
						return (
							<Pressable
								disabled={section.locked}
								onPress={() => pick(section.agent.id, item.key)}
								accessibilityRole="button"
								accessibilityState={{ selected: on, disabled: section.locked }}
								accessibilityLabel={`${section.agent.label}, ${item.label}${item.starred ? ", default" : ""}`}
								testID={`model-search-${section.agent.id}-${item.key}`}
								style={({ pressed }) => [styles.row, on && styles.rowOn, pressed && !on && styles.rowPressed, section.locked && styles.rowLocked]}
							>
								<Text style={[styles.label, on && styles.labelOn]} numberOfLines={1}>{item.label}</Text>
								{item.starred ? <Star size={STAR} color={t.textTertiary} fill={t.textTertiary} strokeWidth={1.5} /> : null}
								<View style={styles.spacer} />
								{on ? <AppSymbol name="checkmark" size={iconSize.sm} color={t.textPrimary} weight="semibold" /> : null}
							</Pressable>
						);
					}}
					ListEmptyComponent={<Text style={styles.empty}>No models match “{query.trim()}”</Text>}
				/>
			</View>
		</Modal>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		page: { flex: 1, backgroundColor: t.bgBase },
		bar: { flexDirection: "row", alignItems: "center", gap: space.md, paddingHorizontal: space.lg, paddingBottom: space.sm },
		field: {
			flex: 1,
			flexDirection: "row",
			alignItems: "center",
			gap: space.xs,
			height: FIELD,
			paddingHorizontal: space.md,
			borderRadius: radius.md,
			borderCurve: "continuous",
			backgroundColor: t.bgElevated,
		},
		input: { flex: 1, height: FIELD, padding: 0, fontFamily: font.regular, fontSize: 17, color: t.textPrimary },
		cancel: { ...type.body, color: t.textPrimary },
		content: { paddingHorizontal: space.sm },
		header: { flexDirection: "row", alignItems: "center", gap: space.xs, paddingHorizontal: space.md, paddingTop: space.lg, paddingBottom: space.xs },
		headerLabel: { ...type.footnote, fontFamily: font.medium, color: t.textSecondary, flexShrink: 1 },
		headerNote: { ...type.footnote, color: t.textTertiary },
		row: {
			flexDirection: "row",
			alignItems: "center",
			gap: space.xs,
			minHeight: ROW,
			paddingHorizontal: space.md,
			borderRadius: radius.lg,
			borderCurve: "continuous",
		},
		rowOn: { backgroundColor: t.bgElevated },
		rowPressed: { backgroundColor: t.bgSubtle },
		rowLocked: { opacity: 0.4 },
		label: { fontFamily: font.regular, fontSize: 17, lineHeight: 22, color: t.textSecondary, flexShrink: 1 },
		labelOn: { color: t.textPrimary },
		spacer: { flex: 1 },
		empty: { ...type.callout, color: t.textTertiary, textAlign: "center", paddingTop: space.xxxl },
	});
