import { Box, FilterChip, Host, Icon, LazyRow, Text } from "@expo/ui/jetpack-compose";
import { startTransition, useState } from "react";
import { background, clip, semantics, Shapes, size, testID } from "@expo/ui/jetpack-compose/modifiers";
import { freshnessAccessibilityLabel, freshnessColor, freshnessTone } from "./machineFreshness";
import { haptics } from "./haptics";
import { useTheme, useThemeState } from "./ThemeProvider";
import { materialDrawable } from "./toolbarIcon.android";
import { gutter, space } from "./tokens";
import { ALL_WORKER_ENVIRONMENTS, workerEnvironmentOptions } from "./worker-controls";
import type { WorkerEnvironmentFilterProps } from "./worker-environment-filter.types";

export type { WorkerEnvironmentFilterProps } from "./worker-environment-filter.types";

/** Material's filter-chip glyph size. */
const CHIP_ICON_SIZE = 18;
/** The freshness dot, the same size as on the rows. */
const FRESHNESS_DOT = 7;

/**
 * The Workers board's machine filter as a row of Material 3 filter chips: "All
 * environments" and one chip per paired machine, scrolling sideways. The chosen
 * chip is checked as well as filled, and Compose gives each one its 48dp
 * target, ripple and selected state for TalkBack.
 */
export function WorkerEnvironmentFilter({ environments, selectedEnvironmentId, onSelectEnvironment }: WorkerEnvironmentFilterProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	// The tapped chip is checked at once, ahead of the board it asks for: the
	// board below is rebuilt for the new machine, which on a phone took seconds,
	// and the check waited for all of it. Kept only until the board first moves;
	// from then on the board says what is chosen, even if it moves back to
	// where the tap started (the tapped machine went away, say).
	const [tapped, setTapped] = useState<string>();
	const [boardSeen, setBoardSeen] = useState(selectedEnvironmentId);
	const boardMoved = boardSeen !== selectedEnvironmentId;
	if (boardMoved) {
		setBoardSeen(selectedEnvironmentId);
		setTapped(undefined);
	}
	const chosenId = !boardMoved && tapped !== undefined ? tapped : selectedEnvironmentId;
	const select = (environmentId: string) => {
		if (environmentId === chosenId) return;
		haptics.select();
		setTapped(environmentId);
		startTransition(() => onSelectEnvironment(environmentId));
	};
	return (
		<Host matchContents={{ vertical: true }} colorScheme={scheme}>
			<LazyRow
				horizontalArrangement={{ spacedBy: space.sm }}
				verticalAlignment="center"
				contentPadding={{ start: gutter, end: gutter }}
				modifiers={[testID("worker-environment-strip")]}
			>
				{workerEnvironmentOptions(environments).map((environment) => {
					const all = environment.id === ALL_WORKER_ENVIRONMENTS;
					const selected = environment.id === chosenId;
					const tone = freshnessTone(environment.freshness);
					return (
						<FilterChip
							// Keyed by its dot too: Compose took up a trailing slot only when
							// the chip was built, so a dot that appeared later never drew.
							key={`${environment.id}:${tone ?? "live"}`}
							selected={selected}
							// Choosing the chosen machine again goes back to every environment.
							onClick={() => select(selected && !all ? ALL_WORKER_ENVIRONMENTS : environment.id)}
							colors={{
								labelColor: t.textSecondary,
								selectedContainerColor: t.accentTint,
								selectedLabelColor: t.accent,
								selectedLeadingIconColor: t.accent,
							}}
							// Material outlines only the chips that are not chosen.
							border={selected ? undefined : { width: 1, color: t.borderDefault }}
							modifiers={[
								testID(`worker-environment-chip-${environment.id}`),
								// The dot is a picture; TalkBack hears its state with the name, as
								// VoiceOver does on iOS. A live chip keeps its label alone.
								...(tone ? [semantics({ contentDescription: `${environment.label}, ${freshnessAccessibilityLabel(environment.freshness)}` })] : []),
							]}
						>
							{selected ? (
								<FilterChip.LeadingIcon>
									<Icon source={materialDrawable("check")} size={CHIP_ICON_SIZE} />
								</FilterChip.LeadingIcon>
							) : null}
							{/* Trailing: Material keeps the leading slot for the check. */}
							{tone ? (
								<FilterChip.TrailingIcon>
									<Box modifiers={[size(FRESHNESS_DOT, FRESHNESS_DOT), clip(Shapes.Circle), background(freshnessColor(t, tone)), testID(`worker-environment-freshness-${environment.id}`)]} />
								</FilterChip.TrailingIcon>
							) : null}
							<FilterChip.Label>
								<Text maxLines={1} overflow="ellipsis" style={{ typography: "labelLarge", fontFamily: "DMSans_500Medium" }}>
									{environment.label}
								</Text>
							</FilterChip.Label>
						</FilterChip>
					);
				})}
			</LazyRow>
		</Host>
	);
}
