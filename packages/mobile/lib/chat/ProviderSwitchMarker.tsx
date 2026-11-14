import { useEffect, useState } from "react";
import { Animated, StyleSheet, Text, View } from "react-native";
import { AgentLogo } from "../AgentLogo";
import { getAgentModels, type AgentModelInfo } from "../api";
import { machineIdentity, type ServerConfig } from "../config";
import { Feather } from "../icons";
import { useApp } from "../store";
import { useBreathing } from "../ui";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { font, iconSize, space, type } from "../tokens";
import { providerSwitchLabel, readProviderSwitch, switchSideExtras, switchSideName, type ProviderSwitch, type ProviderSwitchSide } from "./providerSwitchModel";
import type { ConversationActivity } from "./types";

/**
 * Where one agent handed the conversation to another, drawn as desktop's
 * ProviderSwitchMarker: a rule each side of "Switched", then each agent's mark
 * and the model it ran, the outgoing one dim and the new one bright.
 */
export function ProviderSwitchMarker({ activity }: { activity: ConversationActivity }) {
	return <ProviderSwitchLine change={readProviderSwitch(activity.detail)} />;
}

/**
 * The line itself. While the switch's send is in flight it reads "Switching",
 * breathing as desktop's shimmers, with the agents left out until the
 * daemon's marker names them.
 */
export function ProviderSwitchLine({ change, switching }: { change?: ProviderSwitch; switching?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const breath = useBreathing(Boolean(switching));
	return (
		<View accessible accessibilityLabel={switching ? "Switching" : providerSwitchLabel(change)} accessibilityState={{ busy: Boolean(switching) }} style={styles.row}>
			<View style={styles.rule} />
			<View style={styles.center}>
				{switching
					? <Animated.Text style={[styles.text, breath ? { opacity: breath } : null]}>Switching</Animated.Text>
					: <Text style={styles.text}>Switched</Text>}
				{change && !switching ? (
					<>
						<Side side={change.source} />
						<Feather name="arrow-right" size={iconSize.xs} color={t.textTertiary} />
						<Side side={change.target} bright />
					</>
				) : null}
			</View>
			<View style={styles.rule} />
		</View>
	);
}

function Side({ side, bright }: { side: ProviderSwitchSide; bright?: boolean }) {
	const styles = useThemedStyles(makeStyles);
	const models = useRecordedModels(side.harness, Boolean(side.model));
	const extras = bright ? switchSideExtras(side) : [];
	return (
		<View style={styles.side}>
			<AgentLogo harness={side.harness} size={14} />
			<Text numberOfLines={1} style={[styles.text, styles.name, bright && styles.bright]}>{switchSideName(side, models)}</Text>
			{extras.map((extra) => <Text key={extra} style={styles.text}>{extra}</Text>)}
		</View>
	);
}

// The marker stores model ids; their names come from each agent's catalog on
// the session's machine, fetched once per machine and agent.
const catalogs = new Map<string, Promise<readonly AgentModelInfo[]>>();

function recordedModels(cfg: ServerConfig, harness: string): Promise<readonly AgentModelInfo[]> {
	// The machine's identity, not just its host id: a pairing migrated from
	// before host ids has none, and two such machines must not share names.
	const key = `${machineIdentity(cfg)}|${harness}`;
	let pending = catalogs.get(key);
	if (!pending) {
		pending = getAgentModels(cfg, harness).then((catalog) => catalog.models ?? []);
		// A failed read is retried by the next marker rather than cached.
		pending.catch(() => catalogs.delete(key));
		catalogs.set(key, pending);
	}
	return pending;
}

function useRecordedModels(harness: string, wanted: boolean): readonly AgentModelInfo[] {
	const { config } = useApp();
	const [models, setModels] = useState<readonly AgentModelInfo[]>([]);
	useEffect(() => {
		if (!config || !wanted || !harness) return;
		let cancelled = false;
		recordedModels(config, harness).then((next) => { if (!cancelled) setModels(next); }, () => {});
		return () => { cancelled = true; };
	}, [config, harness, wanted]);
	return models;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	row: { flexDirection: "row", alignItems: "center", gap: space.md, paddingVertical: space.sm },
	rule: { flex: 1, minWidth: space.lg, height: StyleSheet.hairlineWidth, backgroundColor: t.borderDefault },
	center: { flexShrink: 1, flexDirection: "row", alignItems: "center", gap: space.md },
	side: { flexShrink: 1, flexDirection: "row", alignItems: "center", gap: space.xs },
	text: { fontFamily: font.regular, color: t.textTertiary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight },
	name: { flexShrink: 1 },
	bright: { color: t.textPrimary },
});
