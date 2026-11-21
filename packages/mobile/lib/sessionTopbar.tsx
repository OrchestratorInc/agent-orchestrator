import { Stack, useRouter } from "expo-router";
import { useMemo, type ReactNode } from "react";
import { View } from "react-native";
import { HeaderPillButton } from "./headerPill";
import { OrchestratorIcon } from "./orchestrator-icon";
import { useTheme } from "./ThemeProvider";
// Desktop's own orchestrator mark (components/icons.tsx), drawn from the same
// paths into a template image: no SF Symbol or Material glyph is that shape.
import ORCHESTRATOR_GLYPH from "../assets/icons/orchestrator.png";
import { hostedProjectSections, hostedRowKey } from "./hostedRows";
import { useApp } from "./store";
import { toolbarIcon } from "./toolbarIcon";
import { useOrchestratorLauncher } from "./useOrchestratorLauncher";
import { sessionTopbarActionIds, type SessionTopbarActionId } from "./sessionTopbarModel";

/**
 * The desktop session topbar's actions (SessionTopbarActions.tsx), on the
 * phone's session header beside its "⋯":
 *
 * - a worker leads with its project's Orchestrator, its way back to the
 *   orchestrator coordinating it;
 * - an orchestrator offers its Board, then New task, the one primary action.
 *
 * A standalone agent has no project, so neither. The editor and Cues buttons
 * act on the desktop machine itself and have no phone counterpart. Icon-only,
 * as desktop's compact topbar draws them: labels beside them would cut the
 * session's title on a phone's bar.
 */
export type SessionTopbarAction = {
	id: SessionTopbarActionId;
	label: string;
	symbol: "orchestrator" | "board" | "add";
	disabled?: boolean;
	onPress(): void;
};

const LABELS: Record<SessionTopbarActionId, string> = { orchestrator: "Orchestrator", board: "Board", "new-task": "New task" };
const SYMBOLS: Record<SessionTopbarActionId, SessionTopbarAction["symbol"]> = { orchestrator: "orchestrator", board: "board", "new-task": "add" };

export function useSessionTopbarActions({ orchestrator, projectId, hostId }: { orchestrator: boolean; projectId?: string; hostId?: string }): SessionTopbarAction[] {
	const router = useRouter();
	const { hostStates, cloud, currentHostId } = useApp();
	const { busyProjects, openOrchestrator } = useOrchestratorLauncher();
	const host = hostId ?? currentHostId;
	// The project's orchestrator, as home's project row finds it.
	const row = useMemo(() => {
		if (!projectId) return undefined;
		for (const section of hostedProjectSections(cloud ? [...hostStates, cloud] : hostStates)) {
			const found = section.data.find((item) => item.project.id === projectId && item.project.hostId === host);
			if (found) return found;
		}
		return undefined;
	}, [cloud, host, hostStates, projectId]);
	const busy = Boolean(projectId && host && busyProjects.has(hostedRowKey(host, projectId)));
	return useMemo(() => sessionTopbarActionIds({ orchestrator, projectId }).flatMap((id): SessionTopbarAction[] => {
		const base = { id, label: LABELS[id], symbol: SYMBOLS[id] };
		switch (id) {
			case "orchestrator":
				return row ? [{ ...base, label: row.action === "open" ? "Open orchestrator" : "Start orchestrator", disabled: busy, onPress: () => openOrchestrator(row) }] : [];
			case "board":
				return host ? [{ ...base, onPress: () => router.push({ pathname: "/project/[id]", params: { id: projectId!, hostId: host } }) }] : [];
			default:
				return [{ ...base, onPress: () => router.push({ pathname: "/spawn", params: { projectId: projectId!, ...(host ? { hostId: host } : {}) } }) }];
		}
	}), [busy, host, openOrchestrator, orchestrator, projectId, router, row]);
}

/** The actions as native bar buttons, for a header's `Stack.Toolbar placement="right"`, ahead of its "⋯". */
export function sessionTopbarToolbarButtons(actions: readonly SessionTopbarAction[]) {
	return actions.map((action) => (
		<Stack.Toolbar.Button
			key={action.id}
			icon={action.id === "orchestrator" ? ORCHESTRATOR_GLYPH : toolbarIcon(action.symbol)}
			iconRenderingMode={action.id === "orchestrator" ? "template" : undefined}
			accessibilityLabel={action.label}
			// Flat on the bar, as the desktop topbar's icons are, without iOS 26's shared glass.
			hidesSharedBackground
			disabled={action.disabled}
			onPress={action.onPress}
		/>
	));
}

/** The actions as Android's floating header pills, ahead of the "⋯" pill. */
export function SessionTopbarPills({ actions, children }: { actions: readonly SessionTopbarAction[]; children?: ReactNode }) {
	const t = useTheme();
	return (
		<View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
			{actions.map((action) => (
				action.id === "orchestrator" ? (
					<HeaderPillButton key={action.id} label={action.label} onPress={action.disabled ? () => {} : action.onPress} testID={`header-${action.id}`}>
						<OrchestratorIcon size={20} color={t.textPrimary} />
					</HeaderPillButton>
				) : (
					<HeaderPillButton key={action.id} icon={action.symbol} label={action.label} onPress={action.disabled ? () => {} : action.onPress} testID={`header-${action.id}`} />
				)
			))}
			{children}
		</View>
	);
}
