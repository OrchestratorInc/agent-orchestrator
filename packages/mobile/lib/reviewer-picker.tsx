import { Feather } from "@expo/vector-icons";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { AgentLogo } from "./AgentLogo";
import { haptics } from "./haptics";
import { reviewerLabel, type ReviewerPickerProps } from "./reviewer-picker.types";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";

/** Android/web reviewer choice: the same options as the iOS menus, as a checked list. */
export function ReviewerPicker({ reviewers, selectedReviewer, effectiveReviewer, onSelectReviewer, models, modelTitle, selectedModel, onSelectModel, busy }: ReviewerPickerProps) {
	const styles = useThemedStyles(makeStyles);
	const defaultSubtitle = effectiveReviewer ? reviewerLabel(reviewers, effectiveReviewer) : undefined;
	return <View>
		<Option title="Project default" subtitle={defaultSubtitle} icon="users" selected={!selectedReviewer} disabled={busy} onPress={() => onSelectReviewer("")} />
		{reviewers.map((agent) => <Option key={agent.id} title={agent.label} harness={agent.id} selected={selectedReviewer === agent.id} disabled={busy} onPress={() => onSelectReviewer(agent.id)} />)}
		{models.length ? <>
			<Text style={styles.subheading}>{modelTitle.toUpperCase()}</Text>
			<Option title="Provider default" icon="sliders" selected={!selectedModel} disabled={busy} onPress={() => onSelectModel("")} />
			{models.map((model) => <Option key={model.id} title={model.label} icon="cpu" selected={selectedModel === model.id} disabled={busy} onPress={() => onSelectModel(model.id)} />)}
		</> : null}
	</View>;
}

function Option({ title, subtitle, icon, harness, selected, disabled, onPress }: { title: string; subtitle?: string; icon?: keyof typeof Feather.glyphMap; harness?: string; selected: boolean; disabled: boolean; onPress: () => void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return <Pressable accessibilityRole="button" accessibilityState={{ selected, disabled }} disabled={disabled} onPress={() => { haptics.select(); onPress(); }} style={({ pressed }) => [styles.row, pressed && styles.pressed, disabled && styles.disabled]}>
		{harness ? <AgentLogo harness={harness} size={22} /> : <Feather name={icon ?? "user"} size={17} color={selected ? t.accent : t.textTertiary} />}
		<View style={styles.copy}><Text style={styles.title}>{title}</Text>{subtitle ? <Text style={styles.subtitle}>{subtitle}</Text> : null}</View>
		{selected ? <Feather name="check" size={17} color={t.accent} /> : null}
	</Pressable>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	row: { minHeight: 52, flexDirection: "row", alignItems: "center", gap: 11, paddingVertical: 9, borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: t.borderSubtle },
	copy: { flex: 1, gap: 2 },
	title: { color: t.textPrimary, fontSize: 15, fontWeight: "600" },
	subtitle: { color: t.textTertiary, fontSize: 12 },
	subheading: { color: t.textTertiary, fontSize: 11, fontWeight: "700", letterSpacing: 0.7, marginTop: 14, marginBottom: 4 },
	pressed: { opacity: 0.6 },
	disabled: { opacity: 0.5 },
});
