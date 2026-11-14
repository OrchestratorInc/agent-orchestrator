export const FIRST_RUN_PROMPT_COHORT_MAX = 49;

export type FirstRunPromptVariant = "prompt" | "control";
export type FirstRunPromptAction = "clone" | "local_folder" | "standalone" | "dismissed";

export function firstRunPromptVariant(cohort: number | null): FirstRunPromptVariant {
	return cohort !== null && Number.isInteger(cohort) && cohort >= 0 && cohort <= FIRST_RUN_PROMPT_COHORT_MAX
		? "prompt"
		: "control";
}
