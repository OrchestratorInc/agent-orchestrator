import {
  ChevronDown,
  ChevronRight,
  MoreVertical,
  LoaderCircle,
  Pencil,
  Plus,
  RefreshCw,
  Trash2,
} from "lucide-react";
import { useCallback, useEffect, useId, useRef, useState, type RefObject } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { MessageKey } from "../../i18n";
import { aoBridge } from "../../lib/bridge";
import { AccountControlError, accountControlMessage, readAccountRemovalReferences } from "../../lib/accounts-manager-controls";
import {
  accountsManagerQueryKey,
  addAccountsManagerAPIKey,
  cancelAccountsManagerOAuth,
  importAccountsManagerCredential,
  refreshAccountsManagerAccount,
  renameAccountsManagerAccount,
  selectAccountsManagerSnapshot,
  setAccountsManagerDisabled,
  startAccountsManagerOAuth,
  updateAccountsManagerRouting,
  useAccountsManagerEvents,
  useAccountsManagerQuery,
  type AccountsManagerAccount,
  type AccountsManagerSnapshot,
} from "../../hooks/useAccountsManagerQuery";
import { Button } from "../ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { Input } from "../ui/input";
import { Switch } from "../ui/switch";
import { AgentProviderGroup } from "./AgentProviderGroup";
import { SettingsOptionMenu, type SettingsOption } from "./SettingsOptionMenu";
import { SettingsSection } from "./SettingsSection";
import { AccountRemovalDialog, AccountRemovalReconciler } from "./AccountRemovalControl";
import { AccountUsage, prefetchAccountUsage } from "./AccountUsage";
import { SignInBrowserRecovery, signInAuthorizationURL } from "./SignInBrowserRecovery";

type Provider = "codex" | "claude";
type AddMethod = "device" | "browser" | "api-key" | "json";

function providerLabel(provider: string): string {
  return provider.charAt(0).toUpperCase() + provider.slice(1);
}

