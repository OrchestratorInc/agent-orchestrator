import { describe, expect, it, vi } from "vitest";
import { cloudProjectInput, initialProjectCreationStep, validGitHubRepositoryURL, coderSetupProblem, coderParameterProblem, findCloudProjectForRepository } from "./projectCreation";

describe("cloud project creation", () => {
  it("finds an existing repository project beyond the first page", async () => {
    const listProjects = vi.fn().mockResolvedValueOnce({ items: [{ id: "other", repositoryUrl: "https://github.com/me/other" }], page: { hasMore: true, nextCursor: "next" } })
      .mockResolvedValueOnce({ items: [{ id: "existing", repositoryUrl: "https://github.com/me/demo.git" }], page: { hasMore: false } });
    expect(await findCloudProjectForRepository({ listProjects } as never, "org", "https://github.com/me/demo")).toBe("existing");
    expect(listProjects.mock.calls[1]?.[1]).toMatchObject({ cursor: "next" });
  });
  it("allows managed Coder Default but requires a permitted BYO template", () => {
    expect(coderSetupProblem({ configured: false }, [], "")).toBeNull();
    expect(coderSetupProblem({ configured: true }, [{ id: "template" }], "")).toMatch(/template/i);
    expect(coderSetupProblem({ configured: true }, [{ id: "template" }], "template")).toBeNull();
    expect(coderSetupProblem({ configured: true }, [], "missing")).toMatch(/template/i);
  });
  it.each([
    ["number", "not-a-number", [], true],
    ["number", "3", [], false],
    ["bool", "yes", [], true],
    ["bool", "true", [], false],
    ["list(string)", '["one"]', [{ name: "One", value: "one", description: "" }], false],
    ["list(string)", '["other"]', [{ name: "One", value: "one", description: "" }], true],
  ])("validates declared Coder %s values", (type, value, options, invalid) => {
    expect(Boolean(coderParameterProblem({ name: "field", displayName: "Field", description: "", type, formType: "", mutable: false, required: true, defaultValue: "", options, order: 1 }, value))).toBe(invalid);
  });
  it("preserves nested role models and Coder project configuration", () => {
    const config = { worker: { agent: "codex", agentConfig: { model: "model-test", effort: "high" } }, coder: { templateId: "template", parameters: { region: "india" } }, sandboxProvider: "coder" };
    expect(cloudProjectInput({ displayName: "Demo", repositoryUrl: "https://github.com/me/demo", defaultBranch: "main", config }).config).toEqual(config);
  });
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
