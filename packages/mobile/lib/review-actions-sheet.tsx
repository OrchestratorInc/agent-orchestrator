import { CircularProgressIndicator, Column, Host, Icon, ListItem, Switch, Text as ComposeText, type ListItemColors } from "@expo/ui/jetpack-compose";
import { alpha, clickable, size, testID, toggleable } from "@expo/ui/jetpack-compose/modifiers";
import type { ReactNode } from "react";
import { ScrollView, StyleSheet, Text, View } from "react-native";
import { AppSymbol } from "./AppSymbol";
import { rowDividerWidth } from "./divider";
import { ItemActionsMenu } from "./item-actions-menu";
import type { ExternalReviewerRow, ReviewActionsSheetProps, ReviewFeedbackItem, ReviewPolicyRow } from "./review-actions-sheet.types";
import { reviewDrawable } from "./review-drawables";
import { ReviewerPicker } from "./reviewer-picker";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles, useThemeState } from "./ThemeProvider";
import { font, iconSize, radius, space, stateOpacity, type } from "./tokens";
import { SHEET_SCROLL_CONTENT, SheetHeader } from "./ui";

/**
 * The review actions sheet on Android: Material list items with switches,
 * dropdown rows and request rows, inside the sheet's own scroll view. That
 * scroll view stays React Native because it is what cooperates with the
 * bottom sheet's drag; a Compose list there would fight it for every swipe.
 */
export function ReviewActionsSheet({ subtitle, alerts, policies, locked, onPolicyChange, reviewer, externalReviewers, onRerequest, feedback, emptyFeedback }: ReviewActionsSheetProps) {
	const styles = useThemedStyles(makeStyles);
	return (
		<ScrollView style={styles.screen} contentContainerStyle={SHEET_SCROLL_CONTENT} nestedScrollEnabled keyboardShouldPersistTaps="handled">
			<SheetHeader title="Review actions" subtitle={subtitle} />
			{alerts.map((alert) => <Text key={alert} accessibilityRole="alert" accessibilityLiveRegion="polite" style={styles.alert}>{alert}</Text>)}
			<Section title="Automation" footer="The same settings as this session's inspector on desktop.">
				<MaterialList>
					{policies.map((policy) => <PolicyItem key={policy.key} policy={policy} locked={locked} onChange={onPolicyChange} />)}
				</MaterialList>
			</Section>
			<Section title="Orchestrator reviewer" footer="Choose who runs the next Orchestrator review and which model it uses.">
				{reviewer
					? <MaterialList><ReviewerPicker {...reviewer} /></MaterialList>
					: <Text style={styles.empty}>No reviewer agents are available. Install or sign in to an agent on desktop.</Text>}
			</Section>
			{externalReviewers.length ? (
				<Section title="External reviewers" footer="Ask a GitHub reviewer to look at the latest changes again.">
					<MaterialList>
						{externalReviewers.map((item) => <RerequestItem key={item.id} item={item} locked={locked} onPress={() => onRerequest(item.id)} />)}
					</MaterialList>
				</Section>
			) : null}
			{feedback.map((section) => (
				<Section key={section.id} title={section.title} footer={section.footer}>
					<View style={styles.cards}>
						{section.items.map((item) => <FeedbackCard key={item.key} item={item} locked={locked} />)}
					</View>
				</Section>
			))}
			{emptyFeedback ? <Text style={styles.empty}>{emptyFeedback}</Text> : null}
		</ScrollView>
	);
}

function Section({ title, footer, children }: { title: string; footer: string; children: ReactNode }) {
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.section}>
			<Text accessibilityRole="header" style={styles.sectionTitle}>{title}</Text>
			{children}
			<Text style={styles.sectionFooter}>{footer}</Text>
		</View>
	);
}

/** A Compose column of list items, sized to its rows. */
function MaterialList({ children }: { children: ReactNode }) {
	const styles = useThemedStyles(makeStyles);
	const { scheme } = useThemeState();
	return (
		<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.list}>
			<Column>{children}</Column>
		</Host>
	);
}

function listColors(t: Theme): ListItemColors {
	return { containerColor: t.bgSurface, contentColor: t.textPrimary, supportingContentColor: t.textTertiary, leadingContentColor: t.textSecondary, trailingContentColor: t.textSecondary };
}

// A Compose `Text` style replaces the list item's own, so each names its step.
const headline = { typography: "bodyLarge", fontFamily: font.regular } as const;
const supporting = { typography: "bodyMedium", fontFamily: font.regular } as const;

/**
 * Material's switch row: a tap anywhere on the item toggles it, and TalkBack
 * reads the item as a switch with its title and description. @expo/ui's Switch
 * always takes its own taps, so TalkBack also stops on the switch itself and
 * reads only its state; @expo/ui has no way yet to fold it into the row.
 */
