import { StyleSheet, View } from "react-native";
import { useTheme } from "../ThemeProvider";

/**
 * The idle mic's container off iOS: Material's tonal fill.
 *
 * Android has no glass material, and an imitation of one (a translucent fill
 * with a "sheen" in the same colour) drew nothing anyone could see. A plain
 * tonal fill is what Material gives an icon button that needs a container. It
 * sits behind the mic's Pressable and never takes a touch — hold-to-talk and
 * double-tap stay on the Pressable.
 */
export function MicGlass({ size, radius }: { size: number; radius: number }) {
	const t = useTheme();
	return <View pointerEvents="none" style={[StyleSheet.absoluteFill, { width: size, height: size, borderRadius: radius, backgroundColor: t.accentTint }]} />;
}
