import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { projectQueryKey } from "../../hooks/useProjectQuery";
import { captureRendererEvent } from "../../lib/telemetry";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogTitle,
  settingsDialogBodyClass,
  settingsDialogContentClass,
  settingsDialogFooterClass,
  settingsDialogHeaderClass,
} from "../ui/dialog";

type AgentConfig = components["schemas"]["AgentConfig"];
type Project = components["schemas"]["Project"];
type ProjectConfig = components["schemas"]["ProjectConfig"];

export const userConfigQueryKey = ["user-config"] as const;

type PromptOverrideDialogProps =
  | {
      open: boolean;
      onOpenChange: (open: boolean) => void;
      scope: "user";
    }
  | {
      open: boolean;
      onOpenChange: (open: boolean) => void;
      scope: "project";
      projectId: string;
    };

// PromptLoad is the normalized read of whatever the scope's GET returns: the
// stored worker/orchestrator overrides plus the assembled hardcoded prompt
// baselines. Both scopes surface the same baseline fields so the editor prefills
// identically; only the path to the stored override differs.
type PromptLoad = {
  storedWorker: string;
  storedOrchestrator: string;
  defaultWorker: string;
  defaultOrchestrator: string;
};

// A scope adapter encapsulates the only things that differ between user- and
// project-scope: the GET query, the query key, the PUT mutation, the telemetry
// label, and the strings. Everything else (validation, unchanged-tracking,
// reset, dialog chrome) is shared. save receives only the two override values;
// it merges them over the config captured in the adapter's own closure during
// load, so the dialog does not have to thread the loaded config back through.
type ScopeAdapter = {
  scope: "user" | "project";
  queryKey: readonly unknown[];
  load: () => PromptLoad | Promise<PromptLoad>;
  save: (
    workerOverride: string | undefined,
    orchestratorOverride: string | undefined,
  ) => Promise<void>;
  title: string;
  description: string;
  hintFor: (role: "worker" | "orchestrator") => string;
};

function useScopeAdapter(props: PromptOverrideDialogProps): ScopeAdapter {
  const { t } = useTranslation();
  return useMemo<ScopeAdapter>(() => {
    const scopeLabel =
      props.scope === "user"
        ? t("settings.promptOverride.globalScope")
        : t("settings.promptOverride.projectScope");
    const sharedStrings = {
      title:
        props.scope === "user"
          ? t("settings.agentDefaults")
          : t("settings.project.agentDefaults"),
      description: t("settings.promptOverride.description", {
        scope: scopeLabel,
      }),
      hintFor: (role: "worker" | "orchestrator") =>
        t("settings.promptOverride.hint", {
          role: t(`settings.promptOverride.${role}`),
          scope: scopeLabel,
        }),
    };

    if (props.scope === "user") {
      let loadedAgentConfig: AgentConfig = {};
      return {
        scope: "user",
        queryKey: userConfigQueryKey,
        ...sharedStrings,
        async load() {
          const { data, error } = await apiClient.GET("/api/v1/user-config");
          if (error) throw new Error(apiErrorMessage(error));
          const agentConfig = (data.agentConfig ?? {}) as AgentConfig;
          loadedAgentConfig = agentConfig;
          return {
            storedWorker: agentConfig.workerPromptOverride ?? "",
            storedOrchestrator: agentConfig.orchestratorPromptOverride ?? "",
            defaultWorker: data.defaultWorkerPrompt ?? "",
            defaultOrchestrator: data.defaultOrchestratorPrompt ?? "",
          };
        },
        async save(workerOverride, orchestratorOverride) {
          // Wholesale replace: merge the two derived fields over the loaded
          // agentConfig so model/permissions/env/mcp/systemPrompt survive.
          const next: AgentConfig = {
            ...loadedAgentConfig,
            workerPromptOverride: workerOverride,
            orchestratorPromptOverride: orchestratorOverride,
          };
          const { error } = await apiClient.PUT("/api/v1/user-config", {
            body: { agentConfig: next },
          });
          if (error) throw new Error(apiErrorMessage(error));
        },
      };
    }

    const { projectId } = props;
    let loadedProject: { displayName: string; config: ProjectConfig } = {
      displayName: "",
      config: {} as ProjectConfig,
    };
    return {
      scope: "project",
      queryKey: projectQueryKey(projectId),
      ...sharedStrings,
      async load() {
        const { data, error } = await apiClient.GET("/api/v1/projects/{id}", {
          params: { path: { id: projectId } },
        });
        if (error) throw new Error(apiErrorMessage(error));
        if (data?.status !== "ok" || !data.project) {
          throw new Error("Project config is unavailable (degraded).");
        }
        // ProjectOrDegraded is oneOf; the ok-variant is Project. The runtime
        // shape under status:"ok" is the Project object.
        const project = data.project as Project;
        const config = (project.config ?? {}) as ProjectConfig;
        loadedProject = { displayName: project.name, config };
        return {
          storedWorker: config.workerPromptOverride ?? "",
          storedOrchestrator: config.orchestratorPromptOverride ?? "",
          defaultWorker: data.defaultWorkerPrompt ?? "",
          defaultOrchestrator: data.defaultOrchestratorPrompt ?? "",
        };
      },
      async save(workerOverride, orchestratorOverride) {
        // Wholesale replace like PUT /projects/{id}: merge the two derived
        // fields over the loaded config so model/permissions/agents/intake
        // survive, and echo the displayName.
        const config: ProjectConfig = {
          ...loadedProject.config,
          workerPromptOverride: workerOverride,
          orchestratorPromptOverride: orchestratorOverride,
        };
        const { error } = await apiClient.PUT("/api/v1/projects/{id}", {
          params: { path: { id: projectId } },
          body: { displayName: loadedProject.displayName, config },
        });
        if (error) throw new Error(apiErrorMessage(error));
      },
    };
  }, [props, t]);
}