function PolicyItem({ policy, locked, onChange }: { policy: ReviewPolicyRow; locked: boolean; onChange: (key: ReviewPolicyRow["key"], value: boolean) => void }) {
	const t = useTheme();
	const interactive = !locked && !policy.busy;
	return (
		<ListItem colors={listColors(t)} modifiers={[...(interactive ? [toggleable(policy.value, () => onChange(policy.key, !policy.value), { role: "switch" })] : []), testID(`review-policy-${policy.key}`)]}>
			<ListItem.HeadlineContent><ComposeText style={headline}>{policy.title}</ComposeText></ListItem.HeadlineContent>
			<ListItem.SupportingContent><ComposeText style={supporting}>{policy.description}</ComposeText></ListItem.SupportingContent>
			<ListItem.TrailingContent>
				{policy.busy
					? <CircularProgressIndicator color={t.accent} strokeWidth={3} modifiers={[size(iconSize.xl, iconSize.xl)]} />
					: <Switch value={policy.value} enabled={interactive} onCheckedChange={(value) => onChange(policy.key, value)} />}
			</ListItem.TrailingContent>
		</ListItem>
	);
}

/** Asks a GitHub reviewer again. It acts in place, so it ends in a spinner while it runs, never a disclosure arrow. */
function RerequestItem({ item, locked, onPress }: { item: ExternalReviewerRow; locked: boolean; onPress: () => void }) {
	const t = useTheme();
	// While another action runs the row dims as a disabled one; the row that is
	// running keeps full strength so its spinner reads.
	const modifiers = locked ? (item.busy ? [] : [alpha(stateOpacity.disabled)]) : [clickable(onPress)];
	return (
		<ListItem colors={listColors(t)} modifiers={modifiers}>
			<ListItem.HeadlineContent><ComposeText style={headline}>{item.id}</ComposeText></ListItem.HeadlineContent>
			<ListItem.SupportingContent><ComposeText style={supporting}>{item.detail}</ComposeText></ListItem.SupportingContent>
			<ListItem.LeadingContent><Icon source={reviewDrawable("refresh")} size={iconSize.xl} contentDescription="Request another review" /></ListItem.LeadingContent>
			{item.busy ? (
				<ListItem.TrailingContent>
					<CircularProgressIndicator color={t.accent} strokeWidth={3} modifiers={[size(iconSize.xl, iconSize.xl)]} />
				</ListItem.TrailingContent>
			) : null}
		</ListItem>
	);
}

function FeedbackCard({ item, locked }: { item: ReviewFeedbackItem; locked: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.card}>
			<View style={styles.cardHeading}>
				<Text style={styles.author} numberOfLines={1}>{item.author}</Text>
				<Text style={styles.meta} numberOfLines={1}>{item.meta}</Text>
				<ItemActionsMenu accessibilityLabel={item.menuLabel} actions={item.actions} disabled={locked} loading={item.working} />
			</View>
			{item.notInjected ? <Text style={styles.notInjected}>Not automatically sent to the worker</Text> : null}
			{item.location ? <Text style={styles.location}>{item.location}</Text> : null}
			<Text selectable={!item.bodyMissing} style={item.bodyMissing ? styles.bodyMissing : styles.body}>{item.body}</Text>
			{item.sent ? (
				<View style={styles.sent}>
					<AppSymbol name="checkmark" size={iconSize.sm} color={t.green} />
					<Text style={styles.sentText}>Sent to worker</Text>
				</View>
			) : null}
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgSurface },
	alert: { ...type.footnote, color: t.red, backgroundColor: t.tintRed, borderRadius: radius.md, borderCurve: "continuous", padding: space.md, marginBottom: space.sm },
	section: { marginTop: space.xl, gap: space.xs },
	sectionTitle: { ...type.subheadline, fontFamily: font.semibold, fontWeight: "600", color: t.textSecondary },
	sectionFooter: { ...type.footnote, color: t.textTertiary },
	// Material list items carry their own 16dp inset; this lines their text up with the sheet's.
	list: { marginHorizontal: -space.lg },
	cards: { borderTopWidth: rowDividerWidth, borderTopColor: t.borderSubtle },
	card: { paddingVertical: space.md, gap: space.xs, borderBottomWidth: rowDividerWidth, borderBottomColor: t.borderSubtle },
	cardHeading: { flexDirection: "row", alignItems: "center", gap: space.sm },
	author: { ...type.subheadline, fontFamily: font.semibold, fontWeight: "600", flexShrink: 1, color: t.textPrimary },
	meta: { ...type.footnote, flex: 1, textAlign: "right", color: t.textTertiary },
	notInjected: { ...type.footnote, color: t.amber },
	location: { ...type.footnote, fontFamily: t.fontMono, color: t.textSecondary },
	body: { ...type.subheadline, color: t.textSecondary },
	bodyMissing: { ...type.subheadline, fontStyle: "italic", color: t.textTertiary },
	sent: { flexDirection: "row", alignItems: "center", gap: space.xs },
	sentText: { ...type.footnote, fontFamily: font.semibold, fontWeight: "600", color: t.green },
	empty: { ...type.footnote, color: t.textTertiary, paddingVertical: space.md },
});
