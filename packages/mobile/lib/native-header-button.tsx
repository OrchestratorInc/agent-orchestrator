import { Pressable, StyleSheet } from "react-native";
import { Feather, type FeatherIconName } from "./icons";
import { useTheme } from "./ThemeProvider";
import { iconSize, radius } from "./tokens";

export type NativeHeaderButtonIcon = "menu" | "bell" | "close" | "check" | "back" | "more" | "add";

const icons: Record<NativeHeaderButtonIcon, FeatherIconName> = {
	menu: "menu",
	bell: "bell",
	close: "x",
	check: "check",
	back: "chevron-left",
	"more": "more-horizontal",
	add: "plus",
};

export function NativeHeaderButton({
	icon,
	label,
	onPress,
}: {
	icon: NativeHeaderButtonIcon;
	label: string;
	onPress: () => void;
}) {
	const t = useTheme();
	return (
		<Pressable
			testID={`header-${icon}`}
			accessibilityRole="button"
			accessibilityLabel={label}
			onPress={onPress}
			style={({ pressed }) => [
				styles.button,
				{ backgroundColor: pressed ? t.accentTint : t.bgElevatedHover, borderColor: t.borderStrong },
			]}
		>
			<Feather name={icons[icon]} size={iconSize.lg} color={t.textSecondary} />
		</Pressable>
	);
}

const styles = StyleSheet.create({
	button: {
		width: 44,
		height: 44,
		borderRadius: radius.pill, borderCurve: "continuous",
		borderWidth: StyleSheet.hairlineWidth,
		alignItems: "center",
		justifyContent: "center",
		overflow: "hidden",
	},
});
