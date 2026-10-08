import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
	MAX_PROJECT_DISPLAY_NAME_LEN,
	ProjectSettingsFormView,
	ProjectSettingsInputRow,
	ProjectSettingsRow,
	ProjectSettingsSection,
	ProjectSettingsValueRow,
} from "@aoagents/product-ui";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Pencil, TriangleAlert } from "lucide-react";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudOrg } from "../hooks/useCloudOrg";
import { useCoderTemplates } from "../hooks/useCoderTemplates";
import { useOrgCoderConfig } from "../hooks/useOrgCoderConfig";
import { cloudProjectsQueryKey } from "../hooks/useWorkspaceQuery";
import type { CloudCpProject, CloudCpProjectCoderConfig } from "../lib/cloud-cp";
import { ProductExternalLink } from "./ProductExternalLink";
import type { ProjectSettingsSaveState } from "./ProjectSettingsForm";

type CloudProjectForm = { displayName: string; defaultBranch: string };

/** The coder config the control plane stores on a project (config.coder). */
function coderConfigOf(project: CloudCpProject): CloudCpProjectCoderConfig | undefined {
	const coder = project.config?.coder;
	return coder && typeof coder === "object" ? (coder as CloudCpProjectCoderConfig) : undefined;
}

function repositoryHref(repository: string): string | undefined {
	return /^https?:\/\//i.test(repository) ? repository : undefined;
}

/**
 * Settings for a cloud project. A cloud project lives only in the control
 * plane, so it is read from (and saved to) the control plane rather than the
 * local daemon, which has no record of it and would answer "Unknown project".
 * The control plane accepts updates to the name and default branch only; the
 * Coder template is fixed at creation and shown read-only.
 */
