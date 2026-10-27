/**
 * AO's automation messages and worker reports, each drawn as what it is, the
 * way the desktop chat draws them:
 *
 * - What the user did through AO (file feedback, a diff selection, a git button,
 *   browser annotations) is the user's own bubble: their words under a chip
 *   naming what they pointed at.
 * - What GitHub reported on the session's PR is a row in the PR glyph, tinted by
 *   what it means, with the detail beneath.
 * - AO's own notices are one quiet line.
 * - A worker's report is a quiet row (agent, task, state, headline, time) that
 *   opens to the full text.
 *
 * Text that matches none of them keeps the generic card.
 */

import { router } from "expo-router";
import { createContext, memo, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { Pressable, ScrollView, StyleSheet, Text, View, type StyleProp, type TextStyle } from "react-native";
import { AgentLogo } from "../AgentLogo";
import type { DashboardSession } from "../api";
import type { ServerConfig } from "../config";
import { haptics } from "../haptics";
import { sessionHostId } from "../hostedRows";
import { Feather, type FeatherIconName } from "../icons";
import { openGitHub } from "../openGitHub";
import { useApp } from "../store";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { font, prose, radius, space, type } from "../tokens";
import { useFilesNavigation } from "../files/navigation";
import { userMessageSurfaceStyle } from "./chatChrome";
import { ChatMarkdown } from "./ChatMarkdown";
import { readSession, sessionReadKey } from "./sessionLookup";
import {
	artifactEntry,
	automationOf,
	forgeURL,
	GIT_ACTION_LABELS,
	inlineRuns,
	NOTICE_LABELS,
	reportBlocks,
	safeLink,
	safeMarkdown,
	splitArtifacts,
	splitHeadline,
	workerUpdatesOf,
	type Automation,
	type BrowserFeedback,
	type FailedCheck,
	type FileFeedback,
	type GitAction,
	type PullRequestRef,
	type WorkerUpdate,
} from "./automationMessages";
import type { ConversationItem } from "./types";

/** `HH:MM` in the device's zone, written by hand: Hermes' Intl is partial. */
export function clockTime(iso: string): string {
	const date = new Date(iso);
	if (Number.isNaN(date.getTime())) return "";
	return `${String(date.getHours()).padStart(2, "0")}:${String(date.getMinutes()).padStart(2, "0")}`;
}

/** Keys of each worker's newest report; an older "needs you" has been overtaken. */
export const LatestWorkerUpdates = createContext<ReadonlySet<string> | null>(null);

/* -------------------------------------------------------------------------- */
/* entry points                                                                */
/* -------------------------------------------------------------------------- */

/**
 * A non-human user message: a worker's report or message, an automation drawn
 * as what it is, or `fallback` when AO cannot tell what it is.
 */
export function OriginMessageView({ message, fallback }: { message: ConversationItem; fallback: ReactNode }) {
	const updates = workerUpdatesOf(message);
	if (updates.length > 0) {
		// A direct message (no state) reads as prose from that agent; reports are quiet rows.
		if (updates.length === 1 && !updates[0]!.state) return <FromAgent update={updates[0]!} />;
		return <WorkerUpdateRows updates={updates} />;
	}
	const automation = automationOf(message);
	if (!automation) return <>{fallback}</>;
	if (automation.kind === "notice") return <NoticeLine label={NOTICE_LABELS[automation.notice]} />;
	if ("pr" in automation) return <PullRequestEvent automation={automation} createdAt={message.createdAt} />;
	return <YourAction action={automation} />;
}

/* -------------------------------------------------------------------------- */
/* worker reports                                                              */
/* -------------------------------------------------------------------------- */

const STATES: Record<string, { label: string; attention?: boolean }> = {
	needs_input: { label: "needs you", attention: true },
	stuck: { label: "stuck", attention: true },
	done: { label: "done" },
};

type WorkerDirectory = { sessions: ReadonlyMap<string, DashboardSession>; config: ServerConfig | null };
const WorkerSessions = createContext<WorkerDirectory | null>(null);

// What a row reads from a session; a poll that changes nothing here changes no row.
const directoryEntry = (session: DashboardSession) =>
	[session.id, session.projectId, session.harness ?? "", session.displayName ?? "", session.kind ?? "", sessionHostId(session) ?? ""].join("\u0000");

/**
 * The sessions and pairing the worker rows read, taken from the store once for
 * the whole timeline. The store changes on every poll, notification and
 * connection tick; reading it here, and handing rows a value that only changes
 * when something they show changes, keeps settled rows from redrawing.
 */
export function WorkerSessionsProvider({ children }: { children: ReactNode }) {
	const { sessions, config } = useApp();
	const signature = sessions.map(directoryEntry).join("\n");
	// Keyed on the signature, not the array: a poll returns a new array with the same sessions.
	const directory = useMemo(() => new Map(sessions.map((session) => [session.id, session])), [signature]);
	const value = useMemo(() => ({ sessions: directory, config }), [directory, config]);
	return <WorkerSessions.Provider value={value}>{children}</WorkerSessions.Provider>;
}

function useWorkerDirectory(): WorkerDirectory {
	const directory = useContext(WorkerSessions);
	if (!directory) throw new Error("Worker rows render inside a WorkerSessionsProvider");
	return directory;
}

/** The agent this chat runs, the logo a worker gets when its own session cannot be read. */
export const ConversationHarness = createContext<string | undefined>(undefined);


/**
 * The worker's session: from the live board, or read once from the daemon when the
 * board leaves it out (a daemon that pages its archive omits finished workers,
 * and those are most of the workers that report).
 */
function useSession(sessionId: string | undefined): DashboardSession | undefined {
	const { sessions, config } = useWorkerDirectory();
	const live = sessionId ? sessions.get(sessionId) : undefined;
	const key = config && sessionId && !live ? sessionReadKey(config, sessionId) : undefined;
	const [read, setRead] = useState<{ key: string; session: DashboardSession | null }>();
	useEffect(() => {
		if (!key || !config || !sessionId) return;
		let current = true;
		void readSession(config, sessionId).then((session) => { if (current) setRead({ key, session }); });
		return () => { current = false; };
	}, [config, key, sessionId]);
	return live ?? (read && read.key === key ? read.session ?? undefined : undefined);
}

/** The worker's harness mark; until its session is read, or if it cannot be, the chat's own agent. */
function WorkerLogo({ session }: { session: DashboardSession | undefined }) {
	const fallback = useContext(ConversationHarness);
	return <AgentLogo harness={session?.harness || fallback} size={16} />;
}

function useOpenSession(update: Pick<WorkerUpdate, "sessionId" | "projectId">) {
	const { config } = useWorkerDirectory();
	const session = useSession(update.sessionId);
	const projectId = update.projectId ?? session?.projectId;
	if (!update.sessionId || !projectId) return undefined;
	const id = update.sessionId;
	return () => {
		haptics.tap();
		router.push({ pathname: "/session/[id]", params: { id, projectId, hostId: (session && sessionHostId(session)) ?? config?.hostId } });
	};
}

/**
 * Opens a link in text an agent or a reviewer wrote, when `safeLink` allows it: a
 * session's chat for `ao://sessions/…`, the in-app browser for a forge page.
 */
function useOpenSafeLink() {
	const { config, sessions } = useWorkerDirectory();
	return (href: string) => {
		const target = safeLink(href);
		if (!target) return;
		haptics.tap();
		if (target.kind === "web") {
			void openGitHub(target.href);
			return;
		}
		const session = sessions.get(target.sessionId);
		router.push({ pathname: "/session/[id]", params: { id: target.sessionId, projectId: target.projectId, hostId: (session && sessionHostId(session)) ?? config?.hostId } });
	};
}

/** One line of a report: its links, code and emphasis drawn, only safe links live. */
function ReportInline({ text, style }: { text: string; style: StyleProp<TextStyle> }) {
	const styles = useThemedStyles(makeStyles);
	const openSafeLink = useOpenSafeLink();
	return (
		<Text style={style}>
			{inlineRuns(text).map((run, index) => {
				const target = run.href ? safeLink(run.href) : undefined;
				if (target) {
					// A label that is not its own URL could name any page; the host says where it goes.
					const host = target.kind === "web" && run.text !== run.href ? <Text style={styles.linkHost}>{` (${target.host})`}</Text> : null;
					return <Text key={index}><Text accessibilityRole="link" onPress={() => openSafeLink(run.href!)} suppressHighlighting style={styles.link}>{run.text}</Text>{host}</Text>;
				}
				if (run.code) return <Text key={index} style={styles.inlineCode}>{run.text}</Text>;
				if (run.strong) return <Text key={index} style={styles.strong}>{run.text}</Text>;
				return run.text;
			})}
		</Text>
	);
}

/** A report's prose at the row's quiet size: headings, list items and code fences drawn as blocks. */
function ReportText({ text, style }: { text: string; style: StyleProp<TextStyle> }) {
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.reportBlocks}>
			{reportBlocks(text).map((block, index) => {
				switch (block.kind) {
					case "heading":
						return <ReportInline key={index} text={block.text} style={[style, styles.strong]} />;
					case "item":
						return (
							<View key={index} style={styles.reportItem}>
								<Text style={[style, styles.reportMarker]}>{block.marker}</Text>
								<View style={styles.shrink}><ReportInline text={block.text} style={style} /></View>
							</View>
						);
					case "code":
						return (
							<ScrollView key={index} horizontal style={styles.reportCode}>
								<Text selectable style={styles.logText}>{block.text}</Text>
							</ScrollView>
						);
					default:
						return <ReportInline key={index} text={block.text} style={style} />;
				}
			})}
		</View>
	);
}

