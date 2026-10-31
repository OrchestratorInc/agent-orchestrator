import { Animated, StyleSheet, Text, View } from "react-native";
import { useEnterTransition } from "./motionHooks";
import type { Theme } from "./theme";
import { useThemedStyles } from "./ThemeProvider";
import { fontScaleCap, space, type } from "./tokens";
import { Button, MascotLamp } from "./ui";
import { workersIdleCopy, type WorkersIdleInput } from "./workers-idle-copy";

/**
 * The conductor at rest. Its wand lamp is the live connection light (see
 * MascotLamp), so the one picture on the screen is also true.
 */
export function WorkersIdleState({ onNewAgent, ...input }: WorkersIdleInput & { onNewAgent: () => void }) {
	const s = useThemedStyles(makeStyles);
	const enter = useEnterTransition(space.xxs);
	const copy = workersIdleCopy(input);
	return (
		<Animated.View style={[s.wrap, enter]}>
			<View style={s.mascot}>
				<MascotLamp status={input.connection} size={64} />
			</View>
			<Text maxFontSizeMultiplier={fontScaleCap.body} style={s.title}>{copy.title}</Text>
			<Text maxFontSizeMultiplier={fontScaleCap.body} style={s.line}>{`${copy.status}\n${copy.history}`}</Text>
			<Button title="New agent" icon="plus" variant="ghost" onPress={onNewAgent} style={s.action} />
		</Animated.View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		wrap: {
			flex: 1,
			minHeight: 320,
			alignItems: "center",
			justifyContent: "center",
			paddingHorizontal: space.huge,
			paddingVertical: space.xxl,
		},
		mascot: { marginBottom: space.xl },
		title: { ...type.headline, color: t.textPrimary, textAlign: "center" },
		line: { ...type.subheadline, color: t.textSecondary, textAlign: "center", marginTop: space.xs },
		action: { marginTop: space.xl },
	});
