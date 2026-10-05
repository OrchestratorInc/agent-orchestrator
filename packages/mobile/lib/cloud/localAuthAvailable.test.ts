import { describe, expect, it } from "vitest";
import { localAuthAvailable } from "./localAuthAvailable";

describe("localAuthAvailable", () => {
	// Email/password exists for the Docker control plane in cloud/compose.yaml,
	// which sets AO_CLOUD_LOCAL_AUTH=true. Hosted deployments reject it.
	it("is true for a loopback control plane", () => {
		expect(localAuthAvailable("http://127.0.0.1:8081")).toBe(true);
		expect(localAuthAvailable("http://localhost:8081")).toBe(true);
	});

	it("is true for a plain-http LAN address used for device testing", () => {
		expect(localAuthAvailable("http://192.168.1.50:8081")).toBe(true);
	});

	it("is false for the hosted control planes", () => {
		expect(localAuthAvailable("https://staging-api.aoagents.dev")).toBe(false);
		expect(localAuthAvailable("https://api.aoagents.dev")).toBe(false);
	});

	it("is false for an unparseable value rather than guessing", () => {
		expect(localAuthAvailable("not a url")).toBe(false);
	});
});