export function WorkerUpdateRows({ updates }: { updates: readonly WorkerUpdate[] }) {
	const styles = useThemedStyles(makeStyles);
	return <View style={styles.updates}>{updates.map((update) => <WorkerUpdateRow key={update.key} update={update} />)}</View>;
}

/**
 * One worker report, a quiet row like the desktop's. Tapping anywhere on it opens
 * the full report beneath, with its links live and a way into the worker's chat;
 * its artifacts open in a viewer.
 */
const WorkerUpdateRow = memo(function WorkerUpdateRow({ update }: { update: WorkerUpdate }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [open, setOpen] = useState(false);
	const session = useSession(update.sessionId);
	const openSession = useOpenSession(update);
	const latest = useContext(LatestWorkerUpdates);
	const { text, artifacts } = splitArtifacts(update.body);
	const { headline, detail } = splitHeadline(text);
	const state = update.state ? STATES[update.state] : undefined;
	const attention = state?.attention && (!latest || latest.has(update.key));
	// An older report names its sender only by session id; the session supplies its title.
	const name = update.name === update.sessionId ? session?.displayName || update.name : update.name;
	// The headline is a cut of the report. A short first line is cut off whole, so
	// opened, the rest follows it; a first sentence is not, so opened, the report
	// replaces it rather than repeating it.
	const sentence = detail === text;
	const more = detail && detail !== headline ? detail : "";
	return (
		<View testID="worker-update">
			<Pressable
				accessibilityRole="button"
				accessibilityState={{ expanded: open }}
				accessibilityLabel={[name, state?.label, headline].filter(Boolean).join(", ")}
				onPress={() => { haptics.tap(); setOpen((value) => !value); }}
				style={({ pressed }) => [styles.updateRow, pressed && styles.pressed]}
			>
				<View style={styles.updateTop}>
					<View style={styles.lead}><WorkerLogo session={session} /></View>
					<Text numberOfLines={1} style={styles.updateName}>{name}</Text>
					{state ? <Text style={[styles.updateState, attention && styles.updateAttention]}>{state.label}</Text> : null}
					<View style={styles.spacer} />
					<Text style={styles.time}>{clockTime(update.createdAt)}</Text>
					<Feather name={open ? "chevron-down" : "chevron-right"} size={12} color={t.textFaint} />
				</View>
				{headline && !(open && sentence && more) ? <Text numberOfLines={open ? undefined : 2} style={styles.updateHeadline}>{headline}</Text> : null}
			</Pressable>
			{artifacts.map((path) => <ArtifactLine key={path} path={path} update={update} hostId={session ? sessionHostId(session) : undefined} />)}
			{open ? (
				<View style={styles.opened}>
					{/* Muted and small, as the desktop opens it: the row stays a quiet aside. */}
					{more ? <ReportText text={more} style={styles.updateDetail} /> : null}
					{openSession ? (
						<Pressable accessibilityRole="button" hitSlop={8} onPress={openSession} style={({ pressed }) => [styles.openChat, pressed && styles.pressed]}>
							<Text style={styles.openChatText}>Open chat</Text>
							<Feather name="chevron-right" size={12} color={t.textSecondary} />
						</Pressable>
					) : null}
				</View>
			) : null}
		</View>
	);
});

