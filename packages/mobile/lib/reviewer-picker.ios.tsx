import { Label, Picker, Text } from "@expo/ui/swift-ui";
import { accessibilityIdentifier, disabled, listRowBackground, pickerStyle, tag, tint } from "@expo/ui/swift-ui/modifiers";
import { projectDefaultLabel, withCurrentOption, type ReviewerPickerProps } from "./reviewer-picker.types";
import { HarnessImage, useHarnessLogoUris } from "./spawn-composer-controls.ios";
import { geistFont } from "./swiftUIFont";
import { useTheme } from "./ThemeProvider";
import { useDeclinedChoice } from "./useDeclinedChoice";

/**
 * The reviewer and its model as menu pickers, rows of the sheet's Form: the
 * system draws the current value, the checkmark on it and the selection
 * announcement.
 */
export function ReviewerPicker({ reviewers, selectedReviewer, effectiveReviewer, onSelectReviewer, models, modelTitle, selectedModel, onSelectModel, busy }: ReviewerPickerProps) {
	const t = useTheme();
	const logoUris = useHarnessLogoUris(reviewers);
	const reviewerChoice = useDeclinedChoice(selectedReviewer, busy);
	const modelChoice = useDeclinedChoice(selectedModel, busy);
	const rowModifiers = [pickerStyle("menu"), tint(t.textSecondary), geistFont("body"), disabled(busy), listRowBackground(t.bgElevated)];
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
			{models.length ? (
				<Picker
					key={`model-${modelChoice.revision}`}
					label={modelTitle}
					selection={selectedModel}
					onSelectionChange={(id: string) => { modelChoice.choose(id); onSelectModel(id); }}
					modifiers={[...rowModifiers, accessibilityIdentifier("review-model")]}
				>
					<Text modifiers={[tag("")]}>Provider default</Text>
					{withCurrentOption(models, selectedModel).map((model) => <Text key={model.id} modifiers={[tag(model.id)]}>{model.label}</Text>)}
				</Picker>
			) : null}
		</>
	);
}
