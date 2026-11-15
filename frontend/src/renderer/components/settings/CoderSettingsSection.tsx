import { ExternalLink, Loader2, Plus, RefreshCw } from "@/components/app-icons";
import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../../lib/bridge";
import { cn } from "../../lib/utils";
import { ConfirmDialog } from "../ConfirmDialog";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Switch } from "../ui/switch";
import { CloudCpError, type CloudCpOrgCoderConfig, type CloudCpPutOrgCoderConfigRequest } from "../../lib/cloud-cp";
import { useCloudGate } from "../../hooks/useCloudGate";
import { useCloudCp } from "../../hooks/useCloudCp";
import { useCloudOrg } from "../../hooks/useCloudOrg";
import { useCoderTemplates } from "../../hooks/useCoderTemplates";
import { orgCoderConfigQueryKey, useOrgCoderConfig } from "../../hooks/useOrgCoderConfig";
import { canManageBilling, cloudMeQueryKey } from "../../lib/cloud-offering";
import { useCloudSession } from "../../lib/cloud-session";
import {
	onboardingFieldErrorClass,
	onboardingFieldHintClass,
	onboardingFormLabelClass,
} from "../../lib/onboarding-ui";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

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
	const [confirmingRemove, setConfirmingRemove] = useState(false);
	const [removing, setRemoving] = useState(false);
	const [removeError, setRemoveError] = useState<string | null>(null);

	const loaded = config.data;
	useEffect(() => {
		if (!loaded) return;
		setBaseUrl(loaded.baseUrl ?? "");
		setDevEnv(devEnvDraftOf(loaded));
	}, [loaded]);

	const tokenStored = Boolean(loaded?.tokenSet);
	const { templates, isLoading: templatesLoading, isError: templatesError } = useCoderTemplates(orgId === "" ? undefined : orgId, tokenStored);
	const coderTemplatesQueryKey = ["cloud-coder-templates"] as const;

	if (status !== "authenticated") {
		return (
			<SettingsSection title={t("settings.coder.title")} sectionId="coder" titleHidden={titleHidden} grouped>
				<p className="settings-row-bar text-sm text-settings-muted">{t("settings.coder.signIn")}</p>
			</SettingsSection>
		);
	}

	const currentControlPlane = loaded?.protectSecretFiles !== undefined;
	const devEnvFieldErrors = devEnvErrors(devEnv);
	const devEnvInvalid = Object.keys(devEnvFieldErrors).length > 0;
	const devEnvDirty = loaded != null && !sameDevEnv(devEnv, devEnvDraftOf(loaded));
	const readOnly = org?.role !== undefined && !canManageBilling(org);
	const saveAllowed = loaded?.editable !== false;
	const canSave = !busy && !readOnly && saveAllowed && orgId !== "" && baseUrl.trim() !== "" && !devEnvInvalid
		&& (token.trim() !== "" || (tokenStored && devEnvDirty));
	const hasConnection = loaded != null && !readOnly && orgId !== "";
	const canRemove = hasConnection && !busy;
	const save = async () => {
		if (!canSave) return;
		setBusy(true);
		setError(null);
		try {
			await client.putOrgCoderConfig(orgId, {
				...carriedCoderConfig(loaded),
				baseUrl: baseUrl.trim(),
				token: token.trim() === "" ? undefined : token.trim(),
				...(loaded ? devEnvRequestFields(devEnv, currentControlPlane) : {}),
			});
			setToken("");
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

	const remove = async () => {
		if (!canRemove) return;
		setRemoving(true);
		setRemoveError(null);
		try {
			await client.deleteOrgCoderConfig(orgId);
			setConfirmingRemove(false);
			setToken("");
			setBaseUrl("");
			setDevEnv(EMPTY_DEV_ENV);
			await Promise.all([
				queryClient.invalidateQueries({ queryKey: orgCoderConfigQueryKey }),
				queryClient.invalidateQueries({ queryKey: coderTemplatesQueryKey }),
				queryClient.invalidateQueries({ queryKey: cloudMeQueryKey }),
			]);
		} catch (caught) {
			if (caught instanceof CloudCpError && caught.code === "connection_in_use") {
				setRemoveError(t("settings.coder.removeInUse"));
			} else {
				setRemoveError(caught instanceof Error ? caught.message : t("settings.coder.errorRemove"));
			}
		} finally {
			setRemoving(false);
		}
	};

	return (
		<>
			<SettingsSection title={t("settings.coder.title")} sectionId="coder" titleHidden={titleHidden} grouped>
				<div className="settings-content-row flex w-full flex-col gap-4">
					<p className="text-pretty text-control leading-4 text-settings-muted">{t("settings.coder.description")}</p>
					{saveAllowed ? null : <p className={onboardingFieldHintClass}>{t("settings.coder.capabilityLost")}</p>}

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
							disabled={busy || !saveAllowed}
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
							disabled={busy || !saveAllowed}
							value={token}
							onChange={(event) => setToken(event.target.value)}
						/>
					</div>
				</div>
				{saveAllowed ? (
				<SettingsRow
					label={t("settings.coder.tokenHowToTitle", { defaultValue: "Need a token?" })}
					description={
						<>
							<ol className="list-decimal space-y-1 pl-4">
								<li>{t("settings.coder.tokenStep1", { defaultValue: "Use the button to open your Coder token page. If you are not signed in to Coder, you will be asked to log in first, then land on the token page." })}</li>
								<li>{t("settings.coder.tokenStep2", { defaultValue: "Give the token a name (for example \"Orchestrator\"), set a lifetime, and create it." })}</li>
								<li>{t("settings.coder.tokenStep3", { defaultValue: "Copy the token, paste it in the field above, then Save and fetch templates." })}</li>
							</ol>
							{baseUrl.trim() === "" ? (
								<p className="mt-1">{t("settings.coder.tokenCreateNeedsUrl", { defaultValue: "Enter your Coder URL above first." })}</p>
							) : null}
						</>
					}
				>
					<Button
						type="button"
						variant="quiet"
						size="sm"
						className="shrink-0"
						disabled={baseUrl.trim() === ""}
						onClick={() => void aoBridge.app.openExternal(`${baseUrl.trim().replace(/\/+$/, "")}/settings/tokens/new`)}
					>
						<ExternalLink className="size-3.5" aria-hidden="true" />
						{t("settings.coder.tokenCreate", { defaultValue: "Create a token in Coder" })}
					</Button>
				</SettingsRow>
				) : null}
			</SettingsSection>
			{tokenStored ? (
				<SettingsSection title={t("settings.coder.workspaceSetupTitle")} grouped>
					<div className="settings-content-row">
						<WorkspaceSetupFields
							draft={devEnv}
							errors={devEnvFieldErrors}
							disabled={busy || readOnly || !saveAllowed}
							readOnly={readOnly}
							currentControlPlane={currentControlPlane}
							onChange={(patch) => setDevEnv((current) => ({ ...current, ...patch }))}
						/>
					</div>
				</SettingsSection>
			) : null}
			{tokenStored ? (
				<SettingsSection title={t("settings.coder.templatesListLabel")} grouped>
					<div className="settings-row-bar">
						<p className="min-w-0 text-pretty text-control leading-4 text-settings-muted">{t("settings.coder.templatesListHint")}</p>
						<div className="flex shrink-0 items-center gap-1">
							<Button
								type="button"
								variant="quiet"
								size="icon-sm"
								className="shrink-0"
								aria-label={t("settings.coder.templatesRefresh", { defaultValue: "Refresh templates" })}
								title={t("settings.coder.templatesRefresh", { defaultValue: "Refresh templates" })}
								disabled={templatesLoading}
								onClick={() => void queryClient.invalidateQueries({ queryKey: coderTemplatesQueryKey })}
							>
								<RefreshCw className={`size-3.5 ${templatesLoading ? "animate-spin" : ""}`} aria-hidden="true" />
							</Button>
							<Button
								type="button"
								variant="quiet"
								size="sm"
								className="shrink-0"
								disabled={baseUrl.trim() === ""}
								onClick={() => void aoBridge.app.openExternal(`${baseUrl.trim().replace(/\/+$/, "")}/templates/new`)}
							>
								<Plus className="size-3.5" aria-hidden="true" />
								{t("settings.coder.templateCreate", { defaultValue: "New template in Coder" })}
								<ExternalLink className="size-3" aria-hidden="true" />
							</Button>
						</div>
					</div>
					{templatesLoading ? (
						<div className="settings-row-bar justify-start gap-2 text-sm text-settings-muted">
							<Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
							<span>{t("settings.coder.templatesLoading")}</span>
						</div>
					) : templatesError ? (
						<p className="settings-row-bar text-xs text-error" role="alert">{t("settings.coder.templatesError")}</p>
					) : templates.length === 0 ? (
						<p className="settings-row-bar text-sm text-settings-muted">{t("settings.coder.templatesEmpty")}</p>
					) : (
						templates.map((tpl) => (
							<SettingsRow
								key={tpl.id}
								label={tpl.displayName || tpl.name}
								description={tpl.description || undefined}
							>
								<Button
									type="button"
									variant="quiet"
									size="icon-sm"
									className="shrink-0"
									aria-label={t("settings.coder.templateDocs", { defaultValue: "Open template docs in Coder" })}
									title={t("settings.coder.templateDocs", { defaultValue: "Open template docs in Coder" })}
									disabled={baseUrl.trim() === ""}
									onClick={() => void aoBridge.app.openExternal(`${baseUrl.trim().replace(/\/+$/, "")}/templates/${encodeURIComponent(tpl.name)}/docs`)}
								>
									<ExternalLink className="size-3.5" aria-hidden="true" />
								</Button>
							</SettingsRow>
						))
					)}
				</SettingsSection>
			) : null}
			<div className="flex flex-col items-end gap-2 px-(--settings-row-inset)">
				{error ? <p className={cn(onboardingFieldErrorClass, "self-stretch")} role="alert">{error}</p> : null}
				<div className="flex items-center gap-2">
					{hasConnection ? (
						<Button type="button" variant="quiet" size="sm" disabled={!canRemove} onClick={() => { setRemoveError(null); setConfirmingRemove(true); }}>
							{t("settings.coder.removeConnection")}
						</Button>
					) : null}
					{saveAllowed ? (
						<Button
							type="button"
							variant="quiet"
							size="sm"
							disabled={!canSave}
							onClick={() => void save()}
						>
							{busy ? t("settings.coder.saving") : t("settings.coder.save")}
						</Button>
					) : null}
				</div>
			</div>
			<ConfirmDialog
				open={confirmingRemove}
				title={t("settings.coder.removeTitle")}
				description={t("settings.coder.removeDescription")}
				confirmLabel={removing ? t("settings.coder.removing") : t("settings.coder.remove")}
				destructive
				busy={removing}
				error={removeError}
				onConfirm={() => void remove()}
				onOpenChange={setConfirmingRemove}
			/>
		</>
	);
}

const WORKING_DIRECTORY_EXAMPLE = "~/work/{repo}";

function WorkspaceSetupFields({ draft, errors, disabled, readOnly, currentControlPlane, onChange }: {
	draft: DevEnvDraft;
	errors: Partial<Record<keyof DevEnvDraft, DevEnvErrorKey>>;
	disabled: boolean;
	readOnly: boolean;
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
		<div className="flex flex-col gap-4">
			<div className="flex flex-col gap-1">
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
const READY_PROBE_TIMEOUT_RANGE = { min: 30, max: 3600 } as const;

interface DevEnvDraft {
	workspaceUser: boolean;
	reuseCheckout: boolean;
	protectSecretFiles: boolean;
	workingDirectory: string;
	readyProbe: string;
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
		reuseCheckout: config.reuseExistingCheckout ?? true,
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

function isValidSeconds(value: string, range: { min: number; max: number }): boolean {
	const trimmed = value.trim();
	if (trimmed === "") return true;
	if (!/^\d+$/.test(trimmed)) return false;
	const seconds = Number(trimmed);
	return seconds >= range.min && seconds <= range.max;
}

const WORKING_DIRECTORY_MAX_LENGTH = 512;
const WORKING_DIRECTORY_REPO_PLACEHOLDER = "{repo}";

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
