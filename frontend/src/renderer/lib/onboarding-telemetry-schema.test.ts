import { describe, expect, it } from "vitest";
import { ONBOARDING_STEPS } from "./onboarding-steps";
import {
	activationBucket,
	countBucket,
	durationBucket,
	ONBOARDING_EVENTS,
	onboardingRateLimitKey,
	sanitizeOnboardingProperties,
} from "./onboarding-telemetry-schema";
import { sanitizeRendererProperties } from "./telemetry";

const LEAKS = {
	path: "/Users/someone/code/private-repo",
	repo: "acme/secret-repo",
	url: "https://github.com/acme/secret-repo.git",
	email: "someone@example.com",
	username: "someone",
	message: "fatal: could not read Username for 'https://github.com'",
};

describe("onboarding telemetry allowlist", () => {
	it("derives step_index from the step and never passes the caller's", () => {
		ONBOARDING_STEPS.forEach((step, index) => {
			expect(sanitizeOnboardingProperties("ao.onboarding.step_viewed", { step, step_index: 99 })).toEqual({ step, step_index: index });
		});
		expect(sanitizeOnboardingProperties("ao.onboarding.step_viewed", { step: "/Users/x", step_index: 2 })).toEqual({});
	});

	it("drops paths, repository names, URLs, emails, usernames and messages from every event", () => {
		for (const event of ONBOARDING_EVENTS) {
			const safe = sanitizeOnboardingProperties(event, { ...LEAKS, step: "tools", run_kind: "first_run" });
			for (const value of Object.values(safe)) {
				expect(Object.values(LEAKS)).not.toContain(value);
			}
			for (const key of Object.keys(LEAKS)) expect(safe).not.toHaveProperty(key);
		}
	});

	it("keeps only closed values on step events", () => {
		expect(sanitizeOnboardingProperties("ao.onboarding.step_viewed", {
			step: "tools",
			run_kind: "first_run",
			missing: "gh,agent_auth,/Users/x,git",
			agents_ready: 2,
		})).toEqual({ step: "tools", step_index: 3, run_kind: "first_run", missing: "git,gh,agent_auth", agents_ready: 2 });
		expect(sanitizeOnboardingProperties("ao.onboarding.step_viewed", { step: "import_chats", chats_found: 14 }))
			.toEqual({ step: "import_chats", step_index: 5, chats_found_bucket: "6_20" });
		expect(sanitizeOnboardingProperties("ao.onboarding.step_completed", {
			step: "welcome",
			time_on_step_ms: 12_345.6,
			story_points_seen: 3,
			story_completed: true,
		})).toEqual({ step: "welcome", step_index: 1, time_on_step_ms: 12_346, story_points_seen: 3, story_completed: true });
		expect(sanitizeOnboardingProperties("ao.onboarding.step_completed", { step: "tour_project", project_source: "clone" }))
			.toEqual({ step: "tour_project", step_index: 6, project_source: "clone" });
		expect(sanitizeOnboardingProperties("ao.onboarding.step_completed", { step: "tour_project", project_source: "acme/repo", story_points_seen: 7, run_kind: "beta" }))
			.toEqual({ step: "tour_project", step_index: 6 });
		expect(sanitizeOnboardingProperties("ao.onboarding.step_skipped", { step: "sign_in", reason: "user", time_on_step_ms: 900 }))
			.toEqual({ step: "sign_in", step_index: 2, reason: "user", time_on_step_ms: 900 });
		expect(sanitizeOnboardingProperties("ao.onboarding.step_skipped", { step: "sign_in", reason: "because" })).toEqual({ step: "sign_in", step_index: 2 });
		expect(sanitizeOnboardingProperties("ao.onboarding.back_clicked", { step: "tools", to_step: "sign_in" }))
			.toEqual({ step: "tools", step_index: 3, to_step: "sign_in" });
	});

	it("buckets run durations from the measured value", () => {
		expect(sanitizeOnboardingProperties("ao.onboarding.completed", {
			step: "tools",
			run_kind: "replay",
			outcome: "skipped_setup",
			duration_ms: 4 * 60_000,
			launch_count: 2,
			resumed: true,
			back_count: 1,
			auto_skipped_count: 1,
			duration_bucket: "forged",
		})).toEqual({
			step: "tools",
			step_index: 3,
			run_kind: "replay",
			outcome: "skipped_setup",
			duration_ms: 240_000,
			duration_bucket: "3m_10m",
			launch_count: 2,
			resumed: true,
			back_count: 1,
			auto_skipped_count: 1,
		});
		expect(sanitizeOnboardingProperties("ao.onboarding.resumed", { step: "sign_in", away_ms: 3 * 3_600_000, launch_count: 3 }))
			.toEqual({ step: "sign_in", step_index: 2, away_ms: 10_800_000, away_bucket: "2h_1d", launch_count: 3 });
		expect(sanitizeOnboardingProperties("ao.onboarding.abandoned", { step: "welcome", duration_ms: -5 })).toEqual({ step: "welcome", step_index: 1 });
		// A clock jump is clamped, not sent as is.
		expect(sanitizeOnboardingProperties("ao.onboarding.abandoned", { step: "welcome", duration_ms: 1e15 }).duration_ms).toBe(30 * 24 * 3_600_000);
	});

	it("keeps action properties within their vocabularies", () => {
		expect(sanitizeOnboardingProperties("ao.onboarding.action_started", { action: "agent_install", target: "claude-code", step: "tools", attempt: 2, auto: false }))
			.toEqual({ action: "agent_install", target: "claude-code", step: "tools", step_index: 3, attempt: 2, auto: false });
		expect(sanitizeOnboardingProperties("ao.onboarding.action_finished", {
			action: "gh_install",
			target: "gh",
			step: "github_auth",
			attempt: 1,
			outcome: "failed",
			reason: "start_failed",
			error_code: "INSTALL_UNAVAILABLE",
			duration_ms: 1500,
		})).toEqual({ action: "gh_install", target: "gh", step: "github_auth", step_index: 4, attempt: 1, outcome: "failed", reason: "start_failed", error_code: "INSTALL_UNAVAILABLE", duration_ms: 1500 });
		// The tour's project add and clone are reported by ao.projects.*, not as actions.
		expect(sanitizeOnboardingProperties("ao.onboarding.action_finished", { action: "project_clone", reason: "refused" })).toEqual({});
		expect(sanitizeOnboardingProperties("ao.onboarding.action_finished", {
			action: "rm -rf",
			target: "Claude Code",
			attempt: 0,
			outcome: "exploded",
			reason: "exit status 1",
			error_code: "remote: Repository not found.",
		})).toEqual({});
	});

	it("keeps a failure cause only from the closed list, and only for install_failed", () => {
		const finished = { action: "agent_install", target: "codex", attempt: 1, outcome: "failed" };
		expect(sanitizeOnboardingProperties("ao.onboarding.action_finished", { ...finished, reason: "install_failed", failure_cause: "npm_missing" }))
			.toEqual({ ...finished, reason: "install_failed", failure_cause: "npm_missing" });
		expect(sanitizeOnboardingProperties("ao.onboarding.action_finished", { ...finished, reason: "install_failed", failure_cause: "exec: \"npm\": executable file not found in $PATH" }))
			.toEqual({ ...finished, reason: "install_failed" });
		expect(sanitizeOnboardingProperties("ao.onboarding.action_finished", { ...finished, reason: "start_failed", failure_cause: "network" }))
			.toEqual({ ...finished, reason: "start_failed" });
	});

	it("buckets activation time", () => {
		expect(sanitizeOnboardingProperties("ao.onboarding.activated", { milestone: "first_message", time_since_completed_ms: 90_000, during_tour: true, run_kind: "first_run" }))
			.toEqual({ milestone: "first_message", time_since_completed_ms: 90_000, time_bucket: "lt_5m", during_tour: true });
		expect(activationBucket(20 * 60_000)).toBe("15m_1h");
		expect(activationBucket(2 * 86_400_000)).toBe("1d_7d");
	});

	it("buckets durations and counts", () => {
		expect([30_000, 120_000, 300_000, 1_200_000, 3_600_000, 36_000_000, 172_800_000].map(durationBucket))
			.toEqual(["lt_1m", "1m_3m", "3m_10m", "10m_30m", "30m_2h", "2h_1d", "gte_1d"]);
		expect([0, 1, 3, 20, 21].map(countBucket)).toEqual(["0", "1", "2_5", "6_20", "21_plus"]);
	});

	it("is what telemetry.ts applies to onboarding events", async () => {
		const input = { step: "tools", run_kind: "first_run", missing: "gh", ...LEAKS };
		expect(await sanitizeRendererProperties("ao.onboarding.step_viewed", input)).toEqual(sanitizeOnboardingProperties("ao.onboarding.step_viewed", input));
	});

	it("rate-limits per step and action, from sanitized values only", () => {
		expect(onboardingRateLimitKey("ao.onboarding.step_viewed", { step: "tools", step_index: 3 })).toBe("ao.onboarding.step_viewed:tools");
		expect(onboardingRateLimitKey("ao.onboarding.action_started", { action: "agent_install", target: "codex", step: "tools" }))
			.toBe("ao.onboarding.action_started:tools:agent_install:codex");
		expect(onboardingRateLimitKey("ao.routes.viewed", { surface: "home" })).toBeUndefined();
	});
});