/** A file the report attached, opened in the artifact viewer when it sits in the worker's artifact folder. */
function ArtifactLine({ path, update, hostId }: { path: string; update: WorkerUpdate; hostId?: string }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { config } = useWorkerDirectory();
	const entry = artifactEntry(path, update.sessionId);
	const name = path.split("/").at(-1);
	const content = (
		<>
			<Feather name="file-text" size={12} color={t.textTertiary} />
			<Text numberOfLines={1} style={[styles.artifactName, entry ? styles.artifactLink : undefined]}>{name}</Text>
		</>
	);
	if (!entry || !update.sessionId) return <View style={styles.artifact}>{content}</View>;
	const sessionId = update.sessionId;
	return (
		<Pressable
			accessibilityRole="link"
			accessibilityLabel={`Open ${name}`}
			onPress={() => { haptics.tap(); router.push({ pathname: "/artifact/[sessionId]", params: { sessionId, entry, hostId: hostId ?? config?.hostId } }); }}
			style={({ pressed }) => [styles.artifact, pressed && styles.pressed]}
		>
			{content}
		</Pressable>
	);
}

/** In a worker's chat, a message another agent sent it: who, when, and what it said. */
function FromAgent({ update }: { update: WorkerUpdate }) {
	const styles = useThemedStyles(makeStyles);
	const session = useSession(update.sessionId);
	const openSession = useOpenSession(update);
	const name = session?.kind === "orchestrator" ? "Orchestrator" : session?.displayName || update.name;
	return (
		<View style={styles.event} testID="agent-message">
			<View style={styles.lead}><WorkerLogo session={session} /></View>
			<View style={styles.eventBody}>
				<View style={styles.eventHeading}>
					<Text numberOfLines={1} onPress={openSession} suppressHighlighting style={[styles.headingText, styles.strong]}>{name}</Text>
					<Text style={styles.time}>{clockTime(update.createdAt)}</Text>
				</View>
				<Folded><ChatMarkdown text={safeMarkdown(update.body)} /></Folded>
			</View>
		</View>
	);
}

