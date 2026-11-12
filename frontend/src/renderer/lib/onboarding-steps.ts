export const ONBOARDING_STEPS = ["welcome", "github_auth", "first_task"] as const;
export type OnboardingStep = (typeof ONBOARDING_STEPS)[number];
