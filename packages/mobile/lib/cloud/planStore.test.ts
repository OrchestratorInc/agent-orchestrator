import { createElement } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";
import { forgetCloudAccountCaches } from "./accountCaches";
import type { CloudClient } from "./client";
import { CloudError } from "./errors";
import { clearCloudPlan, invalidateCloudPlan, loadCloudPlan, useCloudPlan, type CloudPlanState } from "./planStore";
import type { CloudBillingSummary } from "./types";

const starter: CloudBillingSummary = { enabled: true, billingMode: "self_serve", plan: "starter", entitled: true };
const free: CloudBillingSummary = { enabled: true, billingMode: "self_serve", plan: "free", entitled: false };

function deferred<T>() {
	let resolve!: (value: T) => void;
	let reject!: (error: unknown) => void;
	const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
	return { promise, resolve, reject };
}

function clientWith(getBilling: (orgId: string) => Promise<CloudBillingSummary>) {
	const fn = vi.fn(getBilling);
	return { client: { getBilling: fn } as unknown as CloudClient, getBilling: fn };
}

const flush = async () => { for (let i = 0; i < 6; i++) await Promise.resolve(); };

// Every renderer a test makes, so none outlives it and reads into the next.
const renderers: ReactTestRenderer[] = [];
let renderer: ReactTestRenderer | undefined;
afterEach(() => {
	act(() => { for (const mounted of renderers.splice(0)) mounted.unmount(); renderer?.unmount(); });
	renderer = undefined;
	clearCloudPlan();
});

function renderPlan(client: CloudClient, orgId: string | undefined) {
	const seen: (CloudPlanState | undefined)[] = [];
	function Probe({ org }: { org: string | undefined }) {
		seen.push(useCloudPlan(client, org));
		return null;
	}
	act(() => { renderer = create(createElement(Probe, { org: orgId })); });
	renderers.push(renderer!);
	return { seen, latest: () => seen[seen.length - 1], rerender: (org: string | undefined) => act(() => renderer?.update(createElement(Probe, { org }))) };
}

describe("cloud plan store", () => {
	it("reads the plan once for every screen that shows it, and again once it's half a minute old", async () => {
		const { client, getBilling } = clientWith(async () => starter);
		await Promise.all([loadCloudPlan(client, "o", { now: 0 }), loadCloudPlan(client, "o", { now: 0 })]);
		await loadCloudPlan(client, "o", { now: Date.now() + 1_000 });
		expect(getBilling).toHaveBeenCalledTimes(1);
		await loadCloudPlan(client, "o", { now: Date.now() + 31_000 });
		expect(getBilling).toHaveBeenCalledTimes(2);
	});

	it("reads it again after a refusal, so the sheet explaining it shows current numbers", async () => {
		const { client, getBilling } = clientWith(async () => starter);
		await loadCloudPlan(client, "o");
		invalidateCloudPlan();
		await loadCloudPlan(client, "o");
		expect(getBilling).toHaveBeenCalledTimes(2);
	});

	it("drops a plan that arrives after the account changed", async () => {
		const replies = [deferred<CloudBillingSummary>(), deferred<CloudBillingSummary>()];
		let calls = 0;
		const { client } = clientWith(() => replies[calls++].promise);
		const { latest } = renderPlan(client, "o");
		expect(latest()).toMatchObject({ status: "loading" });
		act(() => forgetCloudAccountCaches());
		// The old account's answer arrives while the new account's read is out.
		await act(async () => { replies[0].resolve(starter); await flush(); });
		expect(calls).toBe(2);
		expect(latest()).toMatchObject({ status: "loading" });
		await act(async () => { replies[1].resolve(free); await flush(); });
		expect(latest()).toMatchObject({ status: "ready", summary: free });
	});

	it("shows the screen its own organization's plan, never the last one's", async () => {
		const { client } = clientWith(async (orgId) => (orgId === "a" ? starter : free));
		const { latest, rerender } = renderPlan(client, "a");
		await act(async () => { await Promise.resolve(); });
		expect(latest()).toMatchObject({ status: "ready", summary: starter });
		rerender("b");
		expect(latest()).toMatchObject({ status: "loading" });
		await act(async () => { await Promise.resolve(); });
		expect(latest()).toMatchObject({ status: "ready", summary: free });
	});

	it("treats a control plane without plans as one where none apply", async () => {
		const { client } = clientWith(async () => { throw new CloudError("Not found", { status: 404 }); });
		const { latest } = renderPlan(client, "o");
		await act(async () => { await Promise.resolve(); });
		expect(latest()).toMatchObject({ status: "ready", summary: { enabled: false } });
	});

	it("keeps the last plan when a refresh fails, and says so only when there was none", async () => {
		let fail = false;
		const { client } = clientWith(async () => { if (fail) throw new CloudError("Down", { status: 503 }); return starter; });
		const { latest } = renderPlan(client, "o");
		await act(async () => { await Promise.resolve(); });
		fail = true;
		await act(async () => { await latest()?.reload(); });
		expect(latest()).toMatchObject({ status: "ready", summary: starter });

		clearCloudPlan();
		const other = renderPlan(client, "p");
		await act(async () => { await flush(); });
		expect(other.latest()).toMatchObject({ status: "failed" });
	});

	it("has nothing to show without an organization", () => {
		const { client, getBilling } = clientWith(async () => starter);
		const { latest } = renderPlan(client, undefined);
		expect(latest()).toBeUndefined();
		expect(getBilling).not.toHaveBeenCalled();
	});
});

describe("cloud plan store, reads in flight", () => {
	// A refusal while the plan is being read: the answer still lands, and the
	// plan is read again, since it may be from before the refusal.
	it("keeps a read that a refusal overtook, then reads again", async () => {
		const replies = [deferred<CloudBillingSummary>(), deferred<CloudBillingSummary>()];
		let calls = 0;
		const { client } = clientWith(() => replies[calls++].promise);
		const { latest } = renderPlan(client, "o");
		act(() => invalidateCloudPlan());
		await act(async () => { replies[0].resolve(starter); await flush(); });
		expect(calls).toBe(2);
		await act(async () => { replies[1].resolve(free); await flush(); });
		expect(latest()).toMatchObject({ status: "ready", summary: free });
	});

	// Signed out and back in while the first read was out, with the same client
	// and organization id: the screen asks again, and only that answer shows.
	it("reads again for the screen when the account changed under its first read", async () => {
		const replies = [deferred<CloudBillingSummary>(), deferred<CloudBillingSummary>()];
		let calls = 0;
		const { client } = clientWith(() => replies[calls++].promise);
		const { latest } = renderPlan(client, "o");
		act(() => clearCloudPlan());
		expect(calls).toBe(2);
		await act(async () => { replies[0].resolve(starter); await flush(); });
		expect(latest()).toMatchObject({ status: "loading" });
		await act(async () => { replies[1].resolve(free); await flush(); });
		expect(latest()).toMatchObject({ status: "ready", summary: free });
	});
});