/* -------------------------------------------------------------------------- */
/* pull request events                                                         */
/* -------------------------------------------------------------------------- */

type PullRequestAutomation = Extract<Automation, { pr: PullRequestRef }>;

function openForge(url: string | undefined) {
	const href = forgeURL(url);
	if (!href) return undefined;
	return () => { haptics.tap(); void openGitHub(href); };
}

/** What GitHub reported on the session's PR, in the PR glyph tinted by what it means. */
function PullRequestEvent({ automation, createdAt }: { automation: PullRequestAutomation; createdAt: string }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { pr } = automation;
	const prLink = <Text accessibilityRole={forgeURL(pr.url) ? "link" : undefined} onPress={openForge(pr.url)} suppressHighlighting style={styles.strong}>#{pr.number}</Text>;
	let tone = t.amber;
	let heading: ReactNode;
	let detail: ReactNode = null;
	switch (automation.kind) {
		case "ciFailing":
			tone = t.red;
			heading = <><Text style={styles.strong}>CI failing</Text> on {prLink}</>;
			detail = automation.checks.map((check, index) => <FailedCheckLine key={index} check={check} />);
			break;
		case "reviewComments": {
			const authors = [...new Set(automation.comments.map((comment) => `@${comment.author}`))];
			heading = <><Text style={styles.strong}>{authors.join(", ")}</Text> commented on {prLink}</>;
			detail = automation.comments.map((comment, index) => (
				<View key={index} style={styles.detailBlock}>
					{comment.path || authors.length > 1 ? (
						<View style={styles.chipRow}>
							{authors.length > 1 ? <Text style={[styles.headingText, styles.strong]}>@{comment.author}</Text> : null}
							{comment.path ? <FileChip path={comment.path} line={comment.line} /> : null}
						</View>
					) : null}
					<Folded><ChatMarkdown text={safeMarkdown(comment.body)} /></Folded>
				</View>
			));
			break;
		}
		case "changesRequested":
			heading = <><Text style={styles.strong}>{automation.author ? `@${automation.author}` : "AO reviewer"}</Text> requested changes on {prLink}</>;
			detail = automation.body ? <Folded><ChatMarkdown text={safeMarkdown(automation.body)} /></Folded> : null;
			break;
		case "mergeConflict":
			// The PR cannot merge until the agent rebases: a failure, like failing CI.
			tone = t.red;
			heading = <><Text style={styles.strong}>Merge conflicts</Text> on {prLink}</>;
			break;
	}
	return (
		<View style={styles.event} testID="pull-request-event">
			<View style={styles.lead}><Feather name="git-pull-request" size={15} color={tone} /></View>
			<View style={styles.eventBody}>
				<View style={styles.eventHeading}>
					<Text numberOfLines={2} style={styles.headingText}>
						{heading}
						{pr.title ? <Text style={styles.prTitle}>{"  "}{pr.title}</Text> : null}
					</Text>
					<Text style={styles.time}>{clockTime(createdAt)}</Text>
				</View>
				{detail ? <View style={styles.detail}>{detail}</View> : null}
			</View>
		</View>
	);
}

