export const ONBOARDING_STEPS = ["welcome", "github_auth"] as const;
export type OnboardingStep = (typeof ONBOARDING_STEPS)[number];
