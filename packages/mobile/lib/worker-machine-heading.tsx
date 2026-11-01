import { StyleSheet, Text, View } from "react-native";
import type { MachineAppearance } from "./machineAppearance";
import { MachineAvatar } from "./MachineIdentity";
import type { Theme } from "./theme";
import { useThemedStyles } from "./ThemeProvider";
import { fontScaleCap, gutter, space, type } from "./tokens";

/**
 * The one machine a board's rows are all on, named once at the head of the
 * list: the machine it is filtered to, or the only one paired. The rows then
 * leave their own machine label off, which said the same name on every row.
 */
export function WorkerMachineHeading({ name, appearance }: { name: string; appearance: MachineAppearance }) {
	const styles = useThemedStyles(makeStyles);
	return (
		<View testID="worker-machine-heading" accessible accessibilityRole="header" accessibilityLabel={`Machine ${name}`} style={styles.heading}>
			<MachineAvatar appearance={appearance} size={space.xl} />
			<Text maxFontSizeMultiplier={fontScaleCap.chrome} numberOfLines={1} style={styles.name}>{name}</Text>
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	heading: { flexDirection: "row", alignItems: "center", gap: space.xs, paddingHorizontal: gutter, paddingTop: space.sm },
	name: { flexShrink: 1, fontFamily: "DMSans_500Medium", color: t.textSecondary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "500" },
});