function FailedCheckLine({ check }: { check: FailedCheck }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [open, setOpen] = useState(false);
	return (
		<View>
			<View style={styles.chipRow}>
				<Feather name="x-circle" size={12} color={t.red} />
				{/* A Pressable, not a pressable Text, so the check name gets a finger-sized target. */}
				<Pressable accessibilityRole={forgeURL(check.url) ? "link" : undefined} disabled={!forgeURL(check.url)} hitSlop={10} onPress={openForge(check.url)} style={({ pressed }) => [styles.shrink, pressed && styles.pressed]}>
					<Text numberOfLines={1} style={[styles.headingText, styles.strong]}>{check.name}</Text>
				</Pressable>
				{check.log ? (
					<Pressable accessibilityRole="button" accessibilityState={{ expanded: open }} hitSlop={8} onPress={() => { haptics.tap(); setOpen((value) => !value); }}>
						<Text style={styles.toggle}>{open ? "Hide log" : "Show log"}</Text>
					</Pressable>
				) : null}
			</View>
			{open && check.log ? (
				<ScrollView nestedScrollEnabled style={styles.log}>
					<ScrollView horizontal nestedScrollEnabled><Text selectable style={styles.logText}>{check.log}</Text></ScrollView>
				</ScrollView>
			) : null}
		</View>
	);
}