export function CloudProjectSettingsForm({
	project,
	onSaveState,
}: {
	project: CloudCpProject;
	onSaveState?: (state: ProjectSettingsSaveState) => void;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const { client } = useCloudCp();
	const { org } = useCloudOrg();
	const coder = coderConfigOf(project);
	const templateId = coder?.templateId?.trim() ?? "";
	const { templates, isLoading: templatesLoading } = useCoderTemplates(project.orgId, templateId !== "");
	const template = templates.find((candidate) => candidate.id === templateId);
	const templateLabel = template ? template.displayName || template.name : templatesLoading ? "…" : templateId;
	// No project template only blocks sessions for a bring-your-own-Coder org
	// without an org-default template (the control plane's
	// coder_template_required). Otherwise the deployment or org default applies,
	// and non-Coder providers need no template at all.
	const orgCoder = useOrgCoderConfig();
	const orgCoderConfig = orgCoder.data as { templateId?: string; defaultTemplateId?: string } | null | undefined;
	const orgDefaultTemplateId = (orgCoderConfig?.templateId ?? orgCoderConfig?.defaultTemplateId ?? "").trim();
	const templateRequired = templateId === "" && orgCoderConfig != null && orgDefaultTemplateId === "";

	const [form, setForm] = useState<CloudProjectForm>({
		displayName: project.displayName,
		defaultBranch: project.defaultBranch,
	});
	const lastSavedRef = useRef(JSON.stringify(form));
	const failedKeyRef = useRef<string | null>(null);
	const [validationError, setValidationError] = useState<string | null>(null);
	const [savedAt, setSavedAt] = useState<number | null>(null);

	const mutation = useMutation({
		mutationFn: async (values: CloudProjectForm) => {
			const orgId = org?.id ?? project.orgId;
			await client.updateProject(orgId, project.id, {
				displayName: values.displayName.trim(),
				defaultBranch: values.defaultBranch.trim(),
			});
			return JSON.stringify(values);
		},
		onSuccess: (savedKey) => {
			lastSavedRef.current = savedKey;
			failedKeyRef.current = null;
			setSavedAt(Date.now());
			void queryClient.invalidateQueries({ queryKey: cloudProjectsQueryKey });
		},
		onError: (_error, values) => {
			failedKeyRef.current = JSON.stringify(values);
		},
	});

	const validate = (values: CloudProjectForm): string | null => {
		const name = values.displayName.trim();
		if (name === "") return t("settings.project.nameRequired");
		if (name.length > MAX_PROJECT_DISPLAY_NAME_LEN) return t("settings.project.nameTooLong", { max: MAX_PROJECT_DISPLAY_NAME_LEN });
		if (values.defaultBranch.trim() === "") return t("settings.project.cloudBranchRequired");
		return null;
	};

	const save = (values: CloudProjectForm) => {
		const error = validate(values);
		setValidationError(error);
		if (error) return;
		setSavedAt(null);
		mutation.mutate(values);
	};

	useEffect(() => {
		const key = JSON.stringify(form);
		if (key === lastSavedRef.current || key === failedKeyRef.current || mutation.isPending) return;
		const timeout = window.setTimeout(() => save(form), 650);
		return () => window.clearTimeout(timeout);
	}, [form, mutation.isPending]);

	useEffect(() => {
		if (savedAt === null) return;
		const timeout = window.setTimeout(() => setSavedAt(null), 1800);
		return () => window.clearTimeout(timeout);
	}, [savedAt]);

	useEffect(() => {
		const mutationError = mutation.isError ? (mutation.error instanceof Error ? mutation.error.message : t("settings.project.saveFailed")) : undefined;
		const dirty = JSON.stringify(form) !== lastSavedRef.current;
		onSaveState?.({
			dirty,
			requestPending: mutation.isPending,
			phase: validationError || mutationError ? "failed" : mutation.isPending ? "saving" : dirty ? "pending" : savedAt !== null ? "saved" : "idle",
			error: validationError ?? mutationError,
		});
	}, [form, mutation.error, mutation.isError, mutation.isPending, onSaveState, savedAt, t, validationError]);

	const editIcon = <Pencil className="settings-inline-edit-icon" aria-hidden="true" />;

	return (
		<ProjectSettingsFormView id="project-settings-form" className="project-settings-form gap-5" onSubmit={() => save(form)}>
			<ProjectSettingsSection title={t("settings.project.details")} grouped>
				<ProjectSettingsInputRow
					editIcon={editIcon}
					editLabel={t("settings.field.edit", { label: t("settings.project.name") })}
					label={t("settings.project.name")}
					id="projectName"
					value={form.displayName}
					onChange={(displayName) => setForm((current) => ({ ...current, displayName }))}
				/>
				<ProjectSettingsValueRow label={t("settings.project.kind")} value={t("settings.project.kind.cloud")} />
				<ProjectSettingsValueRow
					externalLink={ProductExternalLink}
					href={repositoryHref(project.repositoryUrl)}
					label={t("settings.project.repo")}
					value={project.repositoryUrl || "—"}
				/>
				<ProjectSettingsInputRow
					editIcon={editIcon}
					editLabel={t("settings.field.edit", { label: t("settings.project.defaultBranch") })}
					label={t("settings.project.defaultBranch")}
					id="projectDefaultBranch"
					value={form.defaultBranch}
					onChange={(defaultBranch) => setForm((current) => ({ ...current, defaultBranch }))}
				/>
			</ProjectSettingsSection>
			<ProjectSettingsSection title={t("settings.project.coderTitle")} grouped>
				<ProjectSettingsRow label={t("settings.project.coderTemplate")}>
					{templateId !== "" ? (
						<span className="settings-row-value" title={templateId}>{templateLabel}</span>
					) : templateRequired ? (
						<span className="settings-row-value flex items-center gap-1.5 text-error" role="alert">
							<TriangleAlert className="size-4 shrink-0" aria-hidden="true" />
							{t("settings.project.coderTemplateMissing")}
						</span>
					) : (
						<span className="settings-row-value">{orgCoder.isLoading ? "…" : t("settings.project.coderTemplateDefault")}</span>
					)}
				</ProjectSettingsRow>
				{coder?.size ? <ProjectSettingsValueRow label={t("settings.project.coderSize")} value={coder.size} /> : null}
			</ProjectSettingsSection>
		</ProjectSettingsFormView>
	);
}