/**
 * Prompt override editor, surfaced as a dialog (Report-a-problem pattern) and
 * shared by Global Settings (user-scope) and Project Settings (project-scope)
 * so the setting behaves identically everywhere it appears — same control, same
 * save/reset/validation mechanics. Reads/writes /api/v1/user-config for
 * user-scope and /api/v1/projects/{id} for project-scope.
 *
 * The textareas are prefilled with the FULL assembled default system prompt
 * served by the scope's GET (defaultWorkerPrompt / defaultOrchestratorPrompt),
 * so the user sees and edits the real hardcoded baseline rather than starting
 * from an empty box.
 *
 * Save semantics: if the edited text equals the default (trimmed), the override
 * is cleared (stored as undefined) so the hardcoded baseline is used; any other
 * text is stored as the override.
 *
 * "Reset to default" bypasses the compare-with-default dance: it refills the
 * textareas with the hardcoded baseline and writes an explicit
 * {workerPromptOverride: undefined, orchestratorPromptOverride: undefined} save
 * (merged over the loaded config so the rest survives).
 */
export function PromptOverrideDialog(props: PromptOverrideDialogProps) {
  const { open, onOpenChange } = props;
  const { t } = useTranslation();
  const workerId = useId();
  const orchestratorId = useId();
  const workerRef = useRef<HTMLTextAreaElement>(null);

  const adapter = useScopeAdapter(props);
  const queryClient = useQueryClient();

  const query = useQuery({
    queryKey: adapter.queryKey,
    queryFn: adapter.load,
  });

  const loaded = query.data;
  const defaultWorker = loaded?.defaultWorker ?? "";
  const defaultOrchestrator = loaded?.defaultOrchestrator ?? "";

  // The textarea shows the stored override when one exists, otherwise the full
  // assembled default baseline. The "displayed" value is what the user sees and
  // edits; it re-syncs whenever the server value or defaults load/change.
  const storedWorker = loaded?.storedWorker ?? "";
  const storedOrchestrator = loaded?.storedOrchestrator ?? "";
  const initialWorker = storedWorker || defaultWorker;
  const initialOrchestrator = storedOrchestrator || defaultOrchestrator;

  const [workerPrompt, setWorkerPrompt] = useState(initialWorker);
  const [orchestratorPrompt, setOrchestratorPrompt] =
    useState(initialOrchestrator);
  const [savedAt, setSavedAt] = useState<number | null>(null);

  // An override REPLACES the hardcoded system prompt wholesale. An empty field
  // would leave the agent with no prompt at all — it wouldn't know its role,
  // git rules, or session lifecycle. Block Save when either textarea is empty
  // and surface a warning explaining why (Reset to default refills non-empty
  // defaults, so it stays usable).
  const workerEmpty = workerPrompt.trim() === "";
  const orchestratorEmpty = orchestratorPrompt.trim() === "";
  const promptInvalid = workerEmpty || orchestratorEmpty;
  const [validationError, setValidationError] = useState<string | null>(null);

  // The textareas start at initialWorker/initialOrchestrator (the stored
  // override, or the hardcoded baseline when no override is stored). If neither
  // field has been edited there is nothing to save, so disable Save — the same
  // value would round-trip and (if it equals the default) the override would be
  // cleared for nothing. Reset to default ignores this and stays always-on.
  const workerUnchanged = workerPrompt.trim() === initialWorker.trim();
  const orchestratorUnchanged =
    orchestratorPrompt.trim() === initialOrchestrator.trim();
  const promptUnchanged = workerUnchanged && orchestratorUnchanged;

  // Re-sync the form when the dialog opens or the server value/defaults change.
  useEffect(() => {
    if (!open) return;
    setWorkerPrompt(storedWorker || defaultWorker);
    setOrchestratorPrompt(storedOrchestrator || defaultOrchestrator);
    setSavedAt(null);
  }, [
    open,
    storedWorker,
    storedOrchestrator,
    defaultWorker,
    defaultOrchestrator,
  ]);

  // Save the two overrides. Pass an explicit `{ worker, orchestrator }` to
  // bypass the textarea state — used by "Reset to default" so the override is
  // cleared regardless of the current textarea text (state updates are async,
  // so mutating off the just-set state would race).
  const mutation = useMutation({
    mutationFn: async (overrides?: {
      worker?: string;
      orchestrator?: string;
    }) => {
      if (!loaded) return;
      const explicit = overrides !== undefined;
      const scope = adapter.scope;
      void captureRendererEvent(
        explicit
          ? "ao.renderer.prompt_override_reset_to_default"
          : "ao.renderer.prompt_override_save_requested",
        { scope },
      );
      // If the edited text equals the default (trimmed), clear the override so
      // the hardcoded baseline is used; any other text becomes the override.
      const workerOverride = explicit
        ? overrides!.worker
        : workerPrompt.trim() && workerPrompt.trim() !== defaultWorker.trim()
          ? workerPrompt.trim()
          : undefined;
      const orchestratorOverride = explicit
        ? overrides!.orchestrator
        : orchestratorPrompt.trim() &&
            orchestratorPrompt.trim() !== defaultOrchestrator.trim()
          ? orchestratorPrompt.trim()
          : undefined;
      await adapter.save(workerOverride, orchestratorOverride);
    },
    onSuccess: () => {
      void captureRendererEvent("ao.renderer.prompt_override_save_succeeded", {
        scope: adapter.scope,
      });
      setSavedAt(Date.now());
      void queryClient.invalidateQueries({ queryKey: adapter.queryKey });
    },
    onError: () => {
      void captureRendererEvent("ao.renderer.prompt_override_save_failed", {
        scope: adapter.scope,
      });
    },
  });

  const { title, description, hintFor } = adapter;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        showCloseButton={false}
        className={settingsDialogContentClass}
        onOpenAutoFocus={(event) => {
          event.preventDefault();
          workerRef.current?.focus();
        }}
        onKeyDown={(event) => {
          // Cmd/Ctrl+Enter saves; a plain Enter keeps inserting newlines.
          if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
            event.preventDefault();
            if (promptInvalid) {
              setValidationError(t("settings.promptOverride.empty"));
              return;
            }
            // No edits since the dialog opened — nothing to save, this is not
            // a validation error so just bail silently.
            if (promptUnchanged) return;
            setValidationError(null);
            setSavedAt(null);
            mutation.mutate(undefined);
          }
        }}
      >
        <DialogClose asChild>
          <button
            type="button"
            className="settings-dialog-close-button settings-close-button"
            aria-label={t("settings.promptOverride.closeAria")}
            title={t("settings.promptOverride.closeTitle")}
          >
            <X className="size-5" aria-hidden="true" />
          </button>
        </DialogClose>

        <div className={settingsDialogHeaderClass}>
          <DialogTitle className="settings-dialog-title">{title}</DialogTitle>
          <DialogDescription className="text-control leading-4 text-settings-muted">
            {description}
          </DialogDescription>
        </div>

        <div className={settingsDialogBodyClass}>
          {query.isLoading && (
            <p className="text-caption leading-4 text-settings-muted">
              {t("settings.promptOverride.loading")}
            </p>
          )}
          {query.isError && (
            <p role="alert" className="text-caption leading-4 text-error">
              {query.error instanceof Error
                ? query.error.message
                : t("settings.promptOverride.loadFailed")}
            </p>
          )}

          <div className="flex flex-col gap-1.5">
            <label className="settings-field-label" htmlFor={workerId}>
              {t("settings.promptOverride.worker")}
            </label>
            <textarea
              ref={workerRef}
              id={workerId}
              className="settings-field-control min-h-(--size-textarea-min) resize-y py-2.5 font-mono text-caption"
              value={workerPrompt}
              onChange={(e) => {
                setWorkerPrompt(e.target.value);
                setSavedAt(null);
              }}
              disabled={query.isLoading}
              spellCheck={false}
            />
            <span className="text-caption leading-4 text-settings-muted">
              {hintFor("worker")}
            </span>
            {workerEmpty && (
              <p role="alert" className="text-caption leading-4 text-error">
                {t("settings.promptOverride.workerEmpty")}
              </p>
            )}
          </div>

          <div className="flex flex-col gap-1.5">
            <label className="settings-field-label" htmlFor={orchestratorId}>
              {t("settings.promptOverride.orchestrator")}
            </label>
            <textarea
              id={orchestratorId}
              className="settings-field-control min-h-(--size-textarea-min) resize-y py-2.5 font-mono text-caption"
              value={orchestratorPrompt}
              onChange={(e) => {
                setOrchestratorPrompt(e.target.value);
                setSavedAt(null);
              }}
              disabled={query.isLoading}
              spellCheck={false}
            />
            <span className="text-caption leading-4 text-settings-muted">
              {hintFor("orchestrator")}
            </span>
            {orchestratorEmpty && (
              <p role="alert" className="text-caption leading-4 text-error">
                {t("settings.promptOverride.orchestratorEmpty")}
              </p>
            )}
          </div>

          {mutation.isError && (
            <p role="alert" className="text-caption leading-4 text-error">
              {mutation.error instanceof Error
                ? mutation.error.message
                : t("settings.promptOverride.saveFailed")}
            </p>
          )}
          {savedAt && !mutation.isPending && !mutation.isError && (
            <p className="text-caption leading-4 text-success">
              {t("settings.promptOverride.saved")}
            </p>
          )}
        </div>

        <div className={settingsDialogFooterClass}>
          <button
            type="button"
            className="settings-footer-button mr-auto"
            disabled={mutation.isPending || query.isLoading}
            aria-label={t("settings.promptOverride.resetAria")}
            title={t("settings.promptOverride.resetTitle")}
            onClick={() => {
              // Refill the textareas with the hardcoded baseline, then clear
              // the stored override explicitly. Explicit undefined is passed
              // so the override is wiped regardless of the textarea state
              // (state updates are async and would race the mutation closure).
              setWorkerPrompt(defaultWorker);
              setOrchestratorPrompt(defaultOrchestrator);
              setSavedAt(null);
              setValidationError(null);
              mutation.mutate({ worker: undefined, orchestrator: undefined });
            }}
          >
            {t("settings.promptOverride.reset")}
          </button>
          {validationError && promptInvalid && (
            <span className="text-caption leading-4 text-error">
              {validationError}
            </span>
          )}
          <DialogClose asChild>
            <button type="button" className="settings-footer-button">
              {t("confirm.cancel")}
            </button>
          </DialogClose>
          <button
            type="button"
            className="settings-footer-button border-transparent bg-settings-accent text-white disabled:cursor-not-allowed disabled:opacity-50"
            disabled={
              promptInvalid ||
              promptUnchanged ||
              mutation.isPending ||
              query.isLoading
            }
            onClick={() => {
              if (promptInvalid) {
                setValidationError(t("settings.promptOverride.empty"));
                return;
              }
              setValidationError(null);
              setSavedAt(null);
              mutation.mutate(undefined);
            }}
          >
            {mutation.isPending
              ? t("settings.promptOverride.saving")
              : t("settings.promptOverride.save")}
          </button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
