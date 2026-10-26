import { Host } from "@expo/ui";
import { Button, Form, HStack, Image, Label, ProgressView, Section, Spacer, Text, Toggle, VStack } from "@expo/ui/swift-ui";
import { accessibilityHint, disabled, foregroundStyle, italic, lineLimit, listRowBackground, scrollContentBackground, textSelection, tint } from "@expo/ui/swift-ui/modifiers";
import { StyleSheet, View } from "react-native";
import { ItemActionsPullDown } from "./item-actions-menu.ios";
import type { ReviewActionsSheetProps, ReviewFeedbackItem, ReviewPolicyRow } from "./review-actions-sheet.types";
import { ReviewerPicker } from "./reviewer-picker.ios";
import { symbols } from "./symbols";
import { geistFont } from "./swiftUIFont";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles, useThemeState } from "./ThemeProvider";
import { sheetInset, space } from "./tokens";
import { SheetHeader } from "./ui";
import { useAnnounceAlerts } from "./useAnnounceAlerts";

const secondary = foregroundStyle({ type: "hierarchical", style: "secondary" });

/**
 * The review actions sheet as a SwiftUI Form: switches, menu pickers and
 * button rows in grouped sections, with each section's hint as its footer.
 */
export function ReviewActionsSheet({ subtitle, alerts, policies, locked, onPolicyChange, reviewer, externalReviewers, onRerequest, feedback, emptyFeedback }: ReviewActionsSheetProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	useAnnounceAlerts(alerts);
	// Rows sit on the palette's raised surface, as the app's cards do, so a named
	// theme reads as one surface with the sheet around it.
	const row = listRowBackground(t.bgElevated);
	return (
		<View style={styles.screen}>
			<View style={styles.header}><SheetHeader title="Review actions" subtitle={subtitle} /></View>
			<Host style={styles.form} colorScheme={scheme}>
				<Form modifiers={[scrollContentBackground("hidden")]}>
					{alerts.length ? (
						<Section>
							{alerts.map((alert) => <Label key={alert} title={alert} systemImage={symbols.warning.ios} modifiers={[geistFont("subheadline"), foregroundStyle(t.red), row]} />)}
						</Section>
					) : null}
					<Section title="Automation" footer={<Text>The same settings as this session's inspector on desktop.</Text>}>
						{policies.map((policy) => <PolicyToggle key={policy.key} policy={policy} locked={locked} onChange={onPolicyChange} />)}
					</Section>
					<Section title="Orchestrator reviewer" footer={<Text>Choose who runs the next Orchestrator review and which model it uses.</Text>}>
						{reviewer
							? <ReviewerPicker {...reviewer} />
							: <Text modifiers={[geistFont("subheadline"), secondary, row]}>No reviewer agents are available. Install or sign in to an agent on desktop.</Text>}
					</Section>
					{externalReviewers.length ? (
						<Section title="External reviewers" footer={<Text>Ask a GitHub reviewer to look at the latest changes again.</Text>}>
							{externalReviewers.map((item) => (
								<Button key={item.id} onPress={() => onRerequest(item.id)} modifiers={[tint(t.accent), disabled(locked), accessibilityHint("Asks them to review the latest changes again"), row]}>
									<HStack spacing={space.md}>
										<Image systemName={symbols.refresh.ios} />
										<VStack alignment="leading" spacing={space.hair}>
											<Text modifiers={[geistFont("body")]}>{item.id}</Text>
											<Text modifiers={[geistFont("footnote"), secondary]}>{item.detail}</Text>
										</VStack>
										<Spacer />
										{/* The request runs in place and opens nothing, so the row ends in progress rather than a disclosure. */}
										{item.busy ? <ProgressView /> : null}
									</HStack>
								</Button>
							))}
						</Section>
					) : null}
					{feedback.map((section) => (
						<Section key={section.id} title={section.title} footer={<Text>{section.footer}</Text>}>
							{section.items.map((item) => <FeedbackRow key={item.key} item={item} locked={locked} />)}
						</Section>
					))}
					{emptyFeedback ? <Section><Text modifiers={[geistFont("subheadline"), secondary, row]}>{emptyFeedback}</Text></Section> : null}
				</Form>
			</Host>
		</View>
	);
}

function PolicyToggle({ policy, locked, onChange }: { policy: ReviewPolicyRow; locked: boolean; onChange: (key: ReviewPolicyRow["key"], value: boolean) => void }) {
	const t = useTheme();
	const row = listRowBackground(t.bgElevated);
	const title = <Text modifiers={[geistFont("body")]}>{policy.title}</Text>;
	const description = <Text modifiers={[geistFont("subheadline"), secondary]}>{policy.description}</Text>;
	// While the session loads or the change saves, a spinner holds the switch's place.
	if (policy.busy) {
		return (
			<HStack modifiers={[row]}>
				<VStack alignment="leading" spacing={space.hair}>{title}{description}</VStack>
				<Spacer />
				<ProgressView />
			</HStack>
		);
	}
	// No tint: the switch keeps the system's green, which the hue-less accent would wash out.
	return (
		<Toggle isOn={policy.value} onIsOnChange={(value) => onChange(policy.key, value)} modifiers={[disabled(locked), row]}>
			{title}
			{description}
		</Toggle>
	);
}

function FeedbackRow({ item, locked }: { item: ReviewFeedbackItem; locked: boolean }) {
	const t = useTheme();
	return (
		<VStack alignment="leading" spacing={space.xs} modifiers={[listRowBackground(t.bgElevated)]}>
			<HStack spacing={space.sm}>
				<Text modifiers={[geistFont("subheadline", { weight: "600" }), lineLimit(1)]}>{item.author}</Text>
				<Spacer />
				<Text modifiers={[geistFont("footnote"), secondary, lineLimit(1)]}>{item.meta}</Text>
				{item.working ? <ProgressView /> : <ItemActionsPullDown actions={item.actions} accessibilityLabel={item.menuLabel} disabled={locked} tintColor={t.textSecondary} />}
			</HStack>
			{item.notInjected ? <Text modifiers={[geistFont("footnote"), foregroundStyle(t.amber)]}>Not automatically sent to the worker</Text> : null}
			{item.location ? <Text modifiers={[geistFont("footnote", { mono: true }), secondary]}>{item.location}</Text> : null}
			<Text modifiers={item.bodyMissing ? [geistFont("subheadline"), secondary, italic()] : [geistFont("subheadline"), textSelection(true)]}>{item.body}</Text>
			{item.sent ? <Label title="Sent to worker" systemImage={symbols.checkmark.ios} modifiers={[geistFont("footnote", { weight: "600" }), foregroundStyle(t.green)]} /> : null}
		</VStack>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgSurface },
	header: { paddingHorizontal: sheetInset.horizontal, paddingTop: sheetInset.top },
	form: { flex: 1 },
});
