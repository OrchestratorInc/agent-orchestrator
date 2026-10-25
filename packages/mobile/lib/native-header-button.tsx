import { Button, Host } from "@expo/ui";
import { AppSymbol } from "./AppSymbol";
import { nativeHeaderGlyphs, type NativeHeaderButtonProps } from "./native-header-button.types";
import { useThemeState } from "./ThemeProvider";
import { iconSize, radius, touchTarget } from "./tokens";

export type { NativeHeaderButtonIcon, NativeHeaderButtonProps } from "./native-header-button.types";

// The fallback for platforms without a variant of their own. It draws no unread
// dot; the caller's label already names the count.
export function NativeHeaderButton({ icon, label, onPress }: NativeHeaderButtonProps) {
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
				<AppSymbol name={nativeHeaderGlyphs[icon]} size={iconSize.lg} accessibilityLabel={label} />
			</Button>
		</Host>
	);
}
