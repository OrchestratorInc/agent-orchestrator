import { describe, expect, it } from "vitest";
import { hasConnectionForHarness, readyHarnesses } from "./agentReadiness";

// RedactedProviderConnection (packages/cloud-client/src/schema.ts) keys the
// connection by `provider`, not `agent`, and its validation field is
// `validationState`. Verified against the schema before writing this test.
const claude = { provider: "claude-code", validationState: "valid" };
const codexInvalid = { provider: "codex", validationState: "invalid" };
// daytona is a sandbox provider connection, never a coding agent — even
// valid, it must never make a spawn picker look ready to run code.
const daytonaValid = { provider: "daytona", validationState: "valid" };

describe("hasConnectionForHarness", () => {
	it("is true for a harness with its own valid connection", () => {
		expect(hasConnectionForHarness([claude] as never, "claude-code")).toBe(true);
	});

	// The bug this function exists to prevent: a Claude key must not make a
	// Codex spawn look ready, which is what an "any valid key" check does.
	it("is false for a different harness, however many other keys exist", () => {
		expect(hasConnectionForHarness([claude] as never, "codex")).toBe(false);
	});

	it("is false when the harness's own connection failed validation", () => {
		expect(hasConnectionForHarness([codexInvalid] as never, "codex")).toBe(false);
	});

	it("is false with no connections at all", () => {
		expect(hasConnectionForHarness([], "claude-code")).toBe(false);
	});

	// Same bug, non-agent flavor: a valid daytona (sandbox) connection must
	// not make "daytona" look like a runnable harness.
	it("is false for a valid daytona connection queried as a harness", () => {
		expect(hasConnectionForHarness([daytonaValid] as never, "daytona")).toBe(false);
	});
});

describe("readyHarnesses", () => {
	it("names only the harnesses that can actually run", () => {
		expect(readyHarnesses([claude, codexInvalid] as never)).toEqual(["claude-code"]);
	});

	// The false-positive this task exists to prevent, non-agent flavor: a
	// valid daytona connection is a sandbox provider, not a coding agent, and
	// must never appear in the "ready to spawn" list.
	it("excludes a valid daytona connection", () => {
		expect(readyHarnesses([claude, daytonaValid] as never)).toEqual(["claude-code"]);
	});
});