/* -------------------------------------------------------------------------- */
/* the user's own actions                                                      */
/* -------------------------------------------------------------------------- */

type UserAction = Extract<Automation, { kind: "fileFeedback" | "diffSelection" | "gitAction" | "browserFeedback" }>;

/** Something the user did through AO, drawn as the user's own message. */
function YourAction({ action }: { action: UserAction }) {
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.userRow} testID="your-action">
			<View style={styles.userBubble}><UserActionBody action={action} /></View>
		</View>
	);
}

function UserActionBody({ action }: { action: UserAction }) {
	const styles = useThemedStyles(makeStyles);
	switch (action.kind) {
		case "fileFeedback":
			return <FileFeedbackBody comments={action.comments} />;
		case "diffSelection":
			return (
				<View style={styles.actionStack}>
					<FileChip path={action.path} line={action.start} end={action.end} />
					{action.instruction ? <Text style={styles.userText}>{action.instruction}</Text> : null}
				</View>
			);
		case "gitAction":
			return <GitActionBody action={action.action} base={action.base} />;
		case "browserFeedback":
			return <BrowserFeedbackBody annotations={action.annotations} />;
	}
}

function FileChip({ path, line, end }: { path: string; line?: number; end?: number }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const files = useFilesNavigation();
	const name = path.slice(path.lastIndexOf("/") + 1);
	const at = line ? (end && end !== line ? `:${line}–${end}` : `:${line}`) : "";
	return (
		<Pressable
			accessibilityRole={files ? "button" : undefined}
			accessibilityLabel={`Open ${path}`}
			disabled={!files}
			onPress={() => { haptics.tap(); files?.openFile(path); }}
			style={({ pressed }) => [styles.chip, pressed && styles.pressed]}
		>
			<Feather name="file-text" size={12} color={t.textTertiary} />
			<Text numberOfLines={1} style={styles.chipText}>{name}{at}</Text>
		</Pressable>
	);
}

function FileFeedbackBody({ comments }: { comments: readonly FileFeedback[] }) {
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.feedbackList}>
			{comments.map((comment, index) => (
				<View key={index} style={styles.actionStack}>
					<FileChip path={comment.path} line={comment.line} />
					{comment.code || comment.quote ? (
						<Text numberOfLines={1} style={styles.codeLine}>{comment.code ?? comment.quote}</Text>
					) : comment.wholeFile ? (
						<Text style={styles.muted}>Entire file</Text>
					) : null}
					{comment.feedback ? <Text style={styles.userText}>{comment.feedback}</Text> : null}
				</View>
			))}
		</View>
	);
}

const GIT_ICONS: Record<GitAction, FeatherIconName> = {
	createPR: "git-pull-request",
	createDraftPR: "git-pull-request-draft",
	commit: "git-commit",
	commitPush: "git-commit",
	push: "arrow-up-from-line",
};

function GitActionBody({ action, base }: { action: GitAction; base?: string }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.chipRow}>
			<Feather name={GIT_ICONS[action]} size={14} color={t.textTertiary} />
			<Text style={styles.userText}>{GIT_ACTION_LABELS[action]}</Text>
			{base ? (
				<>
					<Feather name="chevron-right" size={12} color={t.textTertiary} />
					<Text numberOfLines={1} style={[styles.chipText, styles.shrink]}>{base}</Text>
				</>
			) : null}
		</View>
	);
}

