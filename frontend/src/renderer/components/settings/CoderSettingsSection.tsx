import { ExternalLink, Loader2, Plus, RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../../lib/bridge";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import type { CloudCpOrgCoderConfig, CloudCpPutOrgCoderConfigRequest } from "../../lib/cloud-cp";
import { useCloudGate } from "../../hooks/useCloudGate";
import { useCloudCp } from "../../hooks/useCloudCp";
import { useCloudOrg } from "../../hooks/useCloudOrg";
import { useCoderTemplates } from "../../hooks/useCoderTemplates";
import { orgCoderConfigQueryKey, useOrgCoderConfig } from "../../hooks/useOrgCoderConfig";
import { useCloudSession } from "../../lib/cloud-session";
import {
	onboardingFieldErrorClass,
	onboardingFieldHintClass,
	onboardingFormLabelClass,
} from "../../lib/onboarding-ui";
import { SettingsSection } from "./SettingsSection";

/**
 * Bring-your-own-Coder configuration: the connection the control plane uses to
 * drive the org's own Coder deployment. The form is deliberately minimal: the
 * only two things an engineer pastes are the Coder **Base URL** and an **API
 * token**. Everything else is derived or chosen elsewhere: the workspace owner
 * is resolved from the token by the control plane on save, and the template is
 * chosen per project via the creation picker. Only shown when the org's
 * offering includes the coder provider (gated by the settings catalog). Mirrors CloudCredentialsSection's shape: the
 * outer component reads only the daemon cloud gate (a query the settings page
 * already runs), so a local-only app renders nothing and never mounts the cloud
 * hooks.
 */
export function CoderSettingsSection({ titleHidden }: { titleHidden?: boolean }) {
	const { cloudEnabled } = useCloudGate();
	if (!cloudEnabled) return null;
	return <CoderSettingsSectionInner titleHidden={titleHidden} />;
}

function CoderSettingsSectionInner({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const { status } = useCloudSession();
	const { client } = useCloudCp();
	const { org } = useCloudOrg();
	const queryClient = useQueryClient();
	const config = useOrgCoderConfig();
	const orgId = org?.id ?? "";

	const [baseUrl, setBaseUrl] = useState("");
	const [token, setToken] = useState("");
	const [devEnv, setDevEnv] = useState<DevEnvDraft>(EMPTY_DEV_ENV);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);

	// Hydrate the editable non-secret fields from the loaded config. The token is
	// never returned by the control plane, so its field always starts empty.
	const loaded = config.data;
	useEffect(() => {
		if (!loaded) return;
		setBaseUrl(loaded.baseUrl ?? "");
		setDevEnv(devEnvDraftOf(loaded));
	}, [loaded]);

	// A connection must be saved before the control plane can reach the org's Coder
	// to list its templates, so only then do we enable the live template list.
	const tokenStored = Boolean(loaded?.tokenSet);
	const { templates, isLoading: templatesLoading, isError: templatesError } = useCoderTemplates(orgId === "" ? undefined : orgId, tokenStored);
	// The coder-templates query is keyed by this prefix (see useCoderTemplates); a
	// prefix match re-runs it after the connection changes.
	const coderTemplatesQueryKey = ["cloud-coder-templates"] as const;

	// Saving the connection needs the signed-in org. This page is reachable while
	// signed out, so say why it is empty instead of rendering a blank pane.
	if (status !== "authenticated") {
		return (
			<SettingsSection title={t("settings.coder.title")} sectionId="coder" titleHidden={titleHidden}>
				<p className="px-3 text-xs leading-relaxed text-muted-foreground">{t("settings.coder.signIn")}</p>
			</SettingsSection>
		);
	}

	// A save sends a new token when one is pasted: the control plane re-derives the
	// workspace owner from it and the form never round-trips the stored secret.
	// Without one there is nothing to save: the dev-environment settings are not
	// edited here (onboarding sets them) and are carried through unchanged. So
	// once a connection is saved and its templates are loaded, Save is not
	// clickable until a token is pasted; re-fetching templates is the refresh
	// button on the Templates section, not Save.
	const timeoutInvalid = devEnv.readyProbe.trim() !== "" && !isValidReadyProbeTimeout(devEnv.readyProbeTimeout);
	const devEnvDirty = loaded != null && !sameDevEnv(devEnv, devEnvDraftOf(loaded));
	const canSave = !busy && orgId !== "" && baseUrl.trim() !== "" && !timeoutInvalid
		&& (token.trim() !== "" || (tokenStored && devEnvDirty));
	const save = async () => {
		if (!canSave) return;
		setBusy(true);
		setError(null);
		try {
			await client.putOrgCoderConfig(orgId, {
				...carriedCoderConfig(loaded),
				baseUrl: baseUrl.trim(),
				token: token.trim() === "" ? undefined : token.trim(),
				...devEnvRequestFields(devEnv),
			});
			setToken("");
			// The connection now points at a (possibly new) Coder, so its template list
			// may have changed — refresh both the config and the templates.
			await Promise.all([
				queryClient.invalidateQueries({ queryKey: orgCoderConfigQueryKey }),
				queryClient.invalidateQueries({ queryKey: coderTemplatesQueryKey }),
			]);
		} catch (caught) {
			setError(caught instanceof Error ? caught.message : t("settings.coder.errorSave"));
		} finally {
			setBusy(false);
		}
	};

	return (
		<SettingsSection title={t("settings.coder.title")} sectionId="coder" titleHidden={titleHidden}>
			<div className="flex w-full flex-col gap-4 px-3 py-1">
				<p className="text-xs leading-relaxed text-muted-foreground">{t("settings.coder.description")}</p>

				<div className="flex flex-col gap-1.5">
					<Label htmlFor="coder-base-url" className={onboardingFormLabelClass}>{t("settings.coder.urlLabel")}</Label>
					<p className={onboardingFieldHintClass}>{t("settings.coder.urlHint")}</p>
					<Input
						id="coder-base-url"
						type="text"
						autoComplete="off"
						spellCheck={false}
						className="font-mono text-[13px]"
						placeholder={t("settings.coder.urlPlaceholder")}
						disabled={busy}
						value={baseUrl}
						onChange={(event) => setBaseUrl(event.target.value)}
					/>
				</div>

				<div className="flex flex-col gap-1.5">
					<Label htmlFor="coder-token" className={onboardingFormLabelClass}>{t("settings.coder.tokenLabel")}</Label>
					<p className={onboardingFieldHintClass}>{t("settings.coder.tokenHint")}</p>
					<Input
						id="coder-token"
						type="password"
						autoComplete="off"
						spellCheck={false}
						className="font-mono text-[13px]"
						placeholder={tokenStored ? t("settings.coder.tokenStored") : t("settings.coder.tokenPlaceholder")}
						disabled={busy}
						value={token}
						onChange={(event) => setToken(event.target.value)}
					/>
					<div className="mt-1 flex flex-col gap-2 rounded-md border border-border/60 bg-muted/20 px-3 py-2.5">
						<p className="text-xs font-medium text-foreground">{t("settings.coder.tokenHowToTitle", { defaultValue: "Need a token?" })}</p>
						<ol className="list-decimal space-y-1 pl-4 text-xs leading-relaxed text-muted-foreground">
							<li>{t("settings.coder.tokenStep1", { defaultValue: "Open your Coder token page below. If you are not signed in to Coder, you will be asked to log in first, then land on the token page." })}</li>
							<li>{t("settings.coder.tokenStep2", { defaultValue: "Give the token a name (for example \"AO\"), set a lifetime, and create it." })}</li>
							<li>{t("settings.coder.tokenStep3", { defaultValue: "Copy the token, paste it in the field above, then Save and fetch templates." })}</li>
						</ol>
						<Button
							type="button"
							variant="outline"
							size="sm"
							className="w-fit gap-1.5"
							disabled={baseUrl.trim() === ""}
							onClick={() => void aoBridge.app.openExternal(`${baseUrl.trim().replace(/\/+$/, "")}/settings/tokens/new`)}
						>
							<ExternalLink className="size-3.5" aria-hidden="true" />
							{t("settings.coder.tokenCreate", { defaultValue: "Create a token in Coder" })}
						</Button>
						{baseUrl.trim() === "" ? (
							<p className={onboardingFieldHintClass}>{t("settings.coder.tokenCreateNeedsUrl", { defaultValue: "Enter your Coder URL above first." })}</p>
						) : null}
					</div>
				</div>

				{/* The dev-environment settings (workerUser, reuseExistingCheckout,
				    readyProbe) are set per customer during onboarding, not here; the
				    form still carries the stored values through every save so a
				    token change never resets them. */}
				{tokenStored ? (
					// Read-only catalog of every template on the connected Coder — so the
					// whole Coder story lives inside AO. Same tidy name + one-line-spec
					// style as the session template picker; the template for a session is
					// chosen per project at creation time, not here.
					<div className="flex flex-col gap-2 border-t border-border pt-4">
						<div className="flex items-center justify-between gap-2">
							<Label className={onboardingFormLabelClass}>{t("settings.coder.templatesListLabel")}</Label>
							<div className="flex items-center gap-1.5">
								<Button
									type="button"
									variant="ghost"
									size="icon-sm"
									className="shrink-0 text-muted-foreground hover:text-foreground"
									aria-label={t("settings.coder.templatesRefresh", { defaultValue: "Refresh templates" })}
									title={t("settings.coder.templatesRefresh", { defaultValue: "Refresh templates" })}
									disabled={templatesLoading}
									onClick={() => void queryClient.invalidateQueries({ queryKey: coderTemplatesQueryKey })}
								>
									<RefreshCw className={`size-3.5 ${templatesLoading ? "animate-spin" : ""}`} aria-hidden="true" />
								</Button>
								<Button
									type="button"
									variant="outline"
									size="sm"
									className="shrink-0 gap-1.5"
									disabled={baseUrl.trim() === ""}
									onClick={() => void aoBridge.app.openExternal(`${baseUrl.trim().replace(/\/+$/, "")}/templates/new`)}
								>
									<Plus className="size-3.5" aria-hidden="true" />
									{t("settings.coder.templateCreate", { defaultValue: "New template in Coder" })}
									<ExternalLink className="size-3" aria-hidden="true" />
								</Button>
							</div>
						</div>
						<p className={onboardingFieldHintClass}>{t("settings.coder.templatesListHint")}</p>
						{templatesLoading ? (
							<div className="flex items-center gap-2 px-1 py-3 text-xs text-muted-foreground">
								<Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
								<span>{t("settings.coder.templatesLoading")}</span>
							</div>
						) : templatesError ? (
							<p className={onboardingFieldErrorClass} role="alert">{t("settings.coder.templatesError")}</p>
						) : templates.length === 0 ? (
							<p className="px-1 py-3 text-xs text-muted-foreground">{t("settings.coder.templatesEmpty")}</p>
						) : (
							<ul aria-label={t("settings.coder.templatesListLabel")} className="flex flex-col overflow-hidden rounded-md border border-border">
								{templates.map((tpl) => (
									<li key={tpl.id} className="flex items-center justify-between gap-2 border-b border-border px-3 py-2 last:border-b-0">
										<div className="flex min-w-0 flex-col gap-0.5">
											<span className="truncate text-[13px] text-foreground">{tpl.displayName || tpl.name}</span>
											{tpl.description ? <span className="truncate text-[11px] text-muted-foreground">{tpl.description}</span> : null}
										</div>
										<Button
											type="button"
											variant="ghost"
											size="icon-sm"
											className="shrink-0 text-muted-foreground hover:text-foreground"
											aria-label={t("settings.coder.templateDocs", { defaultValue: "Open template docs in Coder" })}
											title={t("settings.coder.templateDocs", { defaultValue: "Open template docs in Coder" })}
											disabled={baseUrl.trim() === ""}
											onClick={() => void aoBridge.app.openExternal(`${baseUrl.trim().replace(/\/+$/, "")}/templates/${encodeURIComponent(tpl.name)}/docs`)}
										>
											<ExternalLink className="size-3.5" aria-hidden="true" />
										</Button>
									</li>
								))}
							</ul>
						)}
					</div>
				) : null}

				{error ? <p className={onboardingFieldErrorClass} role="alert">{error}</p> : null}

				<div className="flex items-center justify-end gap-3">
					<Button type="button" variant="outline" disabled={!canSave} onClick={() => void save()}>
						{busy ? t("settings.coder.saving") : t("settings.coder.save")}
					</Button>
				</div>
			</div>
		</SettingsSection>
	);
}

const READY_PROBE_TIMEOUT_MIN = 30;
const READY_PROBE_TIMEOUT_MAX = 3600;

/** The dev-environment fields as the form edits them. */
interface DevEnvDraft {
	workspaceUser: boolean;
	reuseCheckout: boolean;
	readyProbe: string;
	/** Seconds as typed; "" uses the default. */
	readyProbeTimeout: string;
}

const EMPTY_DEV_ENV: DevEnvDraft = { workspaceUser: false, reuseCheckout: true, readyProbe: "", readyProbeTimeout: "" };

function devEnvDraftOf(config: CloudCpOrgCoderConfig): DevEnvDraft {
	return {
		workspaceUser: config.workerUser === "workspace",
		// Reusing the template's checkout defaults on for the workspace user.
		reuseCheckout: config.reuseExistingCheckout ?? true,
		readyProbe: config.readyProbe ?? "",
		readyProbeTimeout: config.readyProbeTimeoutSeconds ? String(config.readyProbeTimeoutSeconds) : "",
	};
}

function sameDevEnv(a: DevEnvDraft, b: DevEnvDraft): boolean {
	return a.workspaceUser === b.workspaceUser
		&& (!a.workspaceUser || a.reuseCheckout === b.reuseCheckout)
		&& a.readyProbe.trim() === b.readyProbe.trim()
		&& (a.readyProbe.trim() === "" || a.readyProbeTimeout.trim() === b.readyProbeTimeout.trim());
}

/** Blank uses the default; otherwise a whole number of seconds in range. */
function isValidReadyProbeTimeout(value: string): boolean {
	const trimmed = value.trim();
	if (trimmed === "") return true;
	if (!/^\d+$/.test(trimmed)) return false;
	const seconds = Number(trimmed);
	return seconds >= READY_PROBE_TIMEOUT_MIN && seconds <= READY_PROBE_TIMEOUT_MAX;
}

/**
 * The dev-environment fields for the PUT body, each only when it differs from
 * the default: the PUT replaces the stored config (so leaving one out resets
 * it), and an older control plane rejects fields it does not know.
 */
function devEnvRequestFields(draft: DevEnvDraft): Partial<CloudCpPutOrgCoderConfigRequest> {
	const fields: Partial<CloudCpPutOrgCoderConfigRequest> = {};
	if (draft.workspaceUser) {
		fields.workerUser = "workspace";
		fields.reuseExistingCheckout = draft.reuseCheckout;
	}
	const probe = draft.readyProbe.trim();
	if (probe !== "") {
		fields.readyProbe = probe;
		const timeout = draft.readyProbeTimeout.trim();
		if (timeout !== "") fields.readyProbeTimeoutSeconds = Number(timeout);
	}
	return fields;
}

/**
 * Stored settings this form does not edit, carried through a save so it does
 * not drop them. The owner is left out on purpose: the control plane derives it
 * from the token, which may have changed.
 */
function carriedCoderConfig(config: CloudCpOrgCoderConfig | null | undefined): Partial<CloudCpPutOrgCoderConfigRequest> {
	if (!config) return {};
	const fields: Partial<CloudCpPutOrgCoderConfigRequest> = {};
	const templateId = config.templateId || config.defaultTemplateId;
	if (templateId) fields.templateId = templateId;
	if (config.agentName) fields.agentName = config.agentName;
	if (config.endpointServiceName) fields.endpointServiceName = config.endpointServiceName;
	if (config.region) fields.region = config.region;
	if (config.parameters && Object.keys(config.parameters).length > 0) fields.parameters = config.parameters;
	if (config.durableRoot) fields.durableRoot = config.durableRoot;
	if (config.requireMountedDurableRoot) fields.requireMountedDurableRoot = true;
	if (config.startupTimeoutSeconds) fields.startupTimeoutSeconds = config.startupTimeoutSeconds;
	return fields;
}