export function AccountsManagerSection({
  titleHidden,
}: {
  titleHidden?: boolean;
}) {
  const { t } = useTranslation();
  const providerFilterOptions = [
    { value: "all", label: t("accountsManager.filter.providers") },
    { value: "codex", label: providerLabel("codex") },
    { value: "claude", label: providerLabel("claude") },
  ] satisfies SettingsOption<string>[];
  const stateFilterOptions = [
    { value: "all", label: t("accountsManager.filter.states") },
    { value: "ready", label: t("accountsManager.filter.available") },
    { value: "disabled", label: t("accountsManager.status.disabled") },
    { value: "attention", label: t("accountsManager.status.error") },
  ] satisfies SettingsOption<string>[];
  const query = useAccountsManagerQuery();
  useAccountsManagerEvents();
  const client = useQueryClient();
  const [pendingRemovals, setPendingRemovals] = useState<Record<string, boolean>>(() => {
    try {
      return Object.fromEntries(readAccountRemovalReferences().map(reference => [reference.accountId, true]));
    } catch {
      return {};
    }
  });
  const markRemovalPending = useCallback((accountId: string) => {
    setPendingRemovals(current => ({ ...current, [accountId]: true }));
  }, []);
  const clearRemovalPending = useCallback((accountId: string) => {
    setPendingRemovals(current => {
      const next = { ...current };
      delete next[accountId];
      return next;
    });
  }, []);
  const [expanded, setExpanded] = useState<Record<Provider, boolean>>({
    codex: true,
    claude: true,
  });
  const [adding, setAdding] = useState<Provider | null>(null);
  const [reconnecting, setReconnecting] =
    useState<AccountsManagerAccount | null>(null);
  const [method, setMethod] = useState<AddMethod>("device");
  const [secret, setSecret] = useState("");
  const [baseURL, setBaseURL] = useState("");
  const [search, setSearch] = useState("");
  const [providerFilter, setProviderFilter] = useState("all");
  const [stateFilter, setStateFilter] = useState("all");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<MessageKey | null>(null);
  const [routingBusy, setRoutingBusy] = useState(false);
  const [routingError, setRoutingError] = useState<{
    provider: Provider;
    requestId: string;
  } | null>(null);
  const [requestId, setRequestId] = useState("");
  const showError = (message: MessageKey, cause: unknown) => {
    setError(cause instanceof AccountControlError && cause.code === "ACCOUNTS_MANAGER_CREDENTIAL_METHOD_UNSUPPORTED" ? "accountsManager.credentials.wrongMethod"
      : cause instanceof AccountControlError && cause.code === "ACCOUNTS_MANAGER_INVALID_CREDENTIAL" ? "accountsManager.verification.rejected"
      : cause instanceof AccountControlError && cause.code === "ACCOUNTS_MANAGER_VERIFICATION_UNAVAILABLE" ? "accountsManager.verification.unavailable" : message);
    setRequestId(cause instanceof AccountControlError ? cause.requestId : "");
  };
  const fileRef = useRef<HTMLInputElement>(null);
  const cancelledOAuth = useRef(new Set<string>());
  const cancellations = useRef(new Map<string, Promise<void>>());
  const addGeneration = useRef(0);
  const activeOAuthID = useRef<string | null>(null);
  const [startedOAuth, setStartedOAuth] = useState<AccountsManagerSnapshot["oauthSessions"][number] | null>(null);
  const data = query.data;
  const update = (next: AccountsManagerSnapshot) =>
    client.setQueryData<AccountsManagerSnapshot>(
      accountsManagerQueryKey,
      (current) => selectAccountsManagerSnapshot(current, next),
    );
  const clearSensitive = () => {
    setSecret("");
    if (fileRef.current) fileRef.current.value = "";
  };
  const cancelOAuthOnce = async (id: string) => {
    if (cancelledOAuth.current.has(id)) return;
    let pending = cancellations.current.get(id);
    if (!pending) {
      pending = cancelAccountsManagerOAuth(id)
        .then(() => {
          cancelledOAuth.current.add(id);
        })
        .finally(() => cancellations.current.delete(id));
      cancellations.current.set(id, pending);
    }
    await pending;
  };

  const startOAuth = async (
    provider: Provider,
    mode: "device" | "callback",
  ) => {
    setBusy(true);
    setError(null);
    const generation = addGeneration.current;
    try {
      const session = reconnecting
        ? await startAccountsManagerOAuth(provider, mode, {
            accountId: reconnecting.id,
            generation: reconnecting.generation,
          })
        : await startAccountsManagerOAuth(provider, mode);
      if (generation !== addGeneration.current) {
        try {
          await cancelOAuthOnce(session.id);
        } catch (cause) {
          activeOAuthID.current = session.id;
          setAdding(provider);
          showError("accountsManager.errors.cancel", cause);
        }
        return;
      }
      activeOAuthID.current = session.id;
      const authorizationURL = signInAuthorizationURL(session.authorizationUrl);
      if (!authorizationURL) {
        await cancelOAuthOnce(session.id).catch(() => undefined);
        throw new Error("Missing authorization URL");
      }
      setStartedOAuth(session);
      try {
        await aoBridge.app.openExternal(authorizationURL);
      } catch {
        if (generation === addGeneration.current) {
          setError("accountsManager.browser.openFailed");
          setRequestId("");
        }
      }
    } catch (cause) {
      if (generation === addGeneration.current) showError("accountsManager.errors.start", cause);
    } finally {
      if (generation === addGeneration.current) setBusy(false);
    }
  };
  const submitKey = async (provider: Provider) => {
    setBusy(true);
    setError(null);
    try {
      update(
        await addAccountsManagerAPIKey(provider, secret, baseURL || undefined),
      );
      setAdding(null);
    } catch (cause) {
      showError("accountsManager.errors.addKey", cause);
    } finally {
      clearSensitive();
      setBusy(false);
    }
  };
  const submitFile = async (provider: Provider, file?: File) => {
    if (!file) return;
    setBusy(true);
    setError(null);
    try {
      if (file.size > 1_048_576 || !file.name.toLowerCase().endsWith(".json"))
        throw new Error("invalid");
      const parsed: unknown = JSON.parse(await file.text());
      if (!parsed || Array.isArray(parsed) || typeof parsed !== "object")
        throw new Error("invalid");
      update(
        await importAccountsManagerCredential(
          provider,
          file.name,
          parsed as Record<string, unknown>,
        ),
      );
      setAdding(null);
    } catch (cause) {
      showError("accountsManager.errors.import", cause);
    } finally {
      clearSensitive();
      setBusy(false);
    }
  };

  const waiting = data?.oauthSessions.find(
    (session) => session.status === "pending",
  ) ?? (startedOAuth && !data?.oauthSessions.some(session => session.id === startedOAuth.id) ? startedOAuth : undefined);
  useEffect(() => {
    if (startedOAuth && data?.oauthSessions.some(session => session.id === startedOAuth.id)) setStartedOAuth(null);
  }, [data?.oauthSessions, startedOAuth]);
  useEffect(() => {
    if (waiting) {
      activeOAuthID.current ??= waiting.id;
      setAdding(waiting.provider as Provider);
      setReconnecting(
        data?.accounts.find((account) => account.id === waiting.accountId) ??
          null,
      );
      setMethod(waiting.mode === "device" ? "device" : "browser");
    }
  }, [waiting?.id]);
  useEffect(() => {
    const active = data?.oauthSessions.find(
      (session) => session.id === activeOAuthID.current,
    );
    if (
      active?.status === "completed" ||
      (active?.status === "expired" && active.failureCode === "cancelled")
    ) {
      activeOAuthID.current = null;
      setStartedOAuth(null);
      setAdding(null);
      setReconnecting(null);
      setError(null);
    }
    if (
      active?.status === "failed" ||
      (active?.status === "expired" && active.failureCode !== "cancelled")
    ) {
      activeOAuthID.current = null;
      setStartedOAuth(null);
      setError(
        active.status === "expired"
          ? "accountsManager.errors.expired"
          : active.failureCode === "identity_mismatch"
            ? "accountsManager.errors.identityMismatch"
            : active.failureCode === "credential_changed"
              ? "accountsManager.errors.credentialChanged"
              : active.failureCode === "storage_unavailable"
                ? "accountsManager.errors.storage"
                : "accountsManager.errors.signIn",
      );
    }
  }, [data?.oauthSessions]);
  const closeAdd = async (provider: Provider) => {
    addGeneration.current++;
    const pending = data?.oauthSessions.find(
      (session) =>
        session.provider === provider && session.status === "pending",
    );
    const id = pending?.id ?? activeOAuthID.current;
    if (id) {
      setBusy(true);
      try {
        await cancelOAuthOnce(id);
      } catch (cause) {
        showError("accountsManager.errors.cancel", cause);
        return;
      } finally {
        setBusy(false);
      }
    }
    activeOAuthID.current = null;
    setStartedOAuth(null);
    setBusy(false);
    setAdding(null);
    setReconnecting(null);
    setError(null);
    clearSensitive();
  };
  const saveRouting = async (
    provider: Provider,
    enabled: boolean,
    accountIds: string[],
  ) => {
    setRoutingBusy(true);
    setRoutingError(null);
    try {
      update(await updateAccountsManagerRouting(provider, enabled, accountIds));
    } catch (cause) {
      setRoutingError({
        provider,
        requestId: cause instanceof AccountControlError ? cause.requestId : "",
      });
    } finally {
      setRoutingBusy(false);
    }
  };

  return (
    <>
    <AccountRemovalReconciler onPending={markRemovalPending} onSettled={clearRemovalPending} />
    <SettingsSection
      title={t("accountsManager.title")}
      titleHidden={titleHidden}
    >
      <div className="space-y-5">
        {query.error ? <p role="alert" className="text-sm text-destructive">{accountControlMessage(query.error, t)}</p> : null}
        {error && requestId ? <p role="alert" className="text-xs text-destructive">{t("accountsManager.controls.requestId", { id: requestId })}</p> : null}
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
          <Input
            className="min-w-0 flex-1"
            aria-label={t("accountsManager.search")}
            placeholder={t("accountsManager.search")}
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
          <SettingsOptionMenu
            aria-label={t("accountsManager.filter.provider")}
            value={providerFilter}
            options={providerFilterOptions}
            onChange={setProviderFilter}
            triggerClassName="w-fit"
          />
          <SettingsOptionMenu
            aria-label={t("accountsManager.filter.state")}
            value={stateFilter}
            options={stateFilterOptions}
            onChange={setStateFilter}
            triggerClassName="w-fit"
          />
        </div>
        {data?.stale ? (
          <p className="text-xs text-muted-foreground">
            {t("accountsManager.stale")}
          </p>
        ) : null}
        {(["codex", "claude"] as const)
          .filter(
            (provider) =>
              providerFilter === "all" ||
              providerFilter === provider ||
              adding === provider,
          )
          .map((provider) => {
            const accounts =
              data?.accounts.filter(
                (account) => account.provider === provider,
              ) ?? [];
            const displayAccounts = accounts.filter((account) => !pendingRemovals[account.id]);
            const hasAccounts = displayAccounts.length > 0;
            const visible = displayAccounts.filter((account) => {
              const matchesSearch =
                `${account.label ?? ""} ${account.email ?? ""} ${account.id}`
                  .toLocaleLowerCase()
                  .includes(search.trim().toLocaleLowerCase());
              const state = account.disabled
                ? "disabled"
                : account.unavailable || account.status !== "active"
                  ? "attention"
                  : "ready";
              return (
                matchesSearch &&
                (stateFilter === "all" || stateFilter === state)
              );
            });
            const unavailable =
              !data || data.availability !== "ready" || data.stale || query.isError;
            const routing = data?.routing?.find(
              (policy) => policy.provider === provider,
            ) ?? { provider, enabled: false, accountIds: [] };
            const selectedRoutingId =
              routing.accountIds.length === 1 ? routing.accountIds[0] : undefined;
            const selectedRoutingAccount = accounts.find(
              (account) => account.id === selectedRoutingId,
            );
            const selectedRoutingAccountEligible = Boolean(
              selectedRoutingAccount &&
                selectedRoutingAccount.verification === "verified" &&
                !selectedRoutingAccount.disabled &&
                !selectedRoutingAccount.unavailable &&
                selectedRoutingAccount.status === "active",
            );
            return (
              <AgentProviderGroup
                key={provider}
                provider={provider}
                name={providerLabel(provider)}
                summary={displayAccounts.length ? t("accountsManager.saved", { count: displayAccounts.length }) : undefined}
                collapsible={hasAccounts}
                expanded={hasAccounts ? expanded[provider] : adding === provider}
                onExpandedChange={(value) =>
                  setExpanded((current) => ({ ...current, [provider]: value }))
                }
                action={
                  adding === provider ? (
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={
                        unavailable ||
                        (busy && (method === "api-key" || method === "json")) ||
                        Boolean(waiting && cancellations.current.has(waiting.id))
                      }
                      onClick={() => void closeAdd(provider)}
                    >
                      {t("confirm.cancel")}
                    </Button>
                  ) : (
                    <Button
                      size={hasAccounts ? "icon" : "sm"}
                      variant={hasAccounts ? "ghost" : "outline"}
                      disabled={unavailable || busy || Boolean(waiting)}
                      aria-label={
                        t("accountsManager.addProvider", { provider })
                      }
                      onClick={() => {
                        clearSensitive();
                        setBaseURL("");
                        addGeneration.current++;
                        setAdding(provider);
                        setReconnecting(null);
                        setMethod(provider === "codex" ? "device" : "browser");
                        setError(null);
                      }}
                    >
                      {hasAccounts ? <Plus className="size-4" /> : t("accountsManager.addFirst")}
                    </Button>
                  )
                }
              >
                {hasAccounts ? <RoutingPanel
                  provider={provider}
                  policy={routing}
                  disabled={unavailable}
                  selected={selectedRoutingAccount}
                  canEnable={selectedRoutingAccountEligible}
                  busy={busy || routingBusy}
                  error={routingError?.provider === provider ? "accountsManager.errors.routing" : null}
                  requestId={routingError?.provider === provider ? routingError.requestId : ""}
                  save={(enabled, accountIds) => saveRouting(provider, enabled, accountIds)}
                /> : null}
                {adding === provider ? (
                  <AddAccountPanel
                    provider={provider}
                    reconnecting={reconnecting}
                    method={method}
                    setMethod={(next) => {
                      if (next !== "api-key") clearSensitive();
                      setMethod(next);
                    }}
                    secret={secret}
                    setSecret={setSecret}
                    baseURL={baseURL}
                    setBaseURL={setBaseURL}
                    busy={busy || unavailable}
                    waiting={
                      waiting?.provider === provider ? waiting : undefined
                    }
                    error={error}
                    dismissError={() => {
                      setError(null);
                      clearSensitive();
                    }}
                    browserOpened={() => {
                      if (activeOAuthID.current === waiting?.id) setError(null);
                    }}
                    startOAuth={(mode) => void startOAuth(provider, mode)}
                    submitKey={() => void submitKey(provider)}
                    fileRef={fileRef}
                    submitFile={(file) => void submitFile(provider, file)}
                  />
                ) : null}
                {visible.length ? (
                  visible.map((account) => (
                    <AccountRow
                      key={account.id}
                      account={account}
                      removalPending={Boolean(pendingRemovals[account.id])}
                      onRemovalStart={() => markRemovalPending(account.id)}
                      onRemovalFailed={() => clearRemovalPending(account.id)}
                      disabled={unavailable}
                      signInDisabled={busy || Boolean(waiting)}
                      isDefault={routing.accountIds.length === 1 && routing.accountIds[0] === account.id}
                      routingEnabled={routing.enabled}
                      routingBusy={routingBusy}
                      canSetDefault={account.verification === "verified" && !account.disabled && !account.unavailable && account.status === "active"}
                      setDefault={(accountId) => void saveRouting(provider, routing.enabled, accountId ? [accountId] : [])}
                      update={update}
                      reconnect={() => {
                        clearSensitive();
                        addGeneration.current++;
                        setReconnecting(account);
                        setAdding(provider);
                        setMethod(provider === "codex" ? "device" : "browser");
                        setError(null);
                      }}
                    />
                  ))
                ) : displayAccounts.length ? (
                  <p className="px-4 py-5 text-sm text-muted-foreground">
                    {t("accountsManager.noMatches")}
                  </p>
                ) : unavailable ? (
                  <p className="px-4 py-5 text-sm text-muted-foreground">
                    {t("accountsManager.loading")}
                  </p>
                ) : null}
              </AgentProviderGroup>
            );
          })}
      </div>
    </SettingsSection>
    </>
  );
}

