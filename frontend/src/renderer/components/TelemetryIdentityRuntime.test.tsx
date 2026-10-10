import { render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const hooks = vi.hoisted(() => ({
	session: null as unknown,
	setCloudUser: vi.fn(async () => undefined),
	identifyCloudUser: vi.fn(async () => undefined),
	clearCloudUser: vi.fn(),
}));
vi.mock("../lib/cloud-session", () => ({ useCloudSession: () => ({ session: hooks.session }) }));
vi.mock("../lib/bridge", () => ({ aoBridge: { telemetry: { setCloudUser: hooks.setCloudUser } } }));
vi.mock("../lib/telemetry", () => ({ identifyCloudUser: hooks.identifyCloudUser, clearCloudUser: hooks.clearCloudUser }));

import { TelemetryIdentityRuntime } from "./TelemetryIdentityRuntime";

const account = (authProvider: "workos" | "local") => ({
	authProvider,
	user: { id: "user_01H", email: "dev@example.com", displayName: "Dev" },
	storedAt: "2026-10-08T00:00:00Z",
});

describe("TelemetryIdentityRuntime", () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("identifies a WorkOS sign-in and hands the user id (not the email) to main", () => {
		hooks.session = account("workos");
		render(<TelemetryIdentityRuntime />);
		expect(hooks.identifyCloudUser).toHaveBeenCalledWith({ id: "user_01H", email: "dev@example.com" });
		expect(hooks.setCloudUser).toHaveBeenCalledWith("user_01H");
		expect(JSON.stringify(hooks.setCloudUser.mock.calls)).not.toContain("dev@example.com");
	});

	it("never identifies the dev-only local provider", () => {
		hooks.session = account("local");
		render(<TelemetryIdentityRuntime />);
		expect(hooks.identifyCloudUser).not.toHaveBeenCalled();
		expect(hooks.setCloudUser).toHaveBeenCalledWith(null);
	});

	it("clears the identity when signed out", () => {
		hooks.session = null;
		render(<TelemetryIdentityRuntime />);
		expect(hooks.clearCloudUser).toHaveBeenCalled();
		expect(hooks.setCloudUser).toHaveBeenCalledWith(null);
	});
});
