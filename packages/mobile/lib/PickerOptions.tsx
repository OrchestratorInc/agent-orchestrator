import { Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { AgentLogo } from "./AgentLogo";
import { Feather } from "./icons";
import { useTheme } from "./ThemeProvider";
import type { Theme } from "./theme";
import type { SpawnComposerOption } from "./spawn-composer-controls.types";
import { type, space } from "./tokens";

/** Task and project pickers share one in-sheet choice list on Android. */
export function PickerOptions({ title, options, selectedValue, showAgentLogos, onSelect, onBack }: {
	title: string;
	options: readonly SpawnComposerOption[];
	selectedValue: string;
	showAgentLogos?: boolean;
	onSelect: (value: string) => void;
	onBack: () => void;
}) {
	const t = useTheme();
	const styles = makeStyles(t);
	return (
		<View style={styles.stack}>
			<Pressable
				accessibilityRole="button"
				accessibilityLabel="Back"
				android_ripple={{ color: t.accentTint }}
				onPress={onBack}
				style={styles.optionHeader}
			>
				<Feather name="chevron-left" size={20} color={t.textSecondary} />
				<Text style={styles.optionTitle}>{title}</Text>
			</Pressable>
			<ScrollView style={styles.optionList} nestedScrollEnabled showsVerticalScrollIndicator={false}>
				{options.map((option, index) => {
					const selected = option.id === selectedValue;
					return (
						<Pressable
							key={option.id}
							accessibilityRole="button"
							accessibilityState={{ selected }}
							android_ripple={{ color: t.accentTint }}
							onPress={() => onSelect(option.id)}
							style={[styles.optionRow, index > 0 && !option.sectionBreakBefore && styles.optionBorder, option.sectionBreakBefore && styles.optionSectionBreak, selected && styles.optionSelected]}
						>
							{showAgentLogos ? <AgentLogo harness={option.id} size={24} /> : option.icon ? <Feather name={option.icon} size={20} color={t.textSecondary} /> : null}
							<Text numberOfLines={2} style={[styles.optionLabel, selected && styles.optionLabelSelected]}>{option.label}</Text>
							{selected ? <Feather name="check" size={20} color={t.accent} /> : null}
						</Pressable>
					);
				})}
			</ScrollView>
		</View>
	);
}
const makeStyles = (t: Theme) => StyleSheet.create({
	stack: { gap: space.sm },
	optionHeader: { minHeight: 44, flexDirection: "row", alignItems: "center", gap: space.xs, borderRadius: 12, overflow: "hidden" },
	optionTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.body.fontSize, lineHeight: type.body.lineHeight, fontWeight: "600" },
	optionList: { maxHeight: 340, borderRadius: 16, backgroundColor: t.bgElevated, overflow: "hidden" },
	optionRow: { minHeight: 54, paddingHorizontal: space.lg, paddingVertical: space.md, flexDirection: "row", alignItems: "center", gap: space.md },
	optionBorder: { borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: t.borderSubtle },
	optionSectionBreak: { marginTop: space.xs, borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: t.borderDefault },
	optionSelected: { backgroundColor: t.accentTint },
	optionLabel: { fontFamily: "Geist_400Regular", flex: 1, color: t.textPrimary, fontSize: type.callout.fontSize, lineHeight: type.callout.lineHeight },
	optionLabelSelected: { fontFamily: "Geist_600SemiBold", color: t.accent, fontWeight: "600" },
});
