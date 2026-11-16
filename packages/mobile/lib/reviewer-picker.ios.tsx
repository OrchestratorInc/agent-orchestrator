import { Label, Picker, Text } from "@expo/ui/swift-ui";
import { accessibilityIdentifier, disabled, listRowBackground, pickerStyle, tag, tint } from "@expo/ui/swift-ui/modifiers";
import { projectDefaultLabel, withCurrentOption, type ReviewerChoiceRow, type ReviewerPickerProps } from "./reviewer-picker.types";
import { HarnessImage, useHarnessLogoUris } from "./spawn-composer-controls.ios";
import { appFont } from "./swiftUIFont";
import { useTheme } from "./ThemeProvider";
import { useDeclinedChoice } from "./useDeclinedChoice";

/**
 * The reviewer, its model and its effort as menu pickers, rows of the sheet's
 * Form: the system draws the current value, the checkmark on it and the
 * selection announcement.
 */
export function ReviewerPicker({ reviewers, selectedReviewer, effectiveReviewer, onSelectReviewer, model, onSelectModel, effort, onSelectEffort, busy }: ReviewerPickerProps) {
	const rowModifiers = useRowModifiers(busy);
	const logoUris = useHarnessLogoUris(reviewers);
	const reviewerChoice = useDeclinedChoice(selectedReviewer, busy);
	return (
		<>
			<Picker
				key={`reviewer-${reviewerChoice.revision}`}
				label="Reviewer"
				selection={selectedReviewer}
				onSelectionChange={(id: string) => { reviewerChoice.choose(id); onSelectReviewer(id); }}
				modifiers={[...rowModifiers, accessibilityIdentifier("review-reviewer")]}
			>
				<Label title={projectDefaultLabel(reviewers, effectiveReviewer)} systemImage="person.2" modifiers={[tag("")]} />
				{withCurrentOption(reviewers, selectedReviewer).map((agent) => (
					<Label key={agent.id} title={agent.label} icon={<HarnessImage uri={logoUris[agent.id]} harness={agent.id} />} modifiers={[tag(agent.id)]} />
				))}
			</Picker>
			{model ? <ChoicePicker row={model} busy={busy} tag="review-model" onSelect={onSelectModel} /> : null}
			{effort ? <ChoicePicker row={effort} busy={busy} tag="review-effort" onSelect={onSelectEffort} /> : null}
		</>
	);
}

function useRowModifiers(busy: boolean) {
	const t = useTheme();
	return [pickerStyle("menu"), tint(t.textSecondary), appFont("body"), disabled(busy), listRowBackground(t.bgElevated)];
}

/** A setting under the reviewer; a choice it declines goes back to the sheet's value. */
function ChoicePicker({ row, busy, tag: identifier, onSelect }: { row: ReviewerChoiceRow; busy: boolean; tag: string; onSelect: (id: string) => void }) {
	const rowModifiers = useRowModifiers(busy);
	const choice = useDeclinedChoice(row.value, busy);
	return (
		<Picker
			key={`${identifier}-${choice.revision}`}
			label={row.title}
			selection={row.value}
			onSelectionChange={(id: string) => { choice.choose(id); onSelect(id); }}
			modifiers={[...rowModifiers, accessibilityIdentifier(identifier)]}
		>
			{row.options.map((option) => <Text key={option.id || "-"} modifiers={[tag(option.id)]}>{option.label}</Text>)}
		</Picker>
	);
}
