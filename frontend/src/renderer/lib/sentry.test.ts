import { afterEach, describe, expect, it, vi } from "vitest";
import { DEFAULT_ELECTRON_SENTRY_DSN } from "../../shared/sentry-config";
import { rendererSentryDsn } from "./sentry";

afterEach(() => vi.unstubAllEnvs());

describe("renderer Sentry configuration", () => {
	it("selects the Electron project unless VITE_AO_SENTRY_DSN overrides it", () => {
		vi.stubEnv("VITE_AO_SENTRY_DSN", undefined);
		expect(rendererSentryDsn()).toBe(DEFAULT_ELECTRON_SENTRY_DSN);
		vi.stubEnv("VITE_AO_SENTRY_DSN", "https://custom.example/3");
		expect(rendererSentryDsn()).toBe("https://custom.example/3");
		vi.stubEnv("VITE_AO_SENTRY_DSN", "");
		expect(rendererSentryDsn()).toBe("");
	});
});
