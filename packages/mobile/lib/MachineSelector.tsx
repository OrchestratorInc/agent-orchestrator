import { useState } from "react";
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from "react-native";
import { haptics } from "./haptics";
import type { Host as PairedHost } from "./hosts";
import { Feather } from "./icons";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { space, type } from "./tokens";

export function MachineSelector({
	hosts,
	selectedHostId,
	selectedHostName,
	statusLabel,
	statusColor,
	switchingHostId,
	onSelect,
}: {
	hosts: PairedHost[];
	selectedHostId: string | null;
	selectedHostName?: string | null;
	statusLabel: string;
	statusColor?: string;
	switchingHostId: string | null;
	onSelect(id: string): void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [open, setOpen] = useState(false);
	const selectedHost = hosts.find((host) => host.id === selectedHostId);
	const machineName = selectedHost?.name ?? selectedHostName ?? "Choose a machine";
	const hasDefault = selectedHost !== undefined || selectedHostName != null;
	const busy = switchingHostId !== null;

	function toggle() {
		haptics.tap();
		setOpen((value) => !value);
	}

	function choose(id: string) {
		haptics.select();
		setOpen(false);
		if (id !== selectedHostId) onSelect(id);
	}

	return (
		<View>
			<Pressable
				testID="machine-selector-trigger"
				accessibilityRole="button"
				accessibilityLabel={`${machineName}${hasDefault ? ", default machine" : ""}. ${statusLabel}`}
				accessibilityState={{ expanded: open }}
				onPress={toggle}
				style={({ pressed }) => [styles.trigger, pressed && styles.pressed]}
			>
				<Feather name="monitor" size={17} color={t.textSecondary} style={styles.icon} />
				<View style={styles.nameGroup}>
					<Text testID="machine-selector-name" style={styles.name} numberOfLines={1}>{machineName}</Text>
					{hasDefault ? <Text testID="machine-selector-default" style={styles.defaultPill}>Default</Text> : null}
				</View>
				<View style={styles.statusGroup}>
					<Text style={[styles.status, statusColor ? { color: statusColor } : null]} numberOfLines={1}>{statusLabel}</Text>
					<Feather name={open ? "chevron-up" : "chevron-down"} size={17} color={t.textFaint} />
				</View>
			</Pressable>

			{open ? (
				<View testID="machine-selector-options" style={styles.options}>
					{hosts.map((host) => {
						const selected = host.id === selectedHostId;
						const loading = host.id === switchingHostId;
						return (
							<View key={host.id} style={styles.optionRow}>
								<Pressable
									testID={`machine-selector-option-${host.id}`}
									accessibilityRole="button"
									accessibilityLabel={selected ? `${host.name}, default machine` : `Use ${host.name}`}
									accessibilityState={{ selected, disabled: busy }}
									disabled={busy}
									onPress={() => choose(host.id)}
									style={({ pressed }) => [styles.optionSelect, pressed && styles.pressed, busy && styles.disabled]}
								>
									<Feather name="server" size={16} color={selected ? t.textPrimary : t.textTertiary} style={styles.optionIcon} />
									<Text style={[styles.optionName, selected && styles.optionNameSelected]} numberOfLines={1}>{host.name}</Text>
									{selected ? <Text style={styles.defaultPill}>Default</Text> : null}
									{loading ? <ActivityIndicator size="small" color={t.textTertiary} /> : null}
								</Pressable>
							</View>
						);
					})}
				</View>
			) : null}
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	trigger: { minHeight: 56, flexDirection: "row", alignItems: "center", paddingHorizontal: space.md, gap: space.sm },
	icon: { width: 26, textAlign: "center" },
	nameGroup: { flex: 1, minWidth: 0, flexDirection: "row", alignItems: "center", gap: space.xs },
	name: { flexShrink: 1, fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "600" },
	defaultPill: { overflow: "hidden", fontFamily: "Geist_600SemiBold", color: t.textSecondary, backgroundColor: t.bgSubtle, fontSize: 10, lineHeight: 18, fontWeight: "600", paddingHorizontal: 7, borderRadius: 9 },
	statusGroup: { flexDirection: "row", alignItems: "center", gap: space.xs, maxWidth: "38%" },
	status: { flexShrink: 1, fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	options: { borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: t.borderSubtle, backgroundColor: t.bgSubtle },
	optionRow: { minHeight: 48, flexDirection: "row", alignItems: "center", borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: t.borderSubtle },
	optionSelect: { minHeight: 48, flex: 1, minWidth: 0, flexDirection: "row", alignItems: "center", gap: space.sm, paddingLeft: 50, paddingRight: space.md },
	optionIcon: { width: 20, textAlign: "center" },
	optionName: { flex: 1, minWidth: 0, fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
	optionNameSelected: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontWeight: "600" },
	pressed: { backgroundColor: t.bgElevatedHover },
	disabled: { opacity: 0.45 },
});
