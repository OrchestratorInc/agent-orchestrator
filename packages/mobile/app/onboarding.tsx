import { useEffect, useState } from "react";
import { useNavigation, useRouter } from "expo-router";
import { Alert, Image, StyleSheet, Text, View } from "react-native";
import Animated, { Easing, useAnimatedStyle, useSharedValue, withRepeat, withTiming, type SharedValue } from "react-native-reanimated";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { loadAccount, signInToAccount } from "../lib/account";
import { syncAccountHosts } from "../lib/accountHosts";
import { AppSymbol } from "../lib/AppSymbol";
import { loadHosts } from "../lib/hosts";
import { completeOnboarding } from "../lib/onboardingNavigation";
import { useApp } from "../lib/store";
import { useCloud } from "../lib/cloud/CloudProvider";
import { Button, MASCOT_ASPECT } from "../lib/ui";
import MASCOT from "../assets/mascot.png";
import { useTheme, useThemedStyles } from "../lib/ThemeProvider";
import type { Theme } from "../lib/theme";
import { MOBILE_EVENTS } from "../lib/telemetry/events";
import { mobileTelemetry } from "../lib/telemetry/runtime";
import { useReducedMotion } from "../lib/useReducedMotion";
import { radius, space, type } from "../lib/tokens";

/** Dots on each side of the mascot, between it and the two devices. */
const LINK_DOTS = 3;
/** One pass of the pulse from the computer to the phone. */
const PULSE_MS = 1800;

/**
 * The first screen: what the app is for in a picture and one line, and the one
 * thing to do. How to pair is the pair page's to say, under the scanner it
 * describes. The app needs a machine to show anything, so the ways on are
 * pairing one, or signing in to pull in the machines an account already has.
 * The same sign-in opens Orchestrator Cloud, which needs no machine at all.
 */
export default function OnboardingScreen() {
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const navigation = useNavigation();
	const insets = useSafeAreaInsets();
	const { reloadConfig } = useApp();
	const { client: cloudClient } = useCloud();
	const [signingIn, setSigningIn] = useState(false);

	useEffect(() => {
		mobileTelemetry()?.capture(MOBILE_EVENTS.onboardingStarted);
	}, []);

	// The same sign-in as Settings, which an unpaired phone can't reach: the
	// account's machines come in and the first one opens the board. An account
	// with Orchestrator Cloud has somewhere to run agents already, so it opens
	// the board too, with Cloud on it, rather than asking for a machine.
	function signIn() {
		setSigningIn(true);
		void (async () => {
			const account = (await loadAccount()) ?? (await signInToAccount());
			if (!account) return;
			await syncAccountHosts(account);
			await reloadConfig();
			if ((await loadHosts()).length > 0 || await hasCloud()) {
				mobileTelemetry()?.capture(MOBILE_EVENTS.onboardingCompleted);
				completeOnboarding(navigation);
				return;
			}
			Alert.alert(
				"No machines yet",
				`No machines are linked to ${account.email} yet. Pair one to get started.`,
				[{ text: "Pair a machine", onPress: () => router.push("/pair?from=onboarding") }],
			);
		})().catch((error: unknown) => {
			Alert.alert("Could not sign in", error instanceof Error ? error.message : "Try again.");
		}).finally(() => setSigningIn(false));
	}

	/** Whether the account reaches Orchestrator Cloud; an account it doesn't serve is refused. */
	async function hasCloud(): Promise<boolean> {
		try {
			await cloudClient.getMe();
			return true;
		} catch {
			return false;
		}
	}

	return (
		<View style={[styles.screen, { paddingTop: insets.top, paddingBottom: insets.bottom + space.lg }]}>
			<View style={styles.hero}>
				<PairingPicture />
				<Text accessibilityRole="header" style={styles.title}>Your agents, in your pocket</Text>
				<Text style={styles.lede}>Pair with your computer or cloud to watch and steer your agents from anywhere.</Text>
			</View>

			{/* Where the thumb already is. */}
			<Button
				title="Connect"
				icon="link"
				onPress={() => router.push("/pair?from=onboarding")}
				style={styles.cta}
			/>
			<Button
				title="Sign in"
				variant="ghost"
				loading={signingIn}
				onPress={signIn}
				style={[styles.cta, styles.signIn]}
			/>
		</View>
	);
}

/**
 * The computer and the phone, joined through the mascot by two short dotted
 * links, with a pulse running across them: the whole idea before a word is
 * read. One image to a screen reader; the headline says the rest.
 */
function PairingPicture() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const reduceMotion = useReducedMotion();
	const pulse = useSharedValue(0);
	useEffect(() => {
		if (reduceMotion) return;
		pulse.value = withRepeat(withTiming(1, { duration: PULSE_MS, easing: Easing.linear }), -1, false);
	}, [pulse, reduceMotion]);

	const dots = (side: 0 | 1) =>
		Array.from({ length: LINK_DOTS }, (_, i) => (
			<LinkDot key={i} index={side * LINK_DOTS + i} pulse={pulse} still={reduceMotion} />
		));

	return (
		<View
			accessible
			accessibilityRole="image"
			accessibilityLabel="Your computer, connected to this phone through Orchestrator"
			style={styles.picture}
		>
			<View style={styles.device}>
				<AppSymbol name="laptop" size={30} color={t.textPrimary} />
			</View>
			<View style={styles.link}>{dots(0)}</View>
			<Image source={MASCOT} style={styles.mascot} resizeMode="contain" />
			<View style={styles.link}>{dots(1)}</View>
			<View style={styles.device}>
				<AppSymbol name="phone" size={30} color={t.textPrimary} />
			</View>
		</View>
	);
}

/** A dot that brightens as the pulse passes it, then fades back. */
function LinkDot({ index, pulse, still }: { index: number; pulse: SharedValue<number>; still: boolean }) {
	const styles = useThemedStyles(makeStyles);
	const lit = useAnimatedStyle(() => {
		if (still) return { opacity: 0.5 };
		const distance = Math.abs(pulse.value * (LINK_DOTS * 2) - index);
		return { opacity: Math.max(0.2, 1 - distance / 1.5) };
	});
	return <Animated.View style={[styles.dot, lit]} />;
}

const DEVICE = 64;
const MASCOT_SIZE = 48;
const DOT = 5;

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		screen: { flex: 1, backgroundColor: t.bgBase, paddingHorizontal: space.xxl },
		hero: { flex: 1, alignItems: "center", justifyContent: "center" },

		picture: { flexDirection: "row", alignItems: "center", marginBottom: space.huge },
		device: {
			width: DEVICE,
			height: DEVICE,
			borderRadius: radius.lg,
			borderCurve: "continuous",
			backgroundColor: t.bgElevated,
			alignItems: "center",
			justifyContent: "center",
		},
		link: { flexDirection: "row", alignItems: "center", gap: space.xs, marginHorizontal: space.sm },
		dot: { width: DOT, height: DOT, borderRadius: DOT / 2, backgroundColor: t.textPrimary },
		mascot: { width: MASCOT_SIZE, height: MASCOT_SIZE * MASCOT_ASPECT },

		title: { ...type.title1, color: t.textPrimary, textAlign: "center" },
		lede: { ...type.callout, color: t.textSecondary, textAlign: "center", marginTop: space.sm, maxWidth: 300 },

		cta: { alignSelf: "center", width: "100%", maxWidth: 400 },
		signIn: { marginTop: space.sm },
	});

export { RouteErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";
