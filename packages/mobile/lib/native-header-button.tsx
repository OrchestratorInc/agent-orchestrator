import { Button, Host } from "@expo/ui";
import { AppSymbol } from "./AppSymbol";
import type { AppSymbolName, PlatformSymbol } from "./symbols";
import { useThemeState } from "./ThemeProvider";
import { iconSize, radius, touchTarget } from "./tokens";

export type NativeHeaderButtonIcon = "menu" | "bell" | "close" | "check" | "back" | "more" | "add" | "trash";

const glyphs: Record<NativeHeaderButtonIcon, AppSymbolName | PlatformSymbol> = {
	// The drawer opener is the only header glyph the registry does not carry.
	menu: { ios: "line.3.horizontal", android: "menu" },
	bell: "notifications",
	close: "close",
	check: "checkmark",
	back: "back",
	more: "moreHorizontal",
	add: "add",
	trash: "delete",
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
	const { scheme } = useThemeState();
	return (
		<Host style={{ width: touchTarget, height: touchTarget }} colorScheme={scheme}>
			<Button
				onPress={onPress}
				variant="outlined"
				testID={`header-${icon}`}
				style={{ width: touchTarget, height: touchTarget, borderRadius: radius.pill }}
			>
				{/* The universal Button takes no accessibility label, so the glyph,
				    its only content, carries the control's name. */}
				<AppSymbol name={glyphs[icon]} size={iconSize.lg} accessibilityLabel={label} />
			</Button>
		</Host>
	);
}
