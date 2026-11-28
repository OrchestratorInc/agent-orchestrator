import { Image, StyleSheet, Text, View } from "react-native";
import { harnessInitial } from "./harnessLogo";
import { logoFor } from "./harnessLogoAssets";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles, useThemeState } from "./ThemeProvider";

/**
 * A harness's brand mark, drawn bare in its own colours, as the desktop draws it.
 *
 * A brand whose mark only reads on one background ships a light/dark pair, and
 * the app's colour scheme picks between them, so no mark needs a chip behind it.
 */
export function AgentLogo({ harness, size = 24 }: { harness?: string | null; size?: number }) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const source = logoFor(harness, scheme);

	return (
		<View style={[styles.box, { width: size, height: size }]}>
			{source ? (
				<Image
					source={source}
					// `contain`, matching desktop's object-contain, so a wide mark such as
					// amp's wordmark keeps its shape.
					resizeMode="contain"
					style={{ width: size, height: size }}
					accessibilityIgnoresInvertColors
				/>
			) : (
				<View style={[styles.fallback, { width: size, height: size, borderRadius: Math.round(size * 0.28), borderCurve: "continuous"}]}>
					<Text style={[styles.initial, { fontFamily: "DMSans_400Regular", fontSize: Math.round(size * 0.5), color: t.textSecondary }]}>
						{harnessInitial(harness)}
					</Text>
				</View>
			)}
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		box: { alignItems: "center", justifyContent: "center" },
		fallback: {
			alignItems: "center",
			justifyContent: "center",
			backgroundColor: t.bgSubtle,
			borderWidth: StyleSheet.hairlineWidth,
			borderColor: t.borderDefault,
		},
		initial: { fontFamily: "DMSans_600SemiBold", fontWeight: "600" },
	});
