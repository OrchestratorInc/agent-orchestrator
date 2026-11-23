import { motion, useReducedMotion } from "motion/react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Columns2, GitBranch, GitPullRequest, ListChecks } from "@/components/app-icons";
import { IS_DEV } from "../../lib/is-dev";
import { cn } from "../../lib/utils";
import { AgentAvatar } from "../AgentAvatar";
import { OrchestratorIcon } from "../icons";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";

// The welcome page's copy, and the directions for it under discussion.
//
// "current" is what ships (translated in every locale). The others are an
// exploration the team is choosing between, so they exist in dev builds only,
// in English, behind the picker at the window's top right. Each one keeps the
// three beats the preview plays (the request, the workers at it, CI and
// review), so whichever is chosen stays in step with the drawing. Once one is
// picked it moves into the catalogs and the rest are deleted.

export type WelcomeVariantId = "current" | "steps" | "team" | "example" | "roles" | "questions";

const VARIANTS: ReadonlyArray<{ id: WelcomeVariantId; name: string }> = [
	{ id: "current", name: "Current" },
	{ id: "steps", name: "A · Numbered steps" },
	{ id: "team", name: "B · Meet the team" },
	{ id: "example", name: "C · Worked example" },
	{ id: "roles", name: "D · Who does what" },
	{ id: "questions", name: "E · Common questions" },
];

const STORAGE_KEY = "ao.dev.welcomeVariant";
/** Names the dev-only picker for assistive tech (not product copy). */
const PICKER_NAME = "Welcome copy direction";

/** How far the story's other parts recede while one plays. */
const INACTIVE_OPACITY = 0.35;
const EASE = [0.23, 1, 0.32, 1] as const;

function readVariant(): WelcomeVariantId {
	if (!IS_DEV) return "current";
	try {
		const stored = window.localStorage.getItem(STORAGE_KEY);
		return VARIANTS.some((variant) => variant.id === stored) ? (stored as WelcomeVariantId) : "current";
	} catch {
		return "current";
	}
}

/** The direction showing: always "current" outside dev builds. */
export function useWelcomeVariant(): [WelcomeVariantId, (next: WelcomeVariantId) => void] {
	const [variant, setVariant] = useState<WelcomeVariantId>(readVariant);
	const choose = (next: WelcomeVariantId) => {
		setVariant(next);
		try {
			window.localStorage.setItem(STORAGE_KEY, next);
		} catch {
			// Remembering the choice is a convenience.
		}
	};
	return [IS_DEV ? variant : "current", choose];
}

/** Dev builds: switches the welcome page between the directions, in place. */
export function WelcomeVariantPicker({ value, onChange }: { value: WelcomeVariantId; onChange: (next: WelcomeVariantId) => void }) {
	if (!IS_DEV) return null;
	return (
		<div className="fixed right-4 top-12 z-30">
			<Select value={value} onValueChange={(next) => onChange(next as WelcomeVariantId)}>
				<SelectTrigger size="sm" aria-label={PICKER_NAME} data-testid="welcome-variant-picker">
					<SelectValue />
				</SelectTrigger>
				<SelectContent align="end">
					{VARIANTS.map((variant) => (
						<SelectItem key={variant.id} value={variant.id}>{variant.name}</SelectItem>
					))}
				</SelectContent>
			</Select>
		</div>
	);
}

type WelcomeCopy = { title: string; body: ReactNode; story: ReactNode };

/** The welcome page's title, line under it, and the story list for `point`. */
export function useWelcomeCopy(variant: WelcomeVariantId, point: number): WelcomeCopy {
	const { t } = useTranslation();
	switch (variant) {
		case "steps":
			return {
				title: "Welcome to Orchestrator",
				body: "Orchestrator runs a team of coding agents on your code. You say what you want; it splits the work, runs it in parallel and brings back pull requests.",
				story: <NumberedSteps point={point} />,
			};
		case "team":
			return {
				title: "Meet your agent team",
				body: "Three parts you'll see everywhere in Orchestrator, and what each one does.",
				story: <TeamList point={point} />,
			};
		case "example":
			return {
				title: "One message, four pull requests",
				body: "Here's what happens when you ask for several things at once.",
				story: <WorkedExample point={point} message={t("firstRun.exampleMessage")} />,
			};
		case "roles":
			return {
				title: "You decide. Agents do the work.",
				body: "Orchestrator takes care of running the agents. You step in only for the calls that need a person.",
				story: <RolesTable point={point} />,
			};
		case "questions":
			return {
				title: t("firstRun.welcomeTitle"),
				body: t("firstRun.welcomeBody"),
				story: <QuestionList point={point} />,
			};
		default:
			return { title: t("firstRun.welcomeTitle"), body: t("firstRun.welcomeBody"), story: <CurrentStory point={point} /> };
	}
}

