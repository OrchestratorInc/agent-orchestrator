import { Button, Host, TextInput, useNativeState } from "@expo/ui";
import { memo, useEffect } from "react";
import { StyleSheet, View } from "react-native";
import { useTheme, useThemeState } from "./ThemeProvider";
import type { EnvironmentKind } from "./environment/types";
import { workerDockVisibility } from "./worker-dock-layout";
import { space, type } from "./tokens";

export type WorkerDockProps = {
	/** Project detail reuses the dock for spawn without list controls. */
	controlsEnabled?: boolean;
	query: string;
	onQueryChange: (query: string) => void;
	onSpawn: () => void;
	searchOpen: boolean;
	onSearchOpen: () => void;
	onSearchClose: () => void;
	onOpenControls: () => void;
	projectFiltered: boolean;
	environmentFilter: "all" | EnvironmentKind;
	onSelectEnvironment: (environment: "all" | EnvironmentKind) => void;
	projectOptions: readonly { id: string; label: string }[];
	selectedProjectId: string;
	selectedProjectLabel: string;
	onSelectProject: (projectId: string) => void;
};

export const WorkerDock = memo(function WorkerDock({
	controlsEnabled = true,
	query,
	onQueryChange,
	onSpawn,
	searchOpen,
	onSearchClose,
	onOpenControls,
	projectFiltered,
}: WorkerDockProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const value = useNativeState(query);
	const visibility = workerDockVisibility(searchOpen, controlsEnabled);

	useEffect(() => {
		if (value.value !== query) value.value = query;
	}, [query, value]);

	return (
		<View style={styles.row}>
			{visibility.showControls ? <Host style={styles.actionHost} colorScheme={scheme} seedColor={t.accent}>
				<Button
					label="Filters"
					onPress={onOpenControls}
					testID="worker-controls"
					variant={projectFiltered ? "filled" : "outlined"}
					style={{ width: 52, height: 52, borderRadius: 28}}
				/>
			</Host> : null}
			{visibility.showSearch ? <Host style={styles.searchHost} colorScheme={scheme} seedColor={t.accent}>
				{
					<TextInput
						value={value}
						onChangeText={onQueryChange}
						placeholder="Search workers"
						autoFocus
						autoCapitalize="none"
						autoCorrect={false}
						returnKeyType="search"
						onBlur={() => {
							if (!query.trim()) onSearchClose();
						}}
						testID="worker-search"
						style={{
							width: "100%",
							height: 52,
							borderRadius: 16,
							backgroundColor: t.bgSubtle,
							borderWidth: StyleSheet.hairlineWidth,
							borderColor: t.borderDefault,
							paddingHorizontal: space.lg,
						}}
						textStyle={{ fontFamily: "Geist_400Regular", color: t.textPrimary, fontSize: type.callout.fontSize }}
						placeholderTextColor={t.textTertiary}
					/>
				}
			</Host> : null}
			{visibility.showSpawn && !visibility.showSearch ? <View style={styles.flexSpacer} /> : null}
			{visibility.showSpawn ? <Host style={styles.actionHost} colorScheme={scheme} seedColor={t.accent}>
				<Button
					label="+"
					onPress={onSpawn}
					testID="spawn-worker"
					variant="outlined"
					style={{ width: 52, height: 52, borderRadius: 28}}
				/>
			</Host> : null}
		</View>
	);
});

const styles = StyleSheet.create({
	row: { flex: 1, height: 52, flexDirection: "row", gap: space.sm },
	flexSpacer: { flex: 1 },
	searchHost: { flex: 1, height: 52 },
	actionHost: { width: 52, height: 52 },
});
