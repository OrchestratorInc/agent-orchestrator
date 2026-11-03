import { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, FlatList, Pressable, StyleSheet, Text, View } from "react-native";
import { AgentLogo } from "./AgentLogo";
import { listArtifactVersions, syncArtifactVersions } from "./api";
import { formatVersionTime, restoredNote, versionAuthor, type ArtifactVersionSummary } from "./artifactVersions";
import { userFacingError } from "./connectionError";
import { haptics } from "./haptics";
import { Feather } from "./icons";
import { useApp } from "./store";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { iconSize, space, type } from "./tokens";
import { Button, SheetHeader } from "./ui";

const AVATAR = 24;

/**
 * Every kept version of one artifact file, newest first, the first marked
 * Current: the phone's counterpart of the desktop viewer's History menu.
 * As there, the kept list shows at once while any edit the daemon has not
 * picked up yet is recorded behind it, then the list is replaced.
 * `onSelect` gets null for the current version, which is the live file.
 */
export function ArtifactHistorySheet({ sessionId, path, name, onSelect }: { sessionId: string; path: string; name: string; onSelect(version: number | null): void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { config, sessions } = useApp();
	const fallbackHarness = sessions.find((session) => session.id === sessionId)?.harness;
	const [versions, setVersions] = useState<ArtifactVersionSummary[]>();
	const [error, setError] = useState<string>();
	const [attempt, setAttempt] = useState(0);
	const now = useRef(new Date()).current;

	useEffect(() => {
		if (!config) return;
		const controller = new AbortController();
		const { signal } = controller;
		let synced = false;
		setError(undefined);
		listArtifactVersions(config, sessionId, path, { signal })
			.then((listed) => { if (!synced) setVersions(listed); })
			.catch(() => {});
		syncArtifactVersions(config, sessionId, path, { signal })
			.then((latest) => { synced = true; setVersions(latest); })
			.catch((cause) => { if (!signal.aborted) setError(userFacingError(cause, "Couldn't load the version history.")); });
		return () => controller.abort();
	}, [attempt, config, path, sessionId]);

	const retry = useCallback(() => setAttempt((n) => n + 1), []);
	const current = versions?.[0]?.version;
	const subtitle = versions?.length ? `${name} · ${versions.length} ${versions.length === 1 ? "version" : "versions"}` : name;

	return (
		<FlatList
			style={styles.list}
			contentContainerStyle={styles.content}
			data={versions ?? []}
			keyExtractor={(version) => String(version.version)}
			// Keeps an Android drag with the list rather than the sheet's own pan.
			nestedScrollEnabled
			ListHeaderComponent={<View style={styles.header}><SheetHeader title="Version history" subtitle={subtitle} /></View>}
			ListEmptyComponent={
				!versions && !error ? (
					<View style={styles.state}><ActivityIndicator color={t.accent} /></View>
				) : error ? (
					<View style={styles.state}>
						<Text style={styles.stateText}>{error}</Text>
						<Button title="Try again" icon="refresh-cw" variant="ghost" onPress={retry} />
					</View>
				) : (
					<View style={styles.state}><Text style={styles.stateText}>No earlier versions yet.</Text></View>
				)
			}
			renderItem={({ item, index }) => {
				const isCurrent = item.version === current;
				const label = formatVersionTime(item.createdAt, now);
				const note = restoredNote(item, versions ?? [], now);
				const author = versionAuthor(item, fallbackHarness);
				return (
					<Pressable
						accessibilityRole="button"
						accessibilityLabel={`${label}${isCurrent ? ", current" : ""}${note ? `, ${note}` : ""}, ${author.kind === "user" ? "saved by you" : "written by the agent"}`}
						onPress={() => { haptics.select(); onSelect(isCurrent ? null : item.version); }}
						style={({ pressed }) => [styles.row, index > 0 && styles.rowDivider, pressed && styles.pressed]}
					>
						<View style={styles.rowText}>
							<View style={styles.titleLine}>
								<Text style={styles.time}>{label || `Version ${item.version}`}</Text>
								{isCurrent ? <Text style={styles.current}>Current</Text> : null}
							</View>
							{note ? <Text numberOfLines={1} style={styles.note}>{note}</Text> : null}
						</View>
						{author.kind === "user" ? (
							<View style={styles.userAvatar}><Feather name="user" size={iconSize.xs} color={t.textSecondary} /></View>
						) : (
							<AgentLogo harness={author.harness} size={AVATAR} />
						)}
						<Feather name="chevron-right" size={iconSize.sm} color={t.textFaint} />
					</Pressable>
				);
			}}
		/>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	list: { flex: 1, backgroundColor: t.bgSurface },
	content: { paddingHorizontal: space.xl, paddingTop: space.xl, paddingBottom: space.xxxl },
	header: { marginBottom: space.md },
	state: { alignItems: "center", gap: space.md, paddingVertical: space.xxl },
	stateText: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, textAlign: "center" },
	row: { minHeight: 56, flexDirection: "row", alignItems: "center", gap: space.md, paddingVertical: space.sm },
	rowDivider: { borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: t.borderSubtle },
	pressed: { opacity: 0.58 },
	rowText: { flex: 1, gap: space.hair },
	titleLine: { flexDirection: "row", alignItems: "baseline", gap: space.sm },
	time: { fontFamily: "Geist_500Medium", color: t.textPrimary, fontSize: type.subheadline.fontSize, fontWeight: "500", fontVariant: ["tabular-nums"] },
	current: { fontFamily: "Geist_500Medium", color: t.accent, fontSize: type.caption2.fontSize, fontWeight: "500" },
	note: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight },
	userAvatar: { width: AVATAR, height: AVATAR, borderRadius: AVATAR / 2, alignItems: "center", justifyContent: "center", backgroundColor: t.bgSubtle },
});
