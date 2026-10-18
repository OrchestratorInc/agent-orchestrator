import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ComponentProps } from "react";
import { PromptOverrideDialog } from "./PromptOverrideDialog";
import { setApiBaseUrl } from "../../lib/api-client";

const DEFAULT_WORKER =
  "## AO Worker Role\n\nYou are an implementation worker.\n\n## Local Git Rules\n\n- Work locally.";
const DEFAULT_ORCH =
  "## AO Orchestrator Role\n\nYou are the human-facing orchestrator.";

type DialogProps = ComponentProps<typeof PromptOverrideDialog>;

// A scope harness bundles the bits that differ between user-scope and
// project-scope: the URL the dialog GETs, the wire shape that carries the
// stored override + baselines, and how the PUT body surfaces the override
// fields. Each test builds its harness via forScope(scope).
type Scope = "user" | "project";

// buildGetBody returns the GET response body for the scope, carrying a stored
// override (worker/orch) plus the baseline defaults and, for project-scope,
// the project wrapper (displayName + config) the PUT must echo back.
function buildGetBody(
  scope: Scope,
  opts: {
    workerOverride?: string;
    orchOverride?: string;
    model?: string;
    permissions?: string;
    extraConfig?: Record<string, unknown>;
  } = {},
) {
  const { workerOverride, orchOverride, model, permissions, extraConfig } =
    opts;
  if (scope === "user") {
    return {
      agentConfig: {
        ...(model ? { model } : {}),
        ...(permissions ? { permissions } : {}),
        ...(workerOverride ? { workerPromptOverride: workerOverride } : {}),
        ...(orchOverride ? { orchestratorPromptOverride: orchOverride } : {}),
      },
      defaultWorkerPrompt: DEFAULT_WORKER,
      defaultOrchestratorPrompt: DEFAULT_ORCH,
    };
  }
  const config = {
    ...(extraConfig ?? {}),
    worker: { agent: "codex" },
    orchestrator: { agent: "claude-code" },
    ...(model || permissions
      ? {
          agentConfig: {
            ...(model ? { model } : {}),
            ...(permissions ? { permissions } : {}),
          },
        }
      : {}),
    ...(workerOverride ? { workerPromptOverride: workerOverride } : {}),
    ...(orchOverride ? { orchestratorPromptOverride: orchOverride } : {}),
  };
  return {
    status: "ok",
    project: {
      id: "proj-1",
      name: "Project One",
      kind: "single_repo",
      path: "/repo/project-one",
      repo: "",
      defaultBranch: "main",
      config,
    },
    defaultWorkerPrompt: DEFAULT_WORKER,
    defaultOrchestratorPrompt: DEFAULT_ORCH,
  };
}

// overridePath walks the PUT body to the override field for the scope, and the
// sibling "rest" fields that must survive the wholesale-replace save.
// apiClient's runtime fast-path passes a Request object straight to fetch, so
// the method/body live on the Request, not in RequestInit.
function putBody(handler: ReturnType<typeof fetchMock>) {
  const bodies = (
    handler as typeof handler & { putBodies?: Record<string, unknown>[] }
  ).putBodies;
  return bodies?.[0] ?? ({} as Record<string, unknown>);
}

function workerOverrideOf(scope: Scope, body: Record<string, unknown>) {
  if (scope === "user") {
    const ac = (body.agentConfig ?? {}) as Record<string, unknown>;
    return ac.workerPromptOverride;
  }
  const cfg = (body.config ?? {}) as Record<string, unknown>;
  return cfg.workerPromptOverride;
}

function orchOverrideOf(scope: Scope, body: Record<string, unknown>) {
  if (scope === "user") {
    const ac = (body.agentConfig ?? {}) as Record<string, unknown>;
    return ac.orchestratorPromptOverride;
  }
  const cfg = (body.config ?? {}) as Record<string, unknown>;
  return cfg.orchestratorPromptOverride;
}

function renderDialog(props: Partial<DialogProps> & { scope?: Scope }) {
  const scope = (props.scope ?? "user") as Scope;
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const full =
    scope === "user"
      ? ({
          open: true,
          onOpenChange: () => {},
          scope: "user",
          ...props,
        } as DialogProps)
      : ({
          open: true,
          onOpenChange: () => {},
          scope: "project",
          projectId: "proj-1",
          ...props,
        } as DialogProps);
  render(
    <QueryClientProvider client={qc}>
      <PromptOverrideDialog {...full} />
    </QueryClientProvider>,
  );
  return qc;
}

