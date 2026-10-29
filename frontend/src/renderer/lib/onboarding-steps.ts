export const ONBOARDING_STEPS = ["welcome", "attribution", "github_auth"] as const;
export type OnboardingStep = (typeof ONBOARDING_STEPS)[number];
