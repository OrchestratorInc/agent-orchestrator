import { afterEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "./bridge";
import {
	clearCloudUser,
	resetCloudTelemetryContextForTest,
	sanitizeRendererExceptionProperties,
	sanitizeRendererProperties,
	setCloudTelemetryContext,
	withTelemetryContext,
} from "./telemetry";

afterEach(() => resetCloudTelemetryContextForTest());

describe("cloud funnel event allowlist", () => {
	it.each(["ao.renderer.cloud_limit_reached", "ao.renderer.cloud_plan_refusal_shown"])("keeps the chat source on %s", async (event) => {
		expect(await sanitizeRendererProperties(event, { code: "WORKER_SLOTS_FULL", shown: false, source: "chat" })).toEqual({
			code: "WORKER_SLOTS_FULL", shown: false, source: "chat",
		});
		expect(await sanitizeRendererProperties(event, { code: "WORKER_SLOTS_FULL", shown: true, source: "private text" })).toEqual({
			code: "WORKER_SLOTS_FULL", shown: true,
		});
	});

	it("keeps vocabulary values and hashes ids", async () => {
		const safe = await sanitizeRendererProperties("ao.renderer.cloud_session_failed", {
			provider: "coder",
			session_kind: "worker",
			stage: "startup",
			code: "bootstrap_failed",
			status: 422,
			ttr_ms: 1234.6,
			project_id: "11111111-1111-1111-1111-111111111111",
			prompt: "do the secret thing",
			email: "dev@example.com",
		});
		expect(safe).toMatchObject({ provider: "coder", session_kind: "worker", stage: "startup", code: "bootstrap_failed", status: 422, ttr_ms: 1235 });
		expect(safe.project_id_hash).toMatch(/^[0-9a-f]{64}$/);
		expect(JSON.stringify(safe)).not.toContain("secret");
		expect(JSON.stringify(safe)).not.toContain("dev@example.com");
		expect(safe).not.toHaveProperty("project_id");
	});

	it("replaces anything outside a vocabulary", async () => {
		const failed = await sanitizeRendererProperties("ao.renderer.cloud_session_failed", {
			provider: "https://evil.example",
			session_kind: "admin",
			stage: "boot",
			code: "Error: token=abc at /Users/me/file.ts",
			ttr_ms: -5,
		});
		expect(failed).toEqual({ provider: "other", session_kind: "other", stage: "other", code: "other" });
		const offering = await sanitizeRendererProperties("ao.renderer.cloud_offering_resolved", {
			org_kind: "enterprise",
			billing_mode: "free",
			default_provider: "freestyle",
			provider_count: 2,
			org_id: "raw-org-id",
		});
		expect(offering).toEqual({ org_kind: "unknown", billing_mode: "unknown", default_provider: "freestyle", provider_count: 2 });
		expect(await sanitizeRendererProperties("ao.renderer.cloud_checkout_started", { plan: "Pro Plan $$" })).toEqual({ plan: "other" });
		expect(await sanitizeRendererProperties("ao.renderer.cloud_checkout_started", { plan: "starter", trial: true })).toEqual({ plan: "starter", trial: true });
		expect(await sanitizeRendererProperties("ao.renderer.cloud_checkout_completed", { plan: "starter", trial: false, duration_ms: 5 })).toEqual({
			plan: "starter",
			trial: false,
			duration_ms: 5,
		});
		expect(await sanitizeRendererProperties("ao.renderer.cloud_checkout_completed", { plan: "starter", trial: "7 days" })).toEqual({ plan: "starter" });
		expect(await sanitizeRendererProperties("ao.renderer.cloud_signed_in", { method: "workos", email: "a@b.c" })).toEqual({ method: "workos" });
		expect(await sanitizeRendererProperties("ao.renderer.cloud_signed_in", { method: "workos", source: "artifact_publish" })).toEqual({ method: "workos", source: "artifact_publish" });
		expect(await sanitizeRendererProperties("ao.renderer.cloud_signed_in", { method: "workos", source: "onboarding" })).toEqual({ method: "workos", source: "onboarding" });
		expect(await sanitizeRendererProperties("ao.renderer.cloud_signed_in", { method: "workos", source: "/Users/a/plan.md" })).toEqual({ method: "workos" });
		expect(await sanitizeRendererProperties("ao.renderer.cloud_signed_in", { method: "workos", same_account: true })).toEqual({ method: "workos", same_account: true });
		expect(await sanitizeRendererProperties("ao.renderer.cloud_signed_in", { method: "workos", same_account: "user-1" })).toEqual({ method: "workos" });
		expect(await sanitizeRendererProperties("ao.renderer.cloud_limit_reached", { code: "WEEKLY_LIMIT_REACHED", shown: true })).toEqual({
			code: "WEEKLY_LIMIT_REACHED",
			shown: true,
		});
	});

	it("keeps only a well-formed website visitor id on the visit link event", async () => {
		const event = "ao.renderer.cloud_web_visitor_link_requested";
		const visitor = "019a3f5e-8c2b-7d41-b6f0-3e2d1c0b9a87";
		expect(await sanitizeRendererProperties(event, { website_distinct_id: visitor, email: "a@b.c", user_id: "user_A" })).toEqual({
			website_distinct_id: visitor,
		});
		for (const invalid of ["", "has space", "/Users/me/secret", "a".repeat(201), "undefined", 7, null]) {
			expect(await sanitizeRendererProperties(event, { website_distinct_id: invalid })).toEqual({});
		}
	});

	it("tags every event and exception with the hashed cloud org, kind and billing mode", async () => {
		setCloudTelemetryContext({ orgId: "22222222-2222-2222-2222-222222222222", orgKind: "b2c", billingMode: "self_serve" });
		// The org id is hashed asynchronously; under a loaded suite one tick is not enough.
		await vi.waitFor(() => expect(withTelemetryContext({}).cloud_org_id_hash).toMatch(/^[0-9a-f]{64}$/));
		const context = withTelemetryContext({});
		expect(context).toMatchObject({ cloud_org_kind: "b2c", cloud_billing_mode: "self_serve" });
		expect(context.cloud_org_id_hash).toMatch(/^[0-9a-f]{64}$/);
		expect(JSON.stringify(context)).not.toContain("22222222");

		const exception = await sanitizeRendererExceptionProperties(new Error("boom"), {
			source: "window-error",
			session_id: "33333333-3333-3333-3333-333333333333",
		});
		expect(exception.session_id_hash).toMatch(/^[0-9a-f]{64}$/);
		expect(exception).not.toHaveProperty("session_id");
	});
});

describe("cloud plan context", () => {
	const org = { orgId: "22222222-2222-2222-2222-222222222222", orgKind: "b2c", billingMode: "self_serve" };

	it.each([
		["free", false],
		["starter", true],
		["pro", true],
		["enterprise", true],
		["none", false],
		["unknown", false],
	] as const)("tags events with cloud_plan %s", (plan, paid) => {
		setCloudTelemetryContext({ ...org, plan, paid });
		expect(withTelemetryContext({})).toMatchObject({ cloud_plan: plan, cloud_paid: paid });
	});

	it("flags a trial only on a paid plan", () => {
		setCloudTelemetryContext({ ...org, plan: "pro", paid: true, trial: true });
		expect(withTelemetryContext({})).toMatchObject({ cloud_plan: "pro", cloud_paid: true, cloud_trial: true });
		setCloudTelemetryContext({ ...org, plan: "free", paid: false, trial: true });
		expect(withTelemetryContext({})).toMatchObject({ cloud_plan: "free", cloud_paid: false, cloud_trial: false });
	});

	it("reports unknown for a value outside the vocabulary, never the value", () => {
		for (const junk of ["Pro Plan $49", "price_123", "acme-corp", "", undefined]) {
			setCloudTelemetryContext({ ...org, plan: junk, paid: true });
			const context = withTelemetryContext({});
			expect(context).toMatchObject({ cloud_plan: "unknown", cloud_paid: false });
			if (junk) expect(JSON.stringify(context)).not.toContain(junk);
		}
	});

	it("moves to none on sign-out and tells main once per change", async () => {
		const setPlan = vi.spyOn(aoBridge.telemetry, "setPlan").mockResolvedValue(true);
		try {
			setCloudTelemetryContext({ ...org, plan: "pro", paid: true });
			setCloudTelemetryContext({ ...org, plan: "pro", paid: true });
			clearCloudUser();
			clearCloudUser();
			expect(withTelemetryContext({})).toMatchObject({ cloud_plan: "none", cloud_paid: false });
			await vi.waitFor(() => expect(setPlan.mock.calls).toEqual([["pro"], ["none"]]));
		} finally {
			setPlan.mockRestore();
		}
	});

	it("keeps only vocabulary values on ao.billing.plan_changed", async () => {
		expect(await sanitizeRendererProperties("ao.billing.plan_changed", { from_plan: "starter", to_plan: "pro", direction: "upgrade", from_trial: true, to_trial: "yes", price: 49, org_name: "Acme" })).toEqual({
			from_plan: "starter", to_plan: "pro", direction: "upgrade", from_trial: true,
		});
		expect(await sanitizeRendererProperties("ao.billing.plan_changed", { from_plan: "Pro $49", to_plan: "sub_123", direction: "sideways" })).toEqual({
			from_plan: "unknown", to_plan: "unknown", direction: "unknown",
		});
	});
});