function RoutingPanel({
  provider,
  policy,
  disabled,
  selected,
  canEnable,
  busy,
  error,
  requestId,
  save,
}: {
  provider: Provider;
  policy: { enabled: boolean; accountIds: string[] };
  disabled: boolean;
  selected?: AccountsManagerAccount;
  canEnable: boolean;
  busy: boolean;
  error: MessageKey | null;
  requestId: string;
  save: (enabled: boolean, accountIds: string[]) => void;
}) {
  const { t } = useTranslation();
  const toggle = (checked: boolean) => {
    if (checked && (!selected || !canEnable)) return;
    save(checked, selected ? [selected.id] : []);
  };

  return (
    <div className="border-b border-border px-4 py-3.5">
      <div className="flex items-start justify-between gap-4">
        <div>
          <p className="text-sm font-medium">
            {t("accountsManager.routing.title")}
          </p>
          <p className="mt-1 text-xs text-muted-foreground">
            {t("accountsManager.routing.description")}
          </p>
          {provider === "codex" ? (
            <p className="mt-1 text-xs text-muted-foreground">
              {t("accountsManager.routing.nativeChat")}
            </p>
          ) : null}
        </div>
        <Switch
          aria-label={t("accountsManager.routing.toggle", { provider })}
          checked={policy.enabled}
          disabled={
            disabled ||
            busy ||
            (!policy.enabled && (!selected || !canEnable))
          }
          onCheckedChange={toggle}
        />
      </div>
      {error ? <p className="mt-2 text-xs text-destructive">{t(error)}</p> : null}
      {error && requestId ? <p role="alert" className="text-xs text-destructive">{t("accountsManager.controls.requestId", { id: requestId })}</p> : null}
    </div>
  );
}