function BrowserFeedbackBody({ annotations }: { annotations: BrowserFeedback }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const shots = annotations.screenshotCount;
	return (
		<View style={styles.actionStack}>
			<View style={styles.chip}>
				<Feather name="mouse-pointer" size={12} color={t.textTertiary} />
				<Text numberOfLines={1} style={[styles.chipText, { fontFamily: font.regular }]}>{annotations.pageTitle}</Text>
			</View>
			{annotations.items.map((item) => (
				<View key={item.number} style={styles.annotation}>
					<Text style={styles.annotationNumber}>{item.number}</Text>
					<Text style={[styles.userText, styles.shrink]}>
						{item.comment || `${item.changes.length} visual ${item.changes.length === 1 ? "change" : "changes"}`}
						{item.target ? <Text style={styles.muted}>{"  "}{item.target}</Text> : null}
						{item.text ? <Text style={styles.muted}>{"  "}{item.text}</Text> : null}
					</Text>
				</View>
			))}
			{shots > 0 ? <Text style={styles.muted}>{shots} reference {shots === 1 ? "screenshot" : "screenshots"}</Text> : null}
		</View>
	);
}

/* -------------------------------------------------------------------------- */
/* notices and folding                                                         */
/* -------------------------------------------------------------------------- */

function NoticeLine({ label }: { label: string }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.notice} testID="automation-notice">
			<Feather name="info" size={13} color={t.textTertiary} />
			<Text style={[styles.muted, styles.shrink]}>{label}</Text>
		</View>
	);
}

// Five whole lines of ChatMarkdown prose: Android clips rather than ellipsizes, so a fold between lines never cuts one in half.
const FOLD_HEIGHT = prose.lineHeight * 5;

/** Content folded after five lines, with "Show more" whenever the fold hides something. */
function Folded({ children }: { children: ReactNode }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [open, setOpen] = useState(false);
	const [measure, setMeasure] = useState({ width: 0, height: 0 });
	const folds = measure.height > FOLD_HEIGHT + 4;
	const clipped = !open && folds;
	return (
		<View>
			{/* A new width (rotation, split view) can change the natural height; unclip once to measure it again. */}
			<View
				style={clipped ? styles.folded : undefined}
				onLayout={(event) => {
					const { width } = event.nativeEvent.layout;
					// The first width is the one the height was measured at (children lay out first); only a later change re-measures.
					setMeasure((current) => current.width === width ? current : { width, height: current.width === 0 ? current.height : 0 });
				}}
			>
				{/* Yoga lays the content out at the clip's height while clipped, so only an unclipped layout is its natural height. */}
				<View onLayout={(event) => {
					const { height } = event.nativeEvent.layout;
					if (!clipped) setMeasure((current) => current.height === height ? current : { ...current, height });
				}}>{children}</View>
			</View>
			{folds ? (
				<Pressable accessibilityRole="button" accessibilityState={{ expanded: open }} hitSlop={8} onPress={() => { haptics.tap(); setOpen((value) => !value); }} style={styles.more}>
					<Text style={styles.toggle}>{open ? "Show less" : "Show more"}</Text>
					<Feather name={open ? "chevron-up" : "chevron-down"} size={12} color={t.textTertiary} />
				</Pressable>
			) : null}
		</View>
	);
}

/* -------------------------------------------------------------------------- */
/* styles                                                                      */
/* -------------------------------------------------------------------------- */

const LEAD = 18;

