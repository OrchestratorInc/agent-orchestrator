import { Platform, StyleSheet, Text } from "react-native";
import { AgentLogo } from "./AgentLogo";
import type { RankedAgent } from "./agentPicker";
import { haptics } from "./haptics";
import { NativePickerList } from "./NativePickerList";
import type { Theme } from "./theme";
import { useThemedStyles } from "./ThemeProvider";
import { SheetHeader } from "./ui";
import { iconSize, type } from "./tokens";

// Picks the agent CLI that will run a session.
//
// Rows carry the brand mark, matching desktop's AgentSelectMenuItem — this was
// the last agent-bearing surface in the app still showing bare text.
//
// Refresh lives here rather than on the form: a stale catalog is something you
// discover while looking for an agent that is missing or unauthorised, which is
// this list, not the screen behind it. Pull the list down to refresh it.
export function AgentPickerSheet({
	onClose,
	agents,
	selected,
	onSelect,
	onRefresh,
	refreshing = false,
	error,
	title = "Agent",
	subtitle = "The CLI that runs this session.",
}: {
	/** Dismisses the sheet route. */
	onClose: () => void;
	agents: RankedAgent[];
	selected: string;
	onSelect: (id: string) => void;
	/** Settles once the catalog has been fetched again. */
	onRefresh: () => Promise<void>;
	refreshing?: boolean;
	error?: string | null;
	/** Android's, in the body; iOS carries the title in the sheet's native bar. */
	title?: string;
	subtitle?: string;
}) {
	const s = useThemedStyles(makeStyles);
	return (
		<NativePickerList
			header={<>
				<SheetHeader title={Platform.OS === "ios" ? undefined : title} subtitle={subtitle} />
				{error ? <Text accessibilityRole="alert" style={s.error}>{error}</Text> : null}
			</>}
			options={agents.map((agent) => ({
				id: agent.id,
				title: agent.label,
				detail: agent.status || undefined,
				// Amber for something you can act on, muted for a missing install —
				// desktop's tone split.
				warn: agent.availability === "auth-unknown" || agent.availability === "needs-auth",
				// Genuinely disabled, not just ignored on press. The previous picker
				// let you tap an unusable agent and silently did nothing.
				disabled: !agent.selectable,
				mark: <AgentLogo harness={agent.id} size={iconSize.lg} />,
			}))}
			selected={selected}
			onSelect={(id) => {
				haptics.select();
				// Dismiss before reporting the choice, matching the other sheets.
				// onClose leaves the sheet, so a callback that navigated first would
				// have that dismiss pop the destination instead of this sheet.
				onClose();
				onSelect(id);
			}}
			refreshing={refreshing}
			onRefresh={onRefresh}
			empty="No agents reported. Check that Orchestrator is running on your computer, then pull down to refresh."
		/>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		error: { fontFamily: type.footnote.fontFamily, color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	});
