import { describe, expect, it } from "vitest";
import { shouldMaintainLocalConnection, shouldPollLocal, shouldPollLocalSource, shouldPollCloudSource } from "./shouldPoll";

describe("shouldPollLocal", () => {
	it("polls for the local environment", () => {
		expect(shouldPollLocal("local")).toBe(true);
	});

	it("does not poll for the cloud environment", () => {
		expect(shouldPollLocal("cloud")).toBe(false);
	});

	it("does not poll before the persisted choice has loaded", () => {
		expect(shouldPollLocal(null)).toBe(false);
	});

	it("does not retain periodic Local connection work in Cloud", () => {
		expect(shouldMaintainLocalConnection("cloud")).toBe(false);
		expect(shouldMaintainLocalConnection("local")).toBe(true);
	});
});

it("keeps Local and Cloud polling concurrently when both are ready", () => {
	expect(shouldPollLocalSource({ paired: true, appActive: true })).toBe(true);
	expect(shouldPollCloudSource({ signedIn: true, orgId: "org-1", appActive: true })).toBe(true);
	expect(shouldPollLocalSource({ paired: true, appActive: false })).toBe(false);
	expect(shouldPollCloudSource({ signedIn: false, orgId: null, appActive: true })).toBe(false);
});