/** One part of the story: brought up while the preview plays it, the rest dimmed. */
function StoryItem({ active, className, children }: { active: boolean; className?: string; children: ReactNode }) {
	const reduceMotion = useReducedMotion();
	return (
		<motion.li
			className={className}
			aria-current={active ? "step" : undefined}
			initial={false}
			animate={{ opacity: active ? 1 : INACTIVE_OPACITY }}
			transition={reduceMotion ? { duration: 0 } : { duration: 0.4, ease: EASE }}
		>
			{children}
		</motion.li>
	);
}

function ItemText({ title, body }: { title: string; body: string }) {
	return (
		<>
			<span className="text-sm font-medium text-foreground">{title}</span>
			<span className="max-w-sm text-pretty text-sm leading-relaxed text-muted-foreground">{body}</span>
		</>
	);
}

// ── Current ─────────────────────────────────────────────────────────────────

const CURRENT_POINTS = [
	{ title: "firstRun.orchPoint1Title", body: "firstRun.orchPoint1Body" },
	{ title: "firstRun.orchPoint2Title", body: "firstRun.orchPoint2Body" },
	{ title: "firstRun.orchPointCiTitle", body: "firstRun.orchPointCiBody" },
] as const;

function CurrentStory({ point }: { point: number }) {
	const { t } = useTranslation();
	return (
		<ol className="flex flex-col gap-4 pt-6 pb-8">
			{CURRENT_POINTS.map(({ title, body }, i) => (
				<StoryItem key={title} active={i === point} className="flex flex-col gap-1">
					<ItemText title={t(title)} body={t(body)} />
				</StoryItem>
			))}
		</ol>
	);
}

// ── A · Numbered steps ──────────────────────────────────────────────────────

const STEPS = [
	{ title: "You describe the outcome", body: "One message is enough. The orchestrator turns it into separate tasks." },
	{ title: "Workers build in parallel", body: "Each task gets its own coding agent and its own copy of your code, so nothing collides." },
	{ title: "You review and merge", body: "Workers open pull requests. CI and code review run on GitHub, and the board shows what's ready." },
];

/** Numbered steps on a thin rail, the playing step's number filled. */
function NumberedSteps({ point }: { point: number }) {
	return (
		<ol className="flex flex-col pt-6 pb-8">
			{STEPS.map(({ title, body }, i) => {
				const active = i === point;
				const last = i === STEPS.length - 1;
				return (
					<StoryItem key={title} active={active} className={cn("relative grid grid-cols-[1.5rem_1fr] gap-x-3.5", !last && "pb-5")}>
						{!last ? <span aria-hidden="true" className="absolute bottom-1 left-3 top-8 w-px -translate-x-1/2 bg-border" /> : null}
						<span
							aria-hidden="true"
							className={cn(
								"grid size-6 place-items-center rounded-full border text-caption font-medium tabular-nums transition-colors duration-normal",
								active ? "border-transparent bg-foreground text-background" : "border-border text-muted-foreground",
							)}
						>
							{i + 1}
						</span>
						<span className="flex flex-col gap-1 pt-0.5">
							<ItemText title={title} body={body} />
						</span>
					</StoryItem>
				);
			})}
		</ol>
	);
}

// ── B · Meet the team ───────────────────────────────────────────────────────

const TEAM = [
	{ title: "Orchestrator", body: "The agent you talk to. It plans the work and starts a worker for each part." },
	{ title: "Workers", body: "Coding agents like Claude Code and Codex. Each one gets its own branch and worktree." },
	{ title: "Board", body: "Every worker's pull request, CI run and review in one place, sorted by what needs you." },
];