function AddAccountPanel(props: {
  provider: Provider;
  reconnecting: AccountsManagerAccount | null;
  method: AddMethod;
  setMethod: (v: AddMethod) => void;
  secret: string;
  setSecret: (v: string) => void;
  baseURL: string;
  setBaseURL: (v: string) => void;
  busy: boolean;
  waiting?: AccountsManagerSnapshot["oauthSessions"][number];
  error: MessageKey | null;
  dismissError: () => void;
  browserOpened: () => void;
  startOAuth: (mode: "device" | "callback") => void;
  submitKey: () => void;
  fileRef: RefObject<HTMLInputElement | null>;
  submitFile: (file?: File) => void;
}) {
  const { t } = useTranslation();
  const [chosenMethod, setChosenMethod] = useState<AddMethod | null>(null);
  const methods: Array<{ method: AddMethod; title: string; description: string }> = [
    ...(props.provider === "codex"
      ? [{
          method: "device" as const,
          title: t("accountsManager.method.device"),
          description: t("accountsManager.method.device.description"),
        }]
      : []),
    {
      method: "browser",
      title: t("accountsManager.method.browser"),
      description: t("accountsManager.method.browser.description"),
    },
    ...(props.reconnecting
      ? []
      : [
          {
            method: "api-key" as const,
            title: t("accountsManager.apiKey"),
            description: t("accountsManager.method.apiKey.description"),
          },
          {
            method: "json" as const,
            title: t("accountsManager.method.json"),
            description: t("accountsManager.method.json.description"),
          },
        ]),
  ];
  const selectMethod = (method: AddMethod) => {
    setChosenMethod(method);
    props.setMethod(method);
    if (method === "device") props.startOAuth("device");
    if (method === "browser") props.startOAuth("callback");
    if (method === "json") {
      requestAnimationFrame(() => {
        requestAnimationFrame(() => props.fileRef.current?.click());
      });
    }
  };
  return (
    <div className="space-y-4 border-b border-border bg-muted/20 px-4 py-5">
      {props.reconnecting ? (
        <p className="text-sm" role="status">
          {t("accountsManager.reconnect.description", {
            account:
              props.reconnecting.label ||
              props.reconnecting.email ||
              props.reconnecting.id.slice(-6),
          })}
        </p>
      ) : null}
      {props.waiting ? (
        <DeviceOrBrowserWaiting key={props.waiting.id} session={props.waiting} browserOpened={props.browserOpened} />
      ) : (
        <div className="space-y-3">
          <p className="text-sm font-medium text-foreground">{t("accountsManager.addPrompt")}</p>
          <div className="flex flex-col gap-2" role="group" aria-label={t("accountsManager.addPrompt")}>
            {methods.map(({ method, title, description }) => {
              const isActive =
                chosenMethod === method ||
                (method === "api-key" && props.method === "api-key");
              const isStartingOAuth =
                chosenMethod === method &&
                props.busy &&
                (method === "device" || method === "browser");
              return (
                <Button
                  key={method}
                  size="none"
                  type="button"
                  variant={isActive ? "secondary" : "outline"}
                  className={`min-h-16 w-full justify-start whitespace-normal px-3 py-4 text-left transition-[background-color,border-color] active:scale-100 active:translate-y-0 active:bg-[var(--color-bg-settings-row-hover)] active:border-ring focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 ${isActive ? "ring-1 ring-ring" : ""}`}
                  disabled={props.busy}
                  aria-pressed={method === "api-key" ? props.method === method : undefined}
                  onClick={() => selectMethod(method)}
                >
                  <span className="flex min-w-0 flex-col items-start gap-1">
                    <span className="flex items-center gap-2 font-medium">
                      {isStartingOAuth ? <LoaderCircle aria-hidden="true" className="size-4 animate-spin" /> : null}
                      {title}
                    </span>
                    <span className="text-xs font-normal text-muted-foreground">{description}</span>
                  </span>
                </Button>
              );
            })}
          </div>
          <input
            ref={props.fileRef}
            className="sr-only"
            type="file"
            aria-label={t("accountsManager.method.json")}
            aria-hidden="true"
            accept="application/json,.json"
            disabled={props.busy}
            tabIndex={-1}
            onChange={(event) => props.submitFile(event.currentTarget.files?.[0])}
          />
          {props.method === "api-key" ? (
            <div className="space-y-3 rounded-md border border-border bg-background/50 p-3">
              <Input
                aria-label={t("accountsManager.apiKey")}
                autoComplete="off"
                placeholder={t("accountsManager.apiKey")}
                type="password"
                value={props.secret}
                onChange={(event) => props.setSecret(event.target.value)}
              />
              <Input
                aria-label={t("accountsManager.baseURL")}
                placeholder={t("accountsManager.baseURLPlaceholder")}
                value={props.baseURL}
                onChange={(event) => props.setBaseURL(event.target.value)}
              />
              <p className="text-xs text-muted-foreground">{t("accountsManager.credentials.keyFormHint")}</p>
              {props.secret.trim() ? (
                <div className="flex justify-end border-t border-border pt-3">
                  <Button disabled={props.busy} onClick={props.submitKey}>
                    {t("accountsManager.add")}
                  </Button>
                </div>
              ) : null}
            </div>
          ) : null}
        </div>
      )}
      {props.error ? (
        <div className="flex items-center gap-2 text-sm text-destructive">
          <span>{t(props.error)}</span>
          <Button size="sm" variant="ghost" onClick={props.dismissError}>
            {t("accountsManager.dismiss")}
          </Button>
        </div>
      ) : null}
    </div>
  );
}

