import { beforeEach, describe, expect, it } from "vitest";
import { clearLegacySandboxProvider, readSelectedSandboxProvider } from "./sandbox-provider-store";

const storageKey = "ao.cloud.sandboxProvider";

describe("sandbox-provider-store", () => {
	beforeEach(() => {
		window.localStorage.clear();
	});

	it("defaults to null (follow the control plane default)", () => {
		expect(readSelectedSandboxProvider()).toBeNull();
	});

	it("reads a previously persisted value", () => {
		window.localStorage.setItem(storageKey, "docker");
		expect(readSelectedSandboxProvider()).toBe("docker");
	});

	it("clears a migrated legacy value", () => {
		window.localStorage.setItem(storageKey, "coder");
		clearLegacySandboxProvider();
		expect(readSelectedSandboxProvider()).toBeNull();
	});
});
