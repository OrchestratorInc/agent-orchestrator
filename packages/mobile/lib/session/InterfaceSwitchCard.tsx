import { StyleSheet, Text, View } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { Spinner } from "../Spinner";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import type { Theme } from "../theme";
import { fontScaleCap, iconSize, radius, space, type } from "../tokens";
import { Button } from "../ui";

export type InterfaceSwitchCardProps = {
	title: string;
	message: string;
	/** Recovery is blocked: a warning glyph instead of the spinner. */
	blocked?: boolean;
	/**
	 * The switch finished but this screen has not been swapped out: a check
	 * instead of the spinner, since nothing is running any more.
	 */
	ready?: boolean;
	cancellable?: boolean;
	cancelling?: boolean;
	onCancel?(): void;
	error?: string;
	retryable?: boolean;
	retrying?: boolean;
	onRetry?(): void;
};

/**
 * The card a Chat or Terminal screen holds over itself while the session
 * switches interface, from the tap until the route swaps the screen out. It
 * floats over the screen rather than replacing it, so the content underneath
 * keeps its place if the switch is cancelled or fails.
 */
export function InterfaceSwitchCard({ title, message, blocked, ready, cancellable, cancelling, onCancel, error, retryable, retrying, onRetry }: InterfaceSwitchCardProps) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.overlay}>
			<View style={styles.card}>
				{/* The system's own indicator while the switch runs: a turning
				    glyph that never turned read as frozen. */}
				{blocked ? (
					<AppSymbol name="warning" size={iconSize.xl} color={t.red} />
				) : ready ? (
					<AppSymbol name="checkCircle" size={iconSize.xl} color={t.green} />
				) : (
					<Spinner size="large" />
				)}
				<Text accessibilityRole="header" maxFontSizeMultiplier={fontScaleCap.title} style={styles.title}>
					{title}
				</Text>
				<Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.copy}>{message}</Text>
				{cancellable ? (
					<Button variant="secondary" title="Cancel switch" loading={cancelling} onPress={() => onCancel?.()} style={styles.action} />
				) : null}
				{error ? <Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.error}>{error}</Text> : null}
				{retryable ? (
					<Button variant="secondary" title="Retry" icon="refresh" loading={retrying} onPress={() => onRetry?.()} style={styles.action} />
				) : null}
			</View>
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		overlay: {
			...StyleSheet.absoluteFill,
			alignItems: "center",
			justifyContent: "center",
			paddingHorizontal: space.xxl,
			backgroundColor: t.scrim,
		},
		// A surface on the scrim needs no outline of its own.
		card: {
			width: "100%",
			maxWidth: 340,
			alignItems: "center",
			gap: space.sm,
			paddingHorizontal: space.xl,
			paddingVertical: space.xxl,
			borderRadius: radius.lg,
			borderCurve: "continuous",
			backgroundColor: t.bgSurface,
		},
		title: { ...type.headline, color: t.textPrimary, textAlign: "center" },
		copy: { ...type.footnote, color: t.textSecondary, textAlign: "center" },
		action: { marginTop: space.xxs, alignSelf: "stretch" },
		error: { ...type.footnote, color: t.red, textAlign: "center" },
	});
