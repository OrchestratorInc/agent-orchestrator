import { Stack, useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useMemo, useRef, useState } from "react";
import { Alert, Platform, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { backOr } from "../lib/backNavigation";
import { isCloudHost } from "../lib/cloud/mapping";
import { haptics } from "../lib/haptics";
import { Feather } from "../lib/icons";
import { buildSidebarTree, projectLetter, projectTileColor, type SidebarProjectNode, type SidebarSessionRow } from "../lib/sidebar/sidebarModel";
import { canSaveTag, TAG_NAME_MAX, type SidebarTag } from "../lib/sidebar/sidebarTags";
import { deleteTag, loadTags, newTagId, requestTagSelection, saveTag, useTags } from "../lib/sidebar/tagStore";
import { useApp } from "../lib/store";
import { textSelectionColors } from "../lib/textSelection";
import type { Theme } from "../lib/theme";
import { useTheme, useThemedStyles } from "../lib/ThemeProvider";
import { font } from "../lib/tokens";

export { RouteErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";

/**
 * A home tag, made or edited: its name, and the projects and sessions it
 * gathers. Ticking a project brings its orchestrator and every worker;
 * otherwise single sessions can be ticked, a project's under it. Opened from
 * the devices bar's + (new) or a tag's long press (edit).
 */
export default function TagPage() {
	const router = useRouter();
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const insets = useSafeAreaInsets();
	const { id } = useLocalSearchParams<{ id?: string }>();
	const app = useApp();
	const tags = useTags();
	useEffect(() => { void loadTags(); }, []);
	const editing = id ? tags.find((tag) => tag.id === id) : undefined;

	const [name, setName] = useState(editing?.name ?? "");
	const [projects, setProjects] = useState<string[]>(editing?.projects ?? []);
	const [sessions, setSessions] = useState<string[]>(editing?.sessions ?? []);
	const [open, setOpen] = useState<string[]>([]);
	// A tag that loads after the page opens fills the form once.
	const filled = useRef(Boolean(editing));
	useEffect(() => {
		if (filled.current || !editing) return;
		filled.current = true;
		setName(editing.name);
		setProjects(editing.projects);
		setSessions(editing.sessions);
	}, [editing]);

	const tree = useMemo(() => buildSidebarTree({
		sessions: app.allSessions,
		projects: app.allProjects,
		showHosts: app.hostStates.length + (app.cloud ? 1 : 0) > 1,
		isUnread: () => false,
		isCloudHost,
	}), [app.allProjects, app.allSessions, app.cloud, app.hostStates.length]);

	const draft = { name, projects, sessions };
	const canSave = canSaveTag(draft);
	const save = () => {
		if (!canSaveTag(draft)) return;
		const tagId = editing?.id ?? newTagId();
		// A new tag is chosen on home straight away.
		if (!editing) requestTagSelection(tagId);
		saveTag({ id: tagId, ...draft } satisfies SidebarTag);
		haptics.success();
		backOr(router);
	};
	const saveRef = useRef(save);
	saveRef.current = save;

	const remove = () => {
		if (!editing) return;
		haptics.warning();
		Alert.alert(`Delete "${editing.name}"?`, "The projects and sessions stay; only the tag goes.", [
			{ text: "Cancel", style: "cancel" },
			{ text: "Delete", style: "destructive", onPress: () => { deleteTag(editing.id); backOr(router); } },
		]);
	};

	const toggle = (list: string[], set: (next: string[]) => void, key: string) => {
		haptics.select();
		set(list.includes(key) ? list.filter((item) => item !== key) : [...list, key]);
	};

	const header = useMemo(() => (
		<>
			<Stack.Screen options={{ title: editing || id ? "Edit tag" : "New tag", ...(Platform.OS === "android" ? { headerRight: () => <SaveButton enabled={canSave} onPress={() => saveRef.current()} /> } : null) }} />
			{Platform.OS === "ios" ? (
				<Stack.Toolbar placement="right">
					<Stack.Toolbar.Button accessibilityLabel="Save tag" disabled={!canSave} style={{ fontWeight: "600" }} onPress={() => saveRef.current()}>Save</Stack.Toolbar.Button>
				</Stack.Toolbar>
			) : null}
		</>
	), [canSave, editing, id]);

	return (
		<>
			{header}
			<ScrollView style={styles.screen} contentContainerStyle={[styles.content, { paddingBottom: insets.bottom + 32 }]} keyboardShouldPersistTaps="handled" contentInsetAdjustmentBehavior="automatic">
				<Text style={styles.label}>Name</Text>
				<TextInput
					value={name}
					onChangeText={setName}
					placeholder="Frontend, This week, Client…"
					placeholderTextColor={t.textTertiary}
					maxLength={TAG_NAME_MAX}
					autoFocus={!editing && !id}
					returnKeyType="done"
					style={styles.input}
					accessibilityLabel="Tag name"
					{...textSelectionColors(t)}
				/>
				<Text style={styles.hint}>Choose what it shows: a whole project, or single sessions.</Text>

				{tree.projects.length > 0 ? <Text style={styles.label}>Projects</Text> : null}
				{tree.projects.map((node) => (
					<ProjectChoice
						key={node.key}
						node={node}
						checked={projects.includes(node.key)}
						open={open.includes(node.key)}
						sessions={sessions}
						onToggle={() => toggle(projects, setProjects, node.key)}
						onOpen={() => setOpen((prev) => (prev.includes(node.key) ? prev.filter((item) => item !== node.key) : [...prev, node.key]))}
						onToggleSession={(key) => toggle(sessions, setSessions, key)}
						styles={styles}
						t={t}
					/>
				))}

				{tree.standalone.length > 0 ? <Text style={styles.label}>Sessions</Text> : null}
				{tree.standalone.map((row) => (
					<SessionChoice key={row.key} row={row} checked={sessions.includes(row.key)} onToggle={() => toggle(sessions, setSessions, row.key)} styles={styles} t={t} />
				))}

				{editing ? (
					<Pressable accessibilityRole="button" onPress={remove} style={({ pressed }) => [styles.delete, pressed && { opacity: 0.6 }]} testID="tag-delete">
						<Text style={styles.deleteLabel}>Delete tag</Text>
					</Pressable>
				) : null}
			</ScrollView>
		</>
	);
}

function SaveButton({ enabled, onPress }: { enabled: boolean; onPress(): void }) {
	const t = useTheme();
	return (
		<Pressable accessibilityRole="button" accessibilityLabel="Save tag" disabled={!enabled} onPress={onPress} hitSlop={8}>
			<Text style={{ fontFamily: font.semibold, fontSize: 16, color: enabled ? t.textPrimary : t.textTertiary }}>Save</Text>
		</Pressable>
	);
}

/** A checkbox: an outlined square, filled with a check when ticked. */
function Check({ checked, t }: { checked: boolean; t: Theme }) {
	return (
		<View style={{ width: 18, height: 18, borderRadius: 5, borderWidth: checked ? 0 : 1.5, borderColor: t.textTertiary, backgroundColor: checked ? t.textPrimary : "transparent", alignItems: "center", justifyContent: "center" }}>
			{checked ? <Feather name="check" size={13} color={t.bgBase} /> : null}
		</View>
	);
}

function ProjectChoice({ node, checked, open, sessions, onToggle, onOpen, onToggleSession, styles, t }: {
	node: SidebarProjectNode;
	checked: boolean;
	open: boolean;
	sessions: readonly string[];
	onToggle(): void;
	onOpen(): void;
	onToggleSession(key: string): void;
	styles: Styles;
	t: Theme;
}) {
	const picked = node.sessions.filter((row) => sessions.includes(row.key)).length;
	const detail = checked ? "Everything" : picked ? `${picked} chosen` : undefined;
	return (
		<View>
			<View style={styles.row}>
				<Pressable
					accessibilityRole="checkbox"
					accessibilityState={{ checked }}
					accessibilityLabel={`${node.project.name}${node.hostLabel ? `, ${node.hostLabel}` : ""}`}
					onPress={onToggle}
					style={({ pressed }) => [styles.rowMain, pressed && styles.pressed]}
					testID={`tag-project-${node.project.id}`}
				>
					<Check checked={checked} t={t} />
					<View style={[styles.tile, { backgroundColor: projectTileColor(node.project.id) }]}>
						<Text style={styles.tileLetter}>{projectLetter(node.project.name)}</Text>
					</View>
					<Text style={styles.rowTitle} numberOfLines={1}>{node.project.name}</Text>
					{node.hostLabel ? <Text style={styles.rowMeta} numberOfLines={1}>{node.hostLabel}</Text> : null}
					{detail ? <Text style={styles.rowMeta}>{detail}</Text> : null}
				</Pressable>
				{!checked && node.sessions.length > 0 ? (
					<Pressable
						accessibilityRole="button"
						accessibilityLabel={open ? `Hide ${node.project.name}'s sessions` : `Choose sessions in ${node.project.name}`}
						accessibilityState={{ expanded: open }}
						onPress={onOpen}
						hitSlop={6}
						style={({ pressed }) => [styles.disclosure, pressed && styles.pressed]}
					>
						<Feather name={open ? "chevron-up" : "chevron-down"} size={16} color={t.textTertiary} />
					</Pressable>
				) : null}
			</View>
			{!checked && open ? node.sessions.map((row) => (
				<View key={row.key} style={styles.indented}>
					<SessionChoice row={row} checked={sessions.includes(row.key)} onToggle={() => onToggleSession(row.key)} styles={styles} t={t} />
				</View>
			)) : null}
		</View>
	);
}

function SessionChoice({ row, checked, onToggle, styles, t }: { row: SidebarSessionRow; checked: boolean; onToggle(): void; styles: Styles; t: Theme }) {
	return (
		<Pressable
			accessibilityRole="checkbox"
			accessibilityState={{ checked }}
			accessibilityLabel={row.title}
			onPress={onToggle}
			style={({ pressed }) => [styles.row, styles.rowMain, pressed && styles.pressed]}
			testID={`tag-session-${row.session.id}`}
		>
			<Check checked={checked} t={t} />
			<Text style={styles.sessionTitle} numberOfLines={1}>{row.title}</Text>
			{row.hostLabel ? <Text style={styles.rowMeta} numberOfLines={1}>{row.hostLabel}</Text> : null}
		</Pressable>
	);
}

type Styles = ReturnType<typeof makeStyles>;

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	content: { paddingHorizontal: 16, paddingTop: 12 },
	label: { marginTop: 20, marginBottom: 6, fontFamily: font.medium, fontSize: 13, color: t.textTertiary },
	input: { height: 44, paddingHorizontal: 12, borderRadius: 10, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderDefault, backgroundColor: t.bgElevated, fontFamily: font.regular, fontSize: 16, color: t.textPrimary },
	hint: { marginTop: 8, fontFamily: font.regular, fontSize: 13, color: t.textTertiary },
	row: { minHeight: 44, flexDirection: "row", alignItems: "center" },
	rowMain: { flex: 1, minHeight: 44, flexDirection: "row", alignItems: "center", gap: 10, paddingHorizontal: 6, borderRadius: 8 },
	pressed: { backgroundColor: t.bgSubtle },
	tile: { width: 20, height: 20, borderRadius: 5, alignItems: "center", justifyContent: "center" },
	tileLetter: { fontFamily: font.semibold, fontSize: 11, color: t.textPrimary, opacity: 0.8 },
	rowTitle: { flexShrink: 1, fontFamily: font.medium, fontSize: 15, color: t.textPrimary },
	sessionTitle: { flexShrink: 1, fontFamily: font.regular, fontSize: 15, color: t.textSecondary },
	rowMeta: { marginLeft: "auto", fontFamily: font.regular, fontSize: 13, color: t.textTertiary },
	disclosure: { width: 36, height: 36, borderRadius: 8, alignItems: "center", justifyContent: "center" },
	indented: { paddingLeft: 28 },
	delete: { marginTop: 32, height: 44, alignItems: "center", justifyContent: "center", borderRadius: 10, backgroundColor: t.bgElevated },
	deleteLabel: { fontFamily: font.medium, fontSize: 15, color: t.red },
});
