import { useEffect } from "react";
import { useNavigation, useRouter } from "expo-router";
import { Image, ScrollView, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { setOnboardingSkipped } from "../lib/onboardingStore";
import { completeOnboarding } from "../lib/onboardingNavigation";
import { useApp } from "../lib/store";
import { Button, MASCOT_ASPECT, NumberedStep } from "../lib/ui";
import MASCOT from "../assets/mascot.png";
import { rowDividerWidth } from "../lib/divider";
import { useThemedStyles } from "../lib/ThemeProvider";
import type { Theme } from "../lib/theme";
import { MOBILE_EVENTS } from "../lib/telemetry/events";
import { mobileTelemetry } from "../lib/telemetry/runtime";
import { fontScaleCap, space, type } from "../lib/tokens";

// Where a step's title starts: NumberedStep's badge at its floor (one line of
// footnote at the chrome cap, plus its padding) and the gap after it. The
// dividers between steps start there, under the titles rather than the badges.
const STEP_TEXT_INSET = Math.ceil(type.footnote.lineHeight * fontScaleCap.chrome) + 2 * space.hair + space.md;

export default function OnboardingScreen() {
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const navigation = useNavigation();
	const insets = useSafeAreaInsets();
	const { reloadConfig } = useApp();

	useEffect(() => {
		mobileTelemetry()?.capture(MOBILE_EVENTS.onboardingStarted);
	}, []);

	async function skip() {
		mobileTelemetry()?.capture(MOBILE_EVENTS.onboardingSkipped);
		await setOnboardingSkipped();
		await reloadConfig();
		completeOnboarding(navigation);
	}

	return (
		<View style={[styles.screen, { paddingTop: insets.top }]}>
			<View style={styles.topBar}>
				<View style={styles.brand}>
					<Image source={MASCOT} style={styles.mascot} resizeMode="contain" />
					<Text style={styles.brandName}>Orchestrator.inc</Text>
				</View>
				<Button variant="ghost" title="Skip" onPress={() => void skip()} style={styles.skip} />
			</View>

			<ScrollView
				style={styles.scroll}
				contentContainerStyle={styles.body}
				showsVerticalScrollIndicator={false}
			>
				<View style={styles.hero}>
					<Text accessibilityRole="header" style={styles.title}>Connect to Orchestrator</Text>
					<Text style={styles.lede}>
						Pair with Orchestrator on a computer or self-hosted machine to check on your agents, jump into any terminal, and drive work from your
						phone.
					</Text>
				</View>

				<View>
					<Text accessibilityRole="header" style={styles.howLabel}>How it works</Text>
					<NumberedStep
						n={1}
						total={3}
						title="Enable a connection on the machine"
						hint="Use Settings → Connect Mobile, or run ao remote-host enable on a headless machine."
					/>
					<View style={styles.divider} />
					<NumberedStep
						n={2}
						total={3}
						title="Scan the code"
						hint="Scan the QR code or enter the address and password manually."
					/>
					<View style={styles.divider} />
					<NumberedStep
						n={3}
						total={3}
						title="You're connected"
						hint="Your workers appear on the Workers tab, ready to drive from your phone."
					/>
				</View>
			</ScrollView>

			{/* Pinned above the home indicator, where the thumb already is, rather
			    than scrolled somewhere mid-page with the copy. */}
			<View style={[styles.footer, { paddingBottom: insets.bottom + space.lg }]}>
				<Button
					title="Pair a machine"
					icon="qrScanner"
					onPress={() => router.push("/pair?from=onboarding")}
					style={styles.cta}
				/>
			</View>
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	topBar: {
		flexDirection: "row",
		alignItems: "center",
		justifyContent: "space-between",
		paddingHorizontal: space.xl,
		paddingTop: space.xxs,
	},
	brand: { flexDirection: "row", alignItems: "center", gap: space.sm },
	mascot: { width: 26, height: 26 * MASCOT_ASPECT },
	brandName: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.body.fontSize, fontWeight: "600", letterSpacing: -0.2 },
	// A text button pads its label for the press state; pulled out by that
	// padding so "Skip" ends on the same edge as the page.
	skip: { marginRight: -space.md },

	scroll: { flex: 1 },
	body: { flexGrow: 1, paddingHorizontal: space.xxl, paddingBottom: space.lg },
	hero: { flexGrow: 1, alignItems: "center", justifyContent: "center", paddingVertical: space.xxxl },
	title: { fontFamily: "Geist_600SemiBold",
		color: t.textPrimary,
		fontSize: type.largeTitle.fontSize,
		fontWeight: "600",
		letterSpacing: -0.8,
		textAlign: "center",
	},
	lede: { fontFamily: "Geist_400Regular",
		color: t.textSecondary,
		fontSize: type.subheadline.fontSize,
		lineHeight: type.subheadline.lineHeight,
		textAlign: "center",
		marginTop: space.md,
		maxWidth: 330,
	},
	howLabel: { fontFamily: "Geist_600SemiBold",
		color: t.textSecondary,
		fontSize: type.footnote.fontSize,
		lineHeight: type.footnote.lineHeight,
		fontWeight: "600",
		marginBottom: space.xxs,
	},
	divider: { height: rowDividerWidth, backgroundColor: t.borderSubtle, marginLeft: STEP_TEXT_INSET },

	footer: { paddingHorizontal: space.xxl, paddingTop: space.md },
	cta: { alignSelf: "center", width: "100%", maxWidth: 400 },
});

export { RouteErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";