// fetchMock lets each test program GET/PUT responses for the scope URL.
// openapi-fetch returns the parsed JSON body directly as `data` (success) or
// `error` (non-2xx), so the Response body is the raw object, not wrapped in
// `{ data }`.
function fetchMock(getBody: unknown, putBody?: unknown) {
  const putBodies: Record<string, unknown>[] = [];
  const handler = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const request =
        input instanceof Request ? input : new Request(input, init);
      const method = request.method.toUpperCase();
      if (method === "PUT") {
        putBodies.push(
          JSON.parse((await request.text()) || "{}") as Record<string, unknown>,
        );
      }
      let body: unknown = getBody;
      if (method === "PUT") body = putBody ?? getBody;
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    },
  );
  (handler as typeof handler & { putBodies: Record<string, unknown>[] }).putBodies =
    putBodies;
  vi.stubGlobal("fetch", handler);
  return handler;
}

// waitForValue polls until a textarea labelled `label` holds `value`. The
// textarea mounts empty before the query resolves; findByLabelText would return
// immediately against the empty box, so we must wait for the prefilled value.
async function waitForValue(label: string, value: string) {
  await waitFor(() => {
    expect(screen.getByLabelText(label)).toHaveValue(value);
  });
}

beforeEach(() => {
  // Trust a base URL so apiClient actually issues fetch calls in the test env.
  setApiBaseUrl("http://127.0.0.1:3001");
});

afterEach(() => {
  vi.unstubAllGlobals();
  setApiBaseUrl(null);
  vi.restoreAllMocks();
});

// The two scopes share identical UI mechanics (prefill, empty-blocked, unchanged
// tracking, reset). Each test runs once per scope to prove the dialog behaves
// the same way regardless of where the override lives.
const SCOPES: Scope[] = ["user", "project"];

