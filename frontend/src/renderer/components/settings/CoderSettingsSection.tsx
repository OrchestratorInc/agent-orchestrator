import { ExternalLink, Loader2, Plus, RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../../lib/bridge";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Switch } from "../ui/switch";
import type { CloudCpOrgCoderConfig, CloudCpPutOrgCoderConfigRequest } from "../../lib/cloud-cp";
import { useCloudGate } from "../../hooks/useCloudGate";
import { useCloudCp } from "../../hooks/useCloudCp";
import { useCloudOrg } from "../../hooks/useCloudOrg";
import { useCoderTemplates } from "../../hooks/useCoderTemplates";
import { orgCoderConfigQueryKey, useOrgCoderConfig } from "../../hooks/useOrgCoderConfig";
import { canManageBilling } from "../../lib/cloud-offering";
import { useCloudSession } from "../../lib/cloud-session";
import {
	onboardingFieldErrorClass,
	onboardingFieldHintClass,
	onboardingFormLabelClass,
} from "../../lib/onboarding-ui";
import { SettingsSection } from "./SettingsSection";

/**
 * Bring-your-own-Coder configuration: the connection the control plane uses to
 * drive the org's own Coder deployment. Connecting takes only the Coder **Base
 * URL** and an **API token**: the workspace owner is resolved from the token by
 * the control plane on save, and the template is chosen per project via the
 * creation picker. Once connected, the **Workspace setup** fields say how AO
 * works inside the template's workspaces (who the agent runs as, where it
 * works, and the scripts around each session). Only shown when the org's
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
	// Without one, Save is clickable only when the workspace setup changed; the
	// stored setup is carried through every save so a token rotation never resets
	// it. Re-fetching templates is the refresh button on the Templates section.
	// A control plane that reports protectSecretFiles knows every Workspace
	// setup field; an older one knows only the ones it had before.
	const currentControlPlane = loaded?.protectSecretFiles !== undefined;
	const devEnvFieldErrors = devEnvErrors(devEnv);
	const devEnvInvalid = Object.keys(devEnvFieldErrors).length > 0;
	const devEnvDirty = loaded != null && !sameDevEnv(devEnv, devEnvDraftOf(loaded));
	// The control plane lets only org owners and admins change the connection. A
	// role the renderer does not know yet leaves the decision to the server.
	const readOnly = org?.role !== undefined && !canManageBilling(org);
	const canSave = !busy && !readOnly && orgId !== "" && baseUrl.trim() !== "" && !devEnvInvalid
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
				// A first connection has no Workspace setup yet (the form shows once
				// a connection is saved), so it sends none of those fields.
				...(loaded ? devEnvRequestFields(devEnv, currentControlPlane) : {}),
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

				{tokenStored ? (
					<WorkspaceSetupFields
						draft={devEnv}
						errors={devEnvFieldErrors}
						disabled={busy || readOnly}
						readOnly={readOnly}
						currentControlPlane={currentControlPlane}
						onChange={(patch) => setDevEnv((current) => ({ ...current, ...patch }))}
					/>
				) : null}

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

/** A path, not copy: the same in every language. */
const WORKING_DIRECTORY_EXAMPLE = "~/work/{repo}";

/**
 * How AO works inside the connected Coder's workspaces: who the worker and
 * agent run as, where the session works, and the ready check that waits for
 * the template's own setup. The values are org-wide and apply to sessions
 * started after a save. Per-session setup and cleanup commands are project
 * settings (Project settings > Scripts), as for a local project.
 */
function WorkspaceSetupFields({ draft, errors, disabled, readOnly, currentControlPlane, onChange }: {
	draft: DevEnvDraft;
	errors: Partial<Record<keyof DevEnvDraft, DevEnvErrorKey>>;
	disabled: boolean;
	readOnly: boolean;
	/** False on an older control plane, which rejects the newer fields. */
	currentControlPlane: boolean;
	onChange: (patch: Partial<DevEnvDraft>) => void;
}) {
	const { t } = useTranslation();
	const fieldError = (field: keyof DevEnvDraft) => {
		const key = errors[field];
		return key ? <p id={`coder-${field}-error`} className={onboardingFieldErrorClass} role="alert">{t(`settings.coder.${key}`)}</p> : null;
	};
	const describedBy = (field: keyof DevEnvDraft, hintId: string) => (errors[field] ? `${hintId} coder-${field}-error` : hintId);
	const secondsField = (field: "readyProbeTimeout", label: string, hint: string) => (
		<div className="flex flex-col gap-1.5">
			<Label htmlFor={`coder-${field}`} className={onboardingFormLabelClass}>{label}</Label>
			<p id={`coder-${field}-hint`} className={onboardingFieldHintClass}>{hint}</p>
			<Input
				id={`coder-${field}`}
				type="text"
				inputMode="numeric"
				autoComplete="off"
				className="w-32 font-mono text-[13px]"
				disabled={disabled}
				aria-invalid={errors[field] ? true : undefined}
				aria-describedby={describedBy(field, `coder-${field}-hint`)}
				value={draft[field]}
				onChange={(event) => onChange({ [field]: event.target.value })}
			/>
			{fieldError(field)}
		</div>
	);
	return (
		<div className="flex flex-col gap-4 border-t border-border pt-4">
			<div className="flex flex-col gap-1">
				<p className={onboardingFormLabelClass}>{t("settings.coder.workspaceSetupTitle")}</p>
				<p className={onboardingFieldHintClass}>{t("settings.coder.workspaceSetupHint")}</p>
				{readOnly ? <p className={onboardingFieldHintClass}>{t("settings.coder.workspaceSetupReadOnly")}</p> : null}
			</div>

			<div className="flex items-start justify-between gap-4">
				<div className="flex min-w-0 flex-col gap-1">
					<Label htmlFor="coder-worker-user" className={onboardingFormLabelClass}>{t("settings.coder.workerUserLabel")}</Label>
					<p id="coder-worker-user-hint" className={onboardingFieldHintClass}>{t("settings.coder.workerUserHint")}</p>
				</div>
				<Switch
					id="coder-worker-user"
					className="mt-1"
					aria-describedby="coder-worker-user-hint"
					disabled={disabled}
					checked={draft.workspaceUser}
					onCheckedChange={(checked) => onChange({ workspaceUser: checked })}
				/>
			</div>

			<div className="flex items-start justify-between gap-4">
				<div className="flex min-w-0 flex-col gap-1">
					<Label htmlFor="coder-reuse-checkout" className={onboardingFormLabelClass}>{t("settings.coder.reuseCheckoutLabel")}</Label>
					<p id="coder-reuse-checkout-hint" className={onboardingFieldHintClass}>{t("settings.coder.reuseCheckoutHint")}</p>
				</div>
				<Switch
					id="coder-reuse-checkout"
					className="mt-1"
					aria-describedby="coder-reuse-checkout-hint"
					disabled={disabled || !draft.workspaceUser}
					checked={draft.workspaceUser && draft.reuseCheckout}
					onCheckedChange={(checked) => onChange({ reuseCheckout: checked })}
				/>
			</div>

			{currentControlPlane ? <div className="flex items-start justify-between gap-4">
				<div className="flex min-w-0 flex-col gap-1">
					<Label htmlFor="coder-protect-secret-files" className={onboardingFormLabelClass}>{t("settings.coder.protectSecretFilesLabel")}</Label>
					<p id="coder-protect-secret-files-hint" className={onboardingFieldHintClass}>{t("settings.coder.protectSecretFilesHint")}</p>
				</div>
				<Switch
					id="coder-protect-secret-files"
					className="mt-1"
					aria-describedby="coder-protect-secret-files-hint"
					disabled={disabled}
					checked={draft.protectSecretFiles}
					onCheckedChange={(checked) => onChange({ protectSecretFiles: checked })}
				/>
			</div> : null}

			{currentControlPlane ? <div className="flex flex-col gap-1.5">
				<Label htmlFor="coder-workingDirectory" className={onboardingFormLabelClass}>{t("settings.coder.workingDirectoryLabel")}</Label>
				<p id="coder-workingDirectory-hint" className={onboardingFieldHintClass}>{t("settings.coder.workingDirectoryHint")}</p>
				<Input
					id="coder-workingDirectory"
					type="text"
					autoComplete="off"
					spellCheck={false}
					className="font-mono text-[13px]"
					placeholder={WORKING_DIRECTORY_EXAMPLE}
					disabled={disabled}
					aria-invalid={errors.workingDirectory ? true : undefined}
					aria-describedby={describedBy("workingDirectory", "coder-workingDirectory-hint")}
					value={draft.workingDirectory}
					onChange={(event) => onChange({ workingDirectory: event.target.value })}
				/>
				{fieldError("workingDirectory")}
			</div> : null}

			<div className="flex flex-col gap-1.5">
				<Label htmlFor="coder-readyProbe" className={onboardingFormLabelClass}>{t("settings.coder.readyProbeLabel")}</Label>
				<p id="coder-readyProbe-hint" className={onboardingFieldHintClass}>{t("settings.coder.readyProbeHint")}</p>
				<Input
					id="coder-readyProbe"
					type="text"
					autoComplete="off"
					spellCheck={false}
					className="font-mono text-[13px]"
					placeholder="grep -q '^Ready' ~/.bootstrap-status"
					disabled={disabled}
					aria-invalid={errors.readyProbe ? true : undefined}
					aria-describedby={describedBy("readyProbe", "coder-readyProbe-hint")}
					value={draft.readyProbe}
					onChange={(event) => onChange({ readyProbe: event.target.value })}
				/>
				{fieldError("readyProbe")}
			</div>
			{draft.readyProbe.trim() !== "" ? secondsField("readyProbeTimeout", t("settings.coder.readyProbeTimeoutLabel"), t("settings.coder.readyProbeTimeoutHint")) : null}

			<p className={onboardingFieldHintClass}>{t("settings.coder.projectScriptsHint")}</p>
		</div>
	);
}

const READY_PROBE_MAX_LENGTH = 1000;
/** Seconds ranges the control plane accepts; a blank field uses its default. */
const READY_PROBE_TIMEOUT_RANGE = { min: 30, max: 3600 } as const;

/** The dev-environment fields as the form edits them. */
interface DevEnvDraft {
	workspaceUser: boolean;
	reuseCheckout: boolean;
	protectSecretFiles: boolean;
	workingDirectory: string;
	readyProbe: string;
	/** Seconds as typed; "" uses the default. */
	readyProbeTimeout: string;
}

const EMPTY_DEV_ENV: DevEnvDraft = {
	workspaceUser: false,
	reuseCheckout: true,
	protectSecretFiles: true,
	workingDirectory: "",
	readyProbe: "",
	readyProbeTimeout: "",
};

function devEnvDraftOf(config: CloudCpOrgCoderConfig): DevEnvDraft {
	return {
		workspaceUser: config.workerUser === "workspace",
		// Reusing the template's checkout defaults on for the workspace user.
		reuseCheckout: config.reuseExistingCheckout ?? true,
		// Keeping the agent out of .env files is on unless turned off.
		protectSecretFiles: config.protectSecretFiles ?? true,
		workingDirectory: config.workingDirectory ?? "",
		readyProbe: config.readyProbe ?? "",
		readyProbeTimeout: secondsDraft(config.readyProbeTimeoutSeconds),
	};
}

function secondsDraft(seconds: number | undefined): string {
	return seconds ? String(seconds) : "";
}

function sameDevEnv(a: DevEnvDraft, b: DevEnvDraft): boolean {
	return a.workspaceUser === b.workspaceUser
		&& (!a.workspaceUser || a.reuseCheckout === b.reuseCheckout)
		&& a.protectSecretFiles === b.protectSecretFiles
		&& a.workingDirectory.trim() === b.workingDirectory.trim()
		&& a.readyProbe.trim() === b.readyProbe.trim()
		&& (a.readyProbe.trim() === "" || a.readyProbeTimeout.trim() === b.readyProbeTimeout.trim());
}

/** Blank uses the default; otherwise a whole number of seconds in range. */
function isValidSeconds(value: string, range: { min: number; max: number }): boolean {
	const trimmed = value.trim();
	if (trimmed === "") return true;
	if (!/^\d+$/.test(trimmed)) return false;
	const seconds = Number(trimmed);
	return seconds >= range.min && seconds <= range.max;
}

const WORKING_DIRECTORY_MAX_LENGTH = 512;
const WORKING_DIRECTORY_REPO_PLACEHOLDER = "{repo}";

/**
 * The working directory the control plane accepts: absolute or under the
 * workspace user's home (`~/` or `$HOME/`), naming a directory below that
 * root, at most 512 characters, with no control characters, no `.` or `..`
 * segment, and no placeholder other than `{repo}`.
 */
function isValidWorkingDirectory(value: string): boolean {
	const trimmed = value.trim();
	if (trimmed === "") return true;
	if ([...trimmed].length > WORKING_DIRECTORY_MAX_LENGTH) return false;
	// eslint-disable-next-line no-control-regex
	if (/[\u0000-\u001f\u007f]/.test(trimmed)) return false;
	const root = ["~/", "$HOME/", "/"].find((prefix) => trimmed.startsWith(prefix));
	if (root === undefined) return false;
	const rest = trimmed.slice(root.length);
	if (rest.replace(/^\/+|\/+$/g, "") === "") return false;
	if (rest.split("/").some((segment) => segment === "." || segment === "..")) return false;
	return !/[{}$`\\]/.test(rest.split(WORKING_DIRECTORY_REPO_PLACEHOLDER).join(""));
}

type DevEnvErrorKey =
	| "workingDirectoryNeedsWorkspaceUser"
	| "workingDirectoryInvalid"
	| "readyProbeTooLong"
	| "readyProbeTimeoutInvalid";

/** Field-level problems, mirroring the control plane's validation. */
function devEnvErrors(draft: DevEnvDraft): Partial<Record<keyof DevEnvDraft, DevEnvErrorKey>> {
	const errors: Partial<Record<keyof DevEnvDraft, DevEnvErrorKey>> = {};
	const workingDirectory = draft.workingDirectory.trim();
	if (workingDirectory !== "" && !draft.workspaceUser) errors.workingDirectory = "workingDirectoryNeedsWorkspaceUser";
	else if (!isValidWorkingDirectory(workingDirectory)) errors.workingDirectory = "workingDirectoryInvalid";
	const probe = draft.readyProbe.trim();
	if (probe.length > READY_PROBE_MAX_LENGTH) errors.readyProbe = "readyProbeTooLong";
	if (probe !== "" && !isValidSeconds(draft.readyProbeTimeout, READY_PROBE_TIMEOUT_RANGE)) errors.readyProbeTimeout = "readyProbeTimeoutInvalid";
	return errors;
}

/**
 * The dev-environment fields for the PUT body. The control plane keeps the
 * stored value of any field the request leaves out, so every field it knows is
 * sent, defaults included: that is how clearing the ready check or the
 * working directory, or turning the workspace user off, takes effect. An older
 * control plane (it does not report protectSecretFiles) rejects the newer
 * fields, so it gets only the ones it already had; the form hides the others.
 */
function devEnvRequestFields(draft: DevEnvDraft, currentControlPlane: boolean): Partial<CloudCpPutOrgCoderConfigRequest> {
	const probe = draft.readyProbe.trim();
	const timeout = draft.readyProbeTimeout.trim();
	const fields: Partial<CloudCpPutOrgCoderConfigRequest> = {
		workerUser: draft.workspaceUser ? "workspace" : "ao-worker",
		reuseExistingCheckout: draft.workspaceUser && draft.reuseCheckout,
		readyProbe: probe,
		readyProbeTimeoutSeconds: probe !== "" && timeout !== "" ? Number(timeout) : 0,
	};
	if (currentControlPlane) {
		fields.workingDirectory = draft.workspaceUser ? draft.workingDirectory.trim() : "";
		fields.protectSecretFiles = draft.protectSecretFiles;
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
