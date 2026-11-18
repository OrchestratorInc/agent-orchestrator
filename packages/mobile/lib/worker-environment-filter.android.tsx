import { FilterChip, Host, Icon, LazyRow, Text } from "@expo/ui/jetpack-compose";
import { testID } from "@expo/ui/jetpack-compose/modifiers";
import { haptics } from "./haptics";
import { useTheme, useThemeState } from "./ThemeProvider";
import { materialDrawable } from "./toolbarIcon.android";
import { gutter, space } from "./tokens";
import { ALL_WORKER_ENVIRONMENTS, workerEnvironmentOptions } from "./worker-controls";
import type { WorkerEnvironmentFilterProps } from "./worker-environment-filter.types";

export type { WorkerEnvironmentFilterProps } from "./worker-environment-filter.types";

/** Material's filter-chip glyph size. */
const CHIP_ICON_SIZE = 18;

/**
 * The Workers board's machine filter as a row of Material 3 filter chips: "All
 * environments" and one chip per paired machine, scrolling sideways. The chosen
 * chip is checked as well as filled, and Compose gives each one its 48dp
 * target, ripple and selected state for TalkBack.
 */
export function WorkerEnvironmentFilter({ environments, selectedEnvironmentId, onSelectEnvironment }: WorkerEnvironmentFilterProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const select = (environmentId: string) => {
		if (environmentId === selectedEnvironmentId) return;
		haptics.select();
		onSelectEnvironment(environmentId);
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
					const selected = environment.id === selectedEnvironmentId;
					return (
						<FilterChip
							key={environment.id}
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
							modifiers={[testID(`worker-environment-chip-${environment.id}`)]}
						>
							{selected ? (
								<FilterChip.LeadingIcon>
									<Icon source={materialDrawable("check")} size={CHIP_ICON_SIZE} />
								</FilterChip.LeadingIcon>
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
