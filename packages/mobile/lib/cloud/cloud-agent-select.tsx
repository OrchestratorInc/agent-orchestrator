import { Pressable, StyleSheet, Text, View } from "react-native";
import { AgentLogo } from "../AgentLogo";
import { Feather } from "../icons";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import type { Theme } from "../theme";
import { fontScaleCap, iconSize, press, space, touchTarget, type } from "../tokens";

export type CloudAgentSelectProps = {
	label: string;
	value: string;
	options: readonly { value: string; label: string }[];
	onChange(value: string): void;
	/** Android replaces the form with the same choice list used by task creation. */
	onOpen(): void;
	enabled: boolean;
	separator?: boolean;
};

/** Android/web trigger; iOS uses the task composer's native branded menu. */
export function CloudAgentSelect({ label, value, options, onOpen, enabled, separator = false }: CloudAgentSelectProps) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const selected = options.find((option) => option.value === value);
	const selectedLabel = selected?.label ?? "Choose agent";
	const canSelect = enabled && options.length > 0;
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={`${label}: ${selectedLabel}`}
			accessibilityState={{ disabled: !canSelect }}
			testID={label}
			disabled={!canSelect}
			onPress={() => { if (canSelect) onOpen(); }}
			android_ripple={{ color: t.accentTint }}
			style={[styles.row, separator && styles.separator, !canSelect && styles.disabled]}
		>
			<Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.label}>{label.replace(/ agent$/i, "")}</Text>
			<View style={styles.valueGroup}>
				{selected ? <AgentLogo harness={selected.value} size={iconSize.lg} /> : null}
				<Text numberOfLines={1} ellipsizeMode="tail" maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.value}>{selectedLabel}</Text>
				<Feather name="chevron-down" size={iconSize.sm} color={t.textTertiary} />
			</View>
		</Pressable>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	row: { width: "100%", minHeight: touchTarget, paddingHorizontal: space.lg, paddingVertical: space.sm, flexDirection: "row", alignItems: "center", gap: space.md },
	label: { ...type.callout, color: t.textPrimary, flexShrink: 0 },
	valueGroup: { flex: 1, minWidth: 0, flexDirection: "row", alignItems: "center", justifyContent: "flex-end", gap: space.sm },
	value: { ...type.callout, color: t.textSecondary, flexShrink: 1, minWidth: 0 },
	separator: { borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: t.borderSubtle },
	disabled: { opacity: press.opacity },
});
