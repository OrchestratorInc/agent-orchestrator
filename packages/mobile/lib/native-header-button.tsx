import { Button, Host } from "@expo/ui";
import { useThemeState } from "./ThemeProvider";
import { radius } from "./tokens";

export type NativeHeaderButtonIcon = "menu" | "bell" | "close" | "check" | "back" | "more" | "add" | "trash";

export function NativeHeaderButton({
	icon,
	label,
	onPress,
}: {
	icon: NativeHeaderButtonIcon;
	label: string;
	onPress: () => void;
}) {
	const { scheme } = useThemeState();
	return (
		<Host style={{ width: 44, height: 44 }} colorScheme={scheme}>
			<Button
				label={icon === "menu" ? "☰" : icon === "close" ? "×" : icon === "check" ? "✓" : icon === "back" ? "‹" : icon === "add" ? "+" : icon === "trash" ? "🗑" : "◉"}
				onPress={onPress}
				variant="outlined"
				testID={`header-${icon}`}
				style={{ width: 44, height: 44, borderRadius: radius.pill}}
			/>
		</Host>
	);
}
