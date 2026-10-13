import type { StyleProp, ViewStyle } from "react-native";
import { StyleSheet, Text, View } from "react-native";
import Svg, { Path, Rect } from "react-native-svg";
import { Feather } from "./icons";
import type { MachineAppearance, MachineSymbol } from "./machineAppearance";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { space, type } from "./tokens";

export function MachineAvatar({ appearance, size = 22 }: { appearance: MachineAppearance; size?: number; testID?: string }) {
	const t = useTheme();
	const glyphSize = Math.max(16, Math.round(size * 0.56));
	return (
		<View
			accessibilityElementsHidden
			importantForAccessibility="no-hide-descendants"
			style={[styles.avatar, { width: size, height: size }]}
		>
			<MachineGlyph symbol={appearance.symbol} size={glyphSize} color={t.textSecondary} />
		</View>
	);
}

const lucideSymbol: Partial<Record<MachineSymbol, "server" | "cloud" | "terminal" | "monitor" | "laptop">> = {
	server: "server",
	cloud: "cloud",
	linux: "terminal",
	desktop: "monitor",
	laptop: "laptop",
};

function MachineGlyph({ symbol, size, color }: { symbol: MachineSymbol; size: number; color: string }) {
	const lucide = lucideSymbol[symbol];
	if (lucide) return <Feather name={lucide} size={size} color={color} />;
	return (
		<Svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
			{symbol === "mac-mini" ? (
				<>
					<Rect width={20} height={8} x={2} y={8} rx={2} />
					<Path d="M6 12h.01" />
				</>
			) : (
				<>
					<Rect width={18} height={14} x={3} y={5} rx={2} />
					<Path d="M7 15h.01M11 15h.01M15 15h.01" />
				</>
			)}
		</Svg>
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
