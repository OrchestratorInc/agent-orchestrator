import { MenuView, type MenuAction, type MenuComponentRef, type NativeActionEvent } from "@expo/ui/community/menu";
import { useCallback, useEffect, useRef } from "react";
import { Platform, StyleSheet, Text, View, type AccessibilityActionEvent } from "react-native";
import { AgentLogo } from "../AgentLogo";
import { haptics } from "../haptics";
import { Feather } from "../icons";
import { useTheme, useThemeState, useThemedStyles } from "../ThemeProvider";
import type { Theme } from "../theme";
import { fontScaleCap, iconSize, press, space, touchTarget, type } from "../tokens";

export type CloudAgentSelectProps = {
	label: string;
	value: string;
	options: readonly { value: string; label: string }[];
	onChange(value: string): void;
	enabled: boolean;
	separator?: boolean;
};

/** A compact agent row for a shared group of Cloud project defaults. */
export function CloudAgentSelect({ label, value, options, onChange, enabled, separator = false }: CloudAgentSelectProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const selected = options.find((option) => option.value === value);
	const selectedLabel = selected?.label ?? "Choose agent";
	const canSelect = enabled && options.length > 0;
	const menuRef = useRef<MenuComponentRef>(null);
	const current = useRef({ value, options, onChange, enabled });
	current.current = { value, options, onChange, enabled };
	const mounted = useRef(true);
	useEffect(() => {
		mounted.current = true;
		return () => { mounted.current = false; };
	}, []);

	// A native menu can deliver a queued action after the row's availability or
	// connected options change. Check the current props, including when the old
	// native trigger has already been removed.
	const choose = useCallback(({ nativeEvent: { event } }: NativeActionEvent) => {
		const latest = current.current;
		if (!mounted.current || !latest.enabled || event === latest.value || !latest.options.some((option) => option.value === event)) return;
		haptics.select();
		latest.onChange(event);
	}, []);
	const activate = useCallback(({ nativeEvent: { actionName } }: AccessibilityActionEvent) => {
		const latest = current.current;
		if (actionName !== "activate" || !mounted.current || !latest.enabled || !latest.options.length) return;
		menuRef.current?.show();
	}, []);

	// MenuView owns the tap. An inner Pressable would claim Android's responder
	// before the native menu's opening control, even without an onPress callback.
	const trigger = (
		<View
			accessible
			accessibilityRole="button"
			accessibilityLabel={`${label}: ${selectedLabel}`}
			accessibilityState={{ disabled: !canSelect }}
			accessibilityActions={Platform.OS === "android" && canSelect ? [{ name: "activate" }] : undefined}
			onAccessibilityAction={Platform.OS === "android" && canSelect ? activate : undefined}
			testID={label}
			style={[styles.row, separator && styles.separator, !canSelect && styles.disabled]}
		>
			<Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.label}>{label.replace(/ agent$/i, "")}</Text>
			<View style={styles.valueGroup}>
				{selected ? <AgentLogo harness={selected.value} size={iconSize.lg} /> : null}
				<Text numberOfLines={1} ellipsizeMode="tail" maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.value}>{selectedLabel}</Text>
				<Feather name="chevron-down" size={iconSize.sm} color={t.textTertiary} />
			</View>
		</View>
	);
	if (!canSelect) return trigger;

	const actions: MenuAction[] = options.map((option) => ({
		id: option.value,
		title: option.label,
		state: selected?.value === option.value ? "on" : "off",
	}));
	return <MenuView ref={menuRef} actions={actions} title={label} colorScheme={scheme} onPressAction={choose} style={styles.menu}>
		{trigger}
	</MenuView>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	menu: { width: "100%" },
	row: { width: "100%", minHeight: touchTarget, paddingHorizontal: space.lg, paddingVertical: space.sm, flexDirection: "row", alignItems: "center", gap: space.md },
	label: { ...type.callout, color: t.textPrimary, flexShrink: 0 },
	valueGroup: { flex: 1, minWidth: 0, flexDirection: "row", alignItems: "center", justifyContent: "flex-end", gap: space.sm },
	value: { ...type.callout, color: t.textSecondary, flexShrink: 1, minWidth: 0 },
	separator: { borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: t.borderSubtle },
	disabled: { opacity: press.opacity },
});