/** The app's three nouns, each with the mark it wears in the app. */
function TeamList({ point }: { point: number }) {
	const marks: ReactNode[] = [
		<OrchestratorIcon key="orchestrator" className="size-4" />,
		<span key="workers" className="flex -space-x-1">
			{/* Each mark in its own disc over the next, as the Tools step stacks them. */}
			{["claude-code", "codex"].map((provider, index) => (
				<span key={provider} className="relative grid size-4 place-items-center overflow-hidden rounded-full bg-background ring-2 ring-muted" style={{ zIndex: 2 - index }}>
					<AgentAvatar provider={provider} className="size-3" decorative />
				</span>
			))}
		</span>,
		<Columns2 key="board" className="size-4" />,
	];
	return (
		<ul className="flex flex-col gap-5 pt-6 pb-8">
			{TEAM.map(({ title, body }, i) => (
				<StoryItem key={title} active={i === point} className="flex gap-3.5">
					<span aria-hidden="true" className="grid size-8 shrink-0 place-items-center rounded-lg bg-muted text-foreground">
						{marks[i]}
					</span>
					<span className="flex flex-col gap-1 pt-1.5">
						<ItemText title={title} body={body} />
					</span>
				</StoryItem>
			))}
		</ul>
	);
}

// ── C · Worked example ──────────────────────────────────────────────────────

const EXAMPLE = [
	{ icon: ListChecks, title: "Split into four tasks", body: "The orchestrator plans the work and picks an agent for each part." },
	{ icon: GitBranch, title: "Four workers start at once", body: "Each one codes on its own branch, in its own copy of your code." },
	{ icon: GitPullRequest, title: "Pull requests come back checked", body: "CI and code review run on GitHub. Merge what's ready, answer what's blocked." },
];

/** The preview's own request as a chat bubble, then what came of it. */
function WorkedExample({ point, message }: { point: number; message: string }) {
	const reduceMotion = useReducedMotion();
	return (
		<div className="flex flex-col gap-5 pt-6 pb-8">
			<motion.p
				className="self-start rounded-xl rounded-bl-sm bg-muted px-3.5 py-2.5 text-pretty text-sm leading-relaxed text-foreground"
				initial={false}
				animate={{ opacity: point === 0 ? 1 : 0.6 }}
				transition={reduceMotion ? { duration: 0 } : { duration: 0.4, ease: EASE }}
			>
				{message}
			</motion.p>
			<ol className="flex flex-col gap-4">
				{EXAMPLE.map(({ icon: Icon, title, body }, i) => (
					<StoryItem key={title} active={i === point} className="flex gap-3">
						<Icon aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
						<span className="flex flex-col gap-1">
							<ItemText title={title} body={body} />
						</span>
					</StoryItem>
				))}
			</ol>
		</div>
	);
}

// ── D · Who does what ───────────────────────────────────────────────────────

const ROLES = [
	{ who: "You", what: "Describe what you want built or fixed, in plain words." },
	{ who: "Agents", what: "Plan it, split it up and write the code side by side, each on its own branch." },
	{ who: "You", what: "Review the pull requests once CI and code review pass. Merge, or answer a question." },
];

/** Who acts at each beat, as one shared list with hairline dividers. */
function RolesTable({ point }: { point: number }) {
	return (
		<ol className="mt-6 mb-8 flex flex-col">
			{ROLES.map(({ who, what }, i) => (
				<StoryItem key={what} active={i === point} className="grid grid-cols-[4.5rem_1fr] gap-x-4 border-border py-3.5 [&+&]:border-t">
					<span className="text-sm font-medium text-foreground">{who}</span>
					<span className="text-pretty text-sm leading-relaxed text-muted-foreground">{what}</span>
				</StoryItem>
			))}
		</ol>
	);
}

// ── E · Common questions ────────────────────────────────────────────────────

const QUESTIONS = [
	{ title: "What does it do?", body: "It turns one request into several tasks and puts a coding agent on each, all at the same time." },
	{ title: "Will it touch my checkout?", body: "No. Every agent works in its own git worktree on its own branch. Your checkout stays as it is." },
	{ title: "How does the work land?", body: "As pull requests. CI and code review run on GitHub before anything is merged." },
];

/** The three things people ask first, answered in the order the preview plays. */
function QuestionList({ point }: { point: number }) {
	return (
		<ul className="mt-6 mb-8 flex flex-col">
			{QUESTIONS.map(({ title, body }, i) => (
				<StoryItem key={title} active={i === point} className="flex flex-col gap-1 border-border py-3.5 [&+&]:border-t">
					<ItemText title={title} body={body} />
				</StoryItem>
			))}
		</ul>
	);
}