describe.each(SCOPES)("PromptOverrideDialog (scope=%s)", (scope) => {
  it("prefills the textareas with the assembled default prompts when no override is stored", async () => {
    fetchMock(buildGetBody(scope));

    renderDialog({ scope });

    await waitForValue("Worker prompt override", DEFAULT_WORKER);
    await waitForValue("Orchestrator prompt override", DEFAULT_ORCH);
  });

  it("prefills the stored override when one exists (override wins over default)", async () => {
    const override = "## Custom Worker\nDo the custom thing.";
    fetchMock(buildGetBody(scope, { workerOverride: override }));

    renderDialog({ scope });

    await waitForValue("Worker prompt override", override);
  });

  it("disables Save until a prompt is actually edited away from the initial value", async () => {
    const user = userEvent.setup();
    fetchMock(buildGetBody(scope));

    renderDialog({ scope });
    await waitForValue("Worker prompt override", DEFAULT_WORKER);

    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();

    const worker = screen.getByLabelText("Worker prompt override");
    await user.type(worker, " more");
    expect(save).toBeEnabled();

    await user.clear(worker);
    await user.type(worker, DEFAULT_WORKER);
    expect(save).toBeDisabled();
  });

  it("stores the override text when the prompt differs from the default, preserving the rest of the config", async () => {
    const user = userEvent.setup();
    const handler = fetchMock(
      buildGetBody(scope, { model: "claude-opus-4-8", permissions: "auto" }),
      scope === "user"
        ? { agentConfig: { workerPromptOverride: "edited" } }
        : { status: "ok", project: { config: {} } },
    );

    renderDialog({ scope });
    const worker = await screen.findByLabelText("Worker prompt override");
    await waitForValue("Worker prompt override", DEFAULT_WORKER);
    await user.clear(worker);
    await user.type(worker, "## Edited Worker\nNew instructions.");

    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(putBody(handler)).toBeTruthy());
    const body = putBody(handler);
    expect(workerOverrideOf(scope, body)).toBe(
      "## Edited Worker\nNew instructions.",
    );
    // Wholesale replace merges over the loaded config, so model/permissions
    // (user-scope) / worker+orchestrator agents (project-scope) survive.
    if (scope === "user") {
      const ac = (body.agentConfig ?? {}) as Record<string, unknown>;
      expect(ac.model).toBe("claude-opus-4-8");
      expect(ac.permissions).toBe("auto");
    } else {
      const cfg = (body.config ?? {}) as Record<string, unknown>;
      expect(cfg.model).toBeUndefined();
      const agentCfg = (cfg.agentConfig ?? {}) as Record<string, unknown>;
      expect(agentCfg.model).toBe("claude-opus-4-8");
      expect(agentCfg.permissions).toBe("auto");
      // The PUT must echo displayName and the surviving worker/orchestrator.
      expect(body.displayName).toBe("Project One");
      expect((cfg.worker as { agent: string }).agent).toBe("codex");
    }
  });

  it("clears an existing override back to the default by editing it back", async () => {
    const user = userEvent.setup();
    const handler = fetchMock(
      buildGetBody(scope, { workerOverride: "## Custom" }),
      scope === "user"
        ? { agentConfig: {} }
        : { status: "ok", project: { config: {} } },
    );

    renderDialog({ scope });
    const worker = await screen.findByLabelText("Worker prompt override");
    await waitForValue("Worker prompt override", "## Custom");
    await user.clear(worker);
    await user.type(worker, DEFAULT_WORKER);

    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(putBody(handler)).toBeTruthy());
    expect(workerOverrideOf(scope, putBody(handler))).toBeUndefined();
  });

  it("Reset to default clears both overrides and refills the textareas with the hardcoded baseline", async () => {
    const user = userEvent.setup();
    const handler = fetchMock(
      buildGetBody(scope, {
        model: "claude-opus-4-8",
        workerOverride: "## Custom Worker",
        orchOverride: "## Custom Orch",
      }),
      scope === "user"
        ? { agentConfig: { model: "claude-opus-4-8" } }
        : { status: "ok", project: { config: {} } },
    );

    renderDialog({ scope });
    await waitForValue("Worker prompt override", "## Custom Worker");
    await waitForValue("Orchestrator prompt override", "## Custom Orch");

    await user.click(screen.getByRole("button", { name: /reset to default/i }));

    await waitFor(() => expect(putBody(handler)).toBeTruthy());
    const body = putBody(handler);
    expect(workerOverrideOf(scope, body)).toBeUndefined();
    expect(orchOverrideOf(scope, body)).toBeUndefined();
    // model survives the wholesale-replace clear.
    if (scope === "user") {
      expect((body.agentConfig as { model: string }).model).toBe(
        "claude-opus-4-8",
      );
    } else {
      const cfg = body.config as { agentConfig?: { model?: string } };
      expect(cfg.agentConfig?.model).toBe("claude-opus-4-8");
    }

    await waitForValue("Worker prompt override", DEFAULT_WORKER);
    await waitForValue("Orchestrator prompt override", DEFAULT_ORCH);
  });

  it("shows a save error when the PUT fails", async () => {
    const user = userEvent.setup();
    const handler = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const request =
          input instanceof Request ? input : new Request(input, init);
        if (request.method.toUpperCase() === "PUT") {
          return new Response(
            JSON.stringify({ code: "internal_error", message: "boom" }),
            { status: 500, headers: { "content-type": "application/json" } },
          );
        }
        return new Response(JSON.stringify(buildGetBody(scope)), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      },
    );
    vi.stubGlobal("fetch", handler);

    renderDialog({ scope });
    const worker = await screen.findByLabelText("Worker prompt override");
    await waitForValue("Worker prompt override", DEFAULT_WORKER);
    await user.type(worker, " more");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("boom");
  });

  it("disables Save and shows a role-warning when the worker prompt is cleared", async () => {
    const user = userEvent.setup();
    fetchMock(buildGetBody(scope));

    renderDialog({ scope });
    const worker = await screen.findByLabelText("Worker prompt override");
    await waitForValue("Worker prompt override", DEFAULT_WORKER);
    await user.clear(worker);

    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();
    expect(screen.getByRole("alert")).toHaveTextContent(
      /A worker needs a system prompt/,
    );
  });

  it("disables Save and shows a role-warning when the orchestrator prompt is cleared", async () => {
    const user = userEvent.setup();
    fetchMock(buildGetBody(scope));

    renderDialog({ scope });
    const orchestrator = await screen.findByLabelText(
      "Orchestrator prompt override",
    );
    await waitForValue("Orchestrator prompt override", DEFAULT_ORCH);
    await user.clear(orchestrator);

    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();
    expect(screen.getByRole("alert")).toHaveTextContent(
      /An orchestrator needs a system prompt/,
    );
  });

  it("Reset to default refills both prompts and keeps Save disabled when already at default", async () => {
    const user = userEvent.setup();
    const handler = fetchMock(
      buildGetBody(scope),
      scope === "user"
        ? { agentConfig: {} }
        : { status: "ok", project: { config: {} } },
    );

    renderDialog({ scope });
    const worker = await screen.findByLabelText("Worker prompt override");
    await waitForValue("Worker prompt override", DEFAULT_WORKER);
    await user.clear(worker);
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();

    await user.click(screen.getByRole("button", { name: /reset to default/i }));

    await waitFor(() => expect(putBody(handler)).toBeTruthy());
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    await waitForValue("Worker prompt override", DEFAULT_WORKER);
  });

  it("disables Save when nothing changed, enables after an edit, then disables again after Reset to default", async () => {
    const user = userEvent.setup();
    const handler = fetchMock(
      buildGetBody(scope),
      scope === "user"
        ? { agentConfig: {} }
        : { status: "ok", project: { config: {} } },
    );

    renderDialog({ scope });
    const worker = await screen.findByLabelText("Worker prompt override");
    await waitForValue("Worker prompt override", DEFAULT_WORKER);

    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();

    await user.type(worker, " edited");
    expect(save).toBeEnabled();

    await user.click(screen.getByRole("button", { name: /reset to default/i }));
    await waitFor(() => expect(putBody(handler)).toBeTruthy());
    expect(save).toBeDisabled();
    await waitForValue("Worker prompt override", DEFAULT_WORKER);

    expect(
      screen.getByRole("button", { name: /reset to default/i }),
    ).toBeEnabled();
  });
});