function DeviceOrBrowserWaiting({
  session,
  browserOpened,
}: {
  session: AccountsManagerSnapshot["oauthSessions"][number];
  browserOpened: () => void;
}) {
  const { t } = useTranslation();
  const userCode = session?.userCode;
  return (
    <div className="space-y-4 rounded-lg border border-border bg-background/40 p-4">
      {userCode ? (
        <div className="space-y-2">
          <p className="text-sm font-medium text-foreground">
            {t("accountsManager.device.enterCode")}
          </p>
          <div className="flex flex-wrap items-center gap-2">
            <code className="inline-flex h-control-form items-center rounded-md bg-background px-3 text-base font-semibold tracking-wider">
              {userCode}
            </code>
            <Button
              size="none"
              variant="secondary"
              className="h-control-form px-3"
              onClick={() =>
                void navigator.clipboard.writeText(userCode).catch(() => undefined)
              }
            >
              {t("diffSelection.copy")}
            </Button>
          </div>
        </div>
      ) : null}
      <div className={userCode ? "border-t border-border pt-4" : ""}>
        <SignInBrowserRecovery authorizationUrl={session.authorizationUrl} expiresAt={session.expiresAt} onOpened={browserOpened} />
      </div>
      <p role="status" className="flex items-center gap-2 border-t border-border pt-3 text-sm text-muted-foreground">
        <LoaderCircle aria-hidden="true" className="size-4 shrink-0 animate-spin" />
        {t("accountsManager.waiting")}
      </p>
    </div>
  );
}

