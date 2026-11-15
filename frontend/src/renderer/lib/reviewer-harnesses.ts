import type { components } from "../../api/schema";

export type ReviewerHarnessId = NonNullable<components["schemas"]["TriggerReviewRequest"]["harness"]>;

const REVIEWER_HARNESS_IDS = [
	"agy",
	"aider",
	"amp",
	"auggie",
	"autohand",
	"claude-code",
	"codex",
	"cline",
	"copilot",
	"crush",
	"cursor",
	"devin",
	"droid",
	"grok",
	"kilocode",
	"kiro",
	"kimi",
	"kimchi",
	"muse",
	"opencode",
	"opencode-v2",
	"pi",
] as const satisfies readonly ReviewerHarnessId[];

type UnlistedReviewerHarness = Exclude<ReviewerHarnessId, (typeof REVIEWER_HARNESS_IDS)[number]>;
const _everyReviewerHarnessIsListed: UnlistedReviewerHarness extends never ? true : never = true;
void _everyReviewerHarnessIsListed;

export const KNOWN_REVIEWER_HARNESS_IDS: ReadonlySet<string> = new Set(REVIEWER_HARNESS_IDS);

export const WORKER_DEFAULT_REVIEWERS: Readonly<Partial<Record<string, ReviewerHarnessId>>> = {
	"claude-code": "claude-code",
	codex: "codex",
	opencode: "opencode",
	muse: "muse",
	kimchi: "kimchi",
};

export function toReviewerHarnessId(value?: string): ReviewerHarnessId | undefined {
	return value && KNOWN_REVIEWER_HARNESS_IDS.has(value) ? (value as ReviewerHarnessId) : undefined;
}

const CLOUD_REVIEWER_HARNESS_IDS: ReadonlySet<string> = new Set(["claude-code", "codex", "cursor"]);

export function defaultReviewerHarness(workerHarness: string, cloud = false): ReviewerHarnessId {
	const mapped = WORKER_DEFAULT_REVIEWERS[workerHarness] ?? "claude-code";
	return cloud && !CLOUD_REVIEWER_HARNESS_IDS.has(mapped) ? "claude-code" : mapped;
}
