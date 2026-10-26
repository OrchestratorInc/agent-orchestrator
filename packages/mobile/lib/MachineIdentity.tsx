import type { StyleProp, ViewStyle } from "react-native";
import { StyleSheet, Text, View } from "react-native";
import { AppSymbol } from "./AppSymbol";
import { machineSymbolNames, type MachineAppearance } from "./machineAppearance";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { space, type } from "./tokens";

/** A machine's glyph, drawn by the system. It is a picture only: the row or control around it carries the name. */
export function MachineAvatar({ appearance, size = 22, color }: { appearance: MachineAppearance; size?: number; color?: string; testID?: string }) {
	const t = useTheme();
	const glyphSize = Math.max(16, Math.round(size * 0.56));
	return (
		<View
			accessibilityElementsHidden
			importantForAccessibility="no-hide-descendants"
			style={[styles.avatar, { width: size, height: size }]}
		>
			<AppSymbol name={machineSymbolNames[appearance.symbol]} size={glyphSize} color={color ?? t.textSecondary} />
		</View>
	);
}

export function MachineIdentity({ name, appearance, style, testID = "machine-identity" }: { name: string; appearance: MachineAppearance; style?: StyleProp<ViewStyle>; testID?: string }) {
	const themed = useThemedStyles(makeStyles);
	return (
		<View testID={testID} accessibilityLabel={`Machine ${name}`} style={[themed.identity, style]}>
			<Text testID="machine-identity-name" numberOfLines={1} ellipsizeMode="tail" style={themed.name}>{name}</Text>
			<MachineAvatar testID="machine-identity-symbol" appearance={appearance} />
		</View>
	);
}

const styles = StyleSheet.create({
	avatar: { flexShrink: 0, alignItems: "center", justifyContent: "center" },
});

const makeStyles = (t: Theme) => StyleSheet.create({
	identity: { minWidth: 0, maxWidth: "48%", flexDirection: "row", alignItems: "center", justifyContent: "flex-end", gap: space.xs },
	name: { flexShrink: 1, color: t.textSecondary, fontFamily: "Geist_500Medium", fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight, fontWeight: "500" },
});