const makeStyles = (t: Theme) => StyleSheet.create({
	pressed: { opacity: 0.6 },
	spacer: { flex: 1 },
	shrink: { flexShrink: 1 },
	lead: { width: LEAD, alignItems: "center", paddingTop: 1 },
	time: { fontFamily: font.regular, color: t.textFaint, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontVariant: ["tabular-nums"] },
	muted: { fontFamily: font.regular, color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	strong: { fontFamily: font.medium, color: t.textPrimary },
	toggle: { fontFamily: font.medium, color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },

	updates: { paddingVertical: space.xs },
	updateRow: { paddingVertical: space.xs, gap: space.hair },
	updateTop: { flexDirection: "row", alignItems: "center", gap: space.sm },
	updateName: { flexShrink: 1, fontFamily: font.medium, color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
	updateState: { fontFamily: font.regular, color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	updateAttention: { fontFamily: font.medium, color: t.amber },
	updateHeadline: { marginLeft: LEAD + space.sm, fontFamily: font.regular, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	artifact: { marginLeft: LEAD + space.sm, minHeight: 24, flexDirection: "row", alignItems: "center", gap: space.xs },
	artifactName: { flexShrink: 1, fontFamily: font.mono, color: t.textSecondary, fontSize: type.footnote.fontSize },
	opened: { marginLeft: LEAD + space.sm, paddingTop: space.xxs, paddingBottom: space.sm, gap: space.sm },
	openChat: { alignSelf: "flex-start", flexDirection: "row", alignItems: "center", gap: space.xxs },
	openChatText: { fontFamily: font.medium, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	artifactLink: { color: t.textPrimary, textDecorationLine: "underline", textDecorationColor: t.borderStrong },
	link: { color: t.textPrimary, textDecorationLine: "underline" },
	linkHost: { color: t.textTertiary },
	reportBlocks: { gap: space.xs },
	reportItem: { flexDirection: "row", gap: space.xs },
	reportMarker: { minWidth: 12, color: t.textTertiary },
	reportCode: { borderRadius: radius.sm, backgroundColor: t.bgSubtle, padding: space.sm },
	inlineCode: { fontFamily: font.mono, color: t.textPrimary },
	updateDetail: { fontFamily: font.regular, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },

	event: { flexDirection: "row", gap: space.sm, paddingVertical: space.md },
	eventBody: { flex: 1, minWidth: 0, gap: space.sm },
	eventHeading: { flexDirection: "row", alignItems: "flex-start", gap: space.sm },
	headingText: { flex: 1, fontFamily: font.regular, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	prTitle: { color: t.textTertiary },
	detail: { gap: space.md },
	detailBlock: { gap: space.xs },
	chipRow: { flexDirection: "row", alignItems: "center", gap: space.xs, minWidth: 0 },
	chip: { alignSelf: "flex-start", maxWidth: "100%", flexDirection: "row", alignItems: "center", gap: space.xxs, borderRadius: radius.xs, backgroundColor: t.bgSubtle, paddingHorizontal: space.xs, paddingVertical: space.hair },
	chipText: { flexShrink: 1, fontFamily: font.mono, color: t.textSecondary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight },
	log: { maxHeight: 256, marginTop: space.xs, borderRadius: radius.sm, backgroundColor: t.bgSubtle, padding: space.sm },
	logText: { fontFamily: font.mono, color: t.textSecondary, fontSize: type.caption2.fontSize, lineHeight: 16 },

	userRow: { alignItems: "flex-end", paddingTop: space.lg, paddingBottom: space.sm },
	userBubble: { maxWidth: "88%", backgroundColor: userMessageSurfaceStyle(t).backgroundColor, borderRadius: 20, borderCurve: "continuous", paddingHorizontal: space.lg, paddingVertical: space.md },
	userText: { fontFamily: font.regular, color: userMessageSurfaceStyle(t).foregroundColor, fontSize: type.callout.fontSize, lineHeight: type.callout.lineHeight },
	actionStack: { gap: space.xs },
	feedbackList: { gap: space.md },
	codeLine: { fontFamily: font.mono, color: t.textTertiary, fontSize: type.caption1.fontSize },
	annotation: { flexDirection: "row", alignItems: "baseline", gap: space.sm },
	annotationNumber: { fontFamily: font.regular, color: t.textTertiary, fontSize: type.footnote.fontSize, fontVariant: ["tabular-nums"] },

	notice: { flexDirection: "row", alignItems: "center", gap: space.sm, minHeight: 28, paddingVertical: space.xs },
	folded: { maxHeight: FOLD_HEIGHT, overflow: "hidden" },
	more: { alignSelf: "flex-start", flexDirection: "row", alignItems: "center", gap: space.xxs, paddingTop: space.xs },
});
