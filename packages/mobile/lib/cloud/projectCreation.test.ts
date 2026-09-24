import { describe, expect, it, vi } from "vitest";
import { cloudProjectInput, initialProjectCreationStep, validGitHubRepositoryURL } from "./projectCreation";

describe("cloud project creation", () => {
  it("asks for a GitHub token only when no saved token exists", () => {
    expect(initialProjectCreationStep([])).toBe("github-token");
    expect(initialProjectCreationStep([{ provider: "github", validationState: "valid" }])).toBe("repository");
  });
  it("accepts only HTTPS GitHub repository URLs", () => {
    expect(validGitHubRepositoryURL("https://github.com/acme/app.git")).toBe(true);
    expect(validGitHubRepositoryURL("http://github.com/acme/app")).toBe(false);
    expect(validGitHubRepositoryURL("https://example.com/acme/app")).toBe(false);
    expect(validGitHubRepositoryURL("https://github.com/acme")).toBe(false);
    expect(validGitHubRepositoryURL("https://github.com.evil.test/acme/app")).toBe(false);
    expect(validGitHubRepositoryURL("https://github.com@evil.test/acme/app")).toBe(false);
  });

  it("does not rely on React Native's incomplete URL polyfill", () => {
    vi.stubGlobal("URL", class { constructor() { throw new Error("URL unavailable"); } });
    try {
      expect(validGitHubRepositoryURL("https://github.com/acme/app")).toBe(true);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("trims project fields and saves distinct worker and orchestrator agents", () => {
    expect(cloudProjectInput({
      displayName: " App ",
      repositoryUrl: " https://github.com/acme/app.git ",
      defaultBranch: " main ",
      workerAgent: "claude-code",
      orchestratorAgent: "codex",
    })).toEqual({
      displayName: "App",
      repositoryUrl: "https://github.com/acme/app.git",
      defaultBranch: "main",
      config: { worker: { agent: "claude-code" }, orchestrator: { agent: "codex" } },
    });
  });
});