function AccountRow({
  account,
  removalPending,
  onRemovalStart,
  onRemovalFailed,
  disabled,
  update,
  reconnect,
  signInDisabled,
  isDefault,
  routingEnabled,
  routingBusy,
  canSetDefault,
  setDefault,
}: {
  account: AccountsManagerAccount;
  removalPending: boolean;
  onRemovalStart: () => void;
  onRemovalFailed: () => void;
  disabled: boolean;
  update: (next: AccountsManagerSnapshot) => void;
  reconnect: () => void;
  signInDisabled: boolean;
  isDefault: boolean;
  routingEnabled: boolean;
  routingBusy: boolean;
  canSetDefault: boolean;
  setDefault: (accountId?: string) => void;
}) {
  const { t, i18n } = useTranslation();
  const queryClient = useQueryClient();
  const detailsId = useId();
  const [animationsReady, setAnimationsReady] = useState(false);
  useEffect(() => setAnimationsReady(true), []);
  const [open, setOpen] = useState(false);
  const [detailsMounted, setDetailsMounted] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<MessageKey | null>(null);
  const [requestId, setRequestId] = useState("");
  const [editing, setEditing] = useState(false);
  const [draftLabel, setDraftLabel] = useState(account.label ?? "");
  useEffect(() => {
    if (!editing) setDraftLabel(account.label ?? "");
  }, [account.label, editing]);
  const label =
    account.label ||
    account.email ||
    t(
      account.kind === "api_key"
        ? "accountsManager.keyAccount"
        : account.kind === "access_token" ? "accountsManager.tokenAccount"
        : "accountsManager.oauthAccount",
      { provider: providerLabel(account.provider), id: account.id.slice(-6) },
    );
  const status = account.disabled
    ? t("accountsManager.status.disabled")
    : account.verification !== undefined && account.verification !== "verified"
      ? t(account.verification === "invalid" ? "accountsManager.verification.invalid" : "accountsManager.verification.unverified")
    : account.unavailable
      ? t("accountsManager.status.unavailable")
      : account.status === "refreshing"
        ? t("accountsManager.status.refreshing")
        : account.status === "error"
          ? t("accountsManager.status.error")
          : account.status === "active"
            ? t(
                account.kind === "api_key" || account.kind === "access_token"
                  ? account.verification === "verified" ? "accountsManager.verification.verified" : "accountsManager.status.saved"
                  : "accountsManager.status.ready",
              )
            : account.status === "pending"
              ? t("accountsManager.status.pending")
              : t("accountsManager.status.unknown");
  const action = async (task: () => Promise<AccountsManagerSnapshot>) => {
    setBusy(true);
    setError(null);
    try {
      update(await task());
      return true;
    } catch (cause) {
      setError(cause instanceof AccountControlError && cause.code === "ACCOUNTS_MANAGER_INVALID_CREDENTIAL" ? "accountsManager.verification.rejected"
        : cause instanceof AccountControlError && cause.code === "ACCOUNTS_MANAGER_VERIFICATION_UNAVAILABLE" ? "accountsManager.verification.unavailable" : "accountsManager.errors.action");
      setRequestId(cause instanceof AccountControlError ? cause.requestId : "");
      return false;
    } finally {
      setBusy(false);
    }
  };
  const warmDetails = () => {
    void prefetchAccountUsage(queryClient, account);
  };
  const expand = () => {
    const next = !open;
    setOpen(next);
    if (next) {
      setDetailsMounted(true);
      warmDetails();
    }
  };
  return (
    <div hidden={removalPending} className="border-b border-border last:border-b-0">
      <div className="flex min-h-16 flex-wrap items-center gap-x-2 gap-y-1.5 px-4 py-3 lg:flex-nowrap lg:gap-3">
        <button
          type="button"
          aria-controls={detailsId}
          aria-expanded={open}
          className="flex min-w-0 flex-[1_1_14rem] items-center gap-2 rounded-sm text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          onPointerEnter={warmDetails}
          onFocus={warmDetails}
          onClick={() => void expand()}
        >
          {open ? (
            <ChevronDown className="size-4" />
          ) : (
            <ChevronRight className="size-4" />
          )}
          <div className="min-w-0">
            <p className="truncate text-sm font-medium">{label}</p>
            <p className="text-xs text-muted-foreground">{status}</p>
            {error ? (
              <p className="text-xs text-destructive">{t(error)}</p>
            ) : null}
            {error && requestId ? <p role="alert" className="text-xs text-destructive">{t("accountsManager.controls.requestId", { id: requestId })}</p> : null}
          </div>
        </button>
        <div className="ml-6 flex w-full flex-wrap items-center justify-end gap-1 lg:ml-0 lg:w-auto lg:flex-nowrap">
        {isDefault ? (
          <span className="mr-auto rounded-full bg-muted px-2 py-1 text-micro font-medium text-foreground lg:mr-1">
            {t("accountsManager.routing.preferred")}
          </span>
        ) : (
          <Button
            size="sm"
            variant="outline"
            disabled={disabled || busy || routingBusy || !canSetDefault}
            onClick={() => setDefault(account.id)}
          >
            {t("accountsManager.routing.use")}
          </Button>
        )}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              size="icon"
              variant="ghost"
              aria-label={t("accountsManager.moreActions")}
              title={t("accountsManager.moreActions")}
              disabled={disabled || busy}
            >
              <MoreVertical aria-hidden="true" className="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="min-w-48">
            {isDefault ? (
              <DropdownMenuItem
                disabled={disabled || busy || routingBusy || routingEnabled}
                onSelect={() => setDefault(undefined)}
              >
                {t("shell.remove")}
              </DropdownMenuItem>
            ) : null}
            <DropdownMenuItem
              disabled={disabled || busy}
              onSelect={() => {
                setEditing(true);
                setDetailsMounted(true);
                setOpen(true);
                warmDetails();
              }}
            >
              <Pencil aria-hidden="true" />
              {t("accountsManager.rename")}
            </DropdownMenuItem>
            <DropdownMenuItem
              disabled={disabled || busy}
              onSelect={() =>
                void action(() => refreshAccountsManagerAccount(account.id))
              }
            >
              {busy ? <LoaderCircle aria-hidden="true" className="animate-spin" /> : <RefreshCw aria-hidden="true" />}
              {t(account.kind === "api_key" || account.kind === "access_token" || account.verification === "unverified" || account.verification === "invalid" ? "accountsManager.verification.verify" : "accountsManager.refresh")}
            </DropdownMenuItem>
            {account.kind === "oauth" ? (
              <DropdownMenuItem
                disabled={disabled || busy || signInDisabled || !account.reconnectSupported}
                title={account.reconnectSupported ? undefined : t("accountsManager.reconnect.unavailable")}
                onSelect={reconnect}
              >
                {t("accountsManager.reconnect")}
              </DropdownMenuItem>
            ) : null}
            <DropdownMenuItem
              disabled={disabled || busy}
              onSelect={() =>
                void action(() =>
                  setAccountsManagerDisabled(account.id, !account.disabled),
                )
              }
            >
              {account.disabled
                ? t("accountsManager.enable")
                : t("accountsManager.disable")}
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              disabled={disabled || busy}
              className="text-destructive focus:text-destructive data-[highlighted]:text-destructive"
              onSelect={() => setConfirm(true)}
            >
              <Trash2 aria-hidden="true" className="!text-destructive" />
              {t("accountsManager.remove")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        <AccountRemovalDialog
          accountId={account.id}
          open={confirm}
          onOpenChange={setConfirm}
          onOptimisticRemove={onRemovalStart}
          onRemovalFailed={onRemovalFailed}
          onRemovalAccepted={() => {
            onRemovalStart();
            setConfirm(false);
          }}
        />
        </div>
      </div>
      <div
        id={detailsId}
        aria-hidden={!open}
        inert={!open}
        className={`grid overflow-hidden ${animationsReady ? "transition-[grid-template-rows,opacity] duration-200 ease-out motion-reduce:transition-none" : ""} ${open ? "grid-rows-[1fr] opacity-100" : "grid-rows-[0fr] opacity-0"}`}
      >
        <div className={`min-h-0 overflow-hidden ${open ? "border-t border-border" : ""}`}>
          {detailsMounted ? (
            <div className="space-y-4 bg-muted/10 px-4 py-4 text-xs text-muted-foreground sm:px-6">
              {editing ? (
                <form
                  className="flex flex-wrap items-center gap-2"
                  onSubmit={(event) => {
                    event.preventDefault();
                    if (!busy)
                      void action(() =>
                        renameAccountsManagerAccount(
                          account.id,
                          draftLabel,
                          account.generation,
                        ),
                      ).then((saved) => {
                        if (saved) setEditing(false);
                      });
                  }}
                >
                  <Input
                    autoFocus
                    aria-label={t("accountsManager.label")}
                    maxLength={80}
                    value={draftLabel}
                    disabled={disabled || busy}
                    onChange={(event) => setDraftLabel(event.target.value)}
                    className="min-w-0 flex-1"
                  />
                  <Button type="submit" size="sm" disabled={disabled || busy}>
                    {t("accountsManager.saveLabel")}
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    disabled={busy}
                    onClick={() => setEditing(false)}
                  >
                    {t("confirm.cancel")}
                  </Button>
                </form>
              ) : null}
              {account.kind === "oauth" && !account.reconnectSupported ? (
                <p className="rounded-md border border-border bg-background/50 px-3 py-2">
                  {t("accountsManager.reconnect.unavailable")}
                </p>
              ) : null}
              {account.kind === "access_token" ? (
                <p className="rounded-md border border-border bg-background/50 px-3 py-2">
                  {t("accountsManager.credentials.legacyToken")}
                </p>
              ) : null}
              {account.cooldowns?.[0]?.retryAt ? (
                <p className="rounded-md border border-border bg-background/50 px-3 py-2">
                  {t("accountsManager.cooldown", {
                    time: new Date(account.cooldowns[0].retryAt).toLocaleString(
                      i18n.resolvedLanguage,
                    ),
                  })}
                </p>
              ) : null}
              <div>
                <AccountUsage
                  key={`${account.id}:${account.generation}:${account.updatedAt}`}
                  account={account}
                />
              </div>
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}
