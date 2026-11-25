import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import {
	resetCloudTelemetryContextForTest,
	setTelemetryHashSaltForTest,
	sanitizeRendererExceptionProperties,
	sanitizeRendererProperties,
	setCloudTelemetryContext,
	withTelemetryContext,
} from "./telemetry";

beforeAll(() => setTelemetryHashSaltForTest("0".repeat(64)));
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

	it("no longer accepts the website visit link event or its visitor id", async () => {
		const visitor = "019a3f5e-8c2b-7d41-b6f0-3e2d1c0b9a87";
		const safe = await sanitizeRendererProperties("ao.renderer.cloud_web_visitor_link_requested", { website_distinct_id: visitor });
		expect(JSON.stringify(safe)).not.toContain(visitor);
	});

	it("keys the cloud org hash with the install salt", async () => {
		const orgId = "22222222-2222-2222-2222-222222222222";
		const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(orgId));
		const unkeyed = Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, "0")).join("");
		setCloudTelemetryContext({ orgId });
		await vi.waitFor(() => expect(withTelemetryContext({}).cloud_org_id_hash).toMatch(/^[0-9a-f]{64}$/));
		expect(withTelemetryContext({}).cloud_org_id_hash).not.toBe(unkeyed);
		setTelemetryHashSaltForTest("1".repeat(64));
		try {
			resetCloudTelemetryContextForTest();
			setCloudTelemetryContext({ orgId });
			await vi.waitFor(() => expect(withTelemetryContext({}).cloud_org_id_hash).toMatch(/^[0-9a-f]{64}$/));
			const other = withTelemetryContext({}).cloud_org_id_hash;
			resetCloudTelemetryContextForTest();
			setTelemetryHashSaltForTest("0".repeat(64));
			setCloudTelemetryContext({ orgId });
			await vi.waitFor(() => expect(withTelemetryContext({}).cloud_org_id_hash).toMatch(/^[0-9a-f]{64}$/));
			expect(withTelemetryContext({}).cloud_org_id_hash).not.toBe(other);
		} finally {
			setTelemetryHashSaltForTest("0".repeat(64));
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
