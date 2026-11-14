import { ChevronDown, ChevronRight, TriangleAlert } from "@/components/app-icons";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { useCoderTemplateParameters } from "../hooks/useCoderTemplateParameters";
import { useOrgCoderConfig } from "../hooks/useOrgCoderConfig";
import type { CloudCpCoderTemplateParameter } from "../lib/cloud-cp/types";
import {
	coderReusesTemplateCheckout,
	decodeListValue,
	duplicateCloneOptions,
	editableTemplateParameters,
	effectiveParameterValue,
	encodeListValue,
	withParameterValue,
} from "../lib/coder-template-parameters";
import { onboardingFieldErrorClass, onboardingFieldHintClass } from "../lib/onboarding-ui";
import { cn } from "../lib/utils";
import { LiveStatus } from "./LiveStatus";
import { Checkbox } from "./ui/checkbox";
import { Input } from "./ui/input";
import { Label } from "./ui/label";
import { Pressable } from "./ui/pressable";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { Switch } from "./ui/switch";

const COLLAPSE_AFTER = 4;
const EMPTY_OPTION_VALUE = "\u0000empty";

const TEMPLATE_PARAMETER_MAX_LENGTH = 4096;

export function CoderTemplateOptions({
	orgId,
	templateId,
	overrides,
	onChange,
	disabled,
	footer,
	repositoryUrls = [],
	projectWorkingDirectory,
}: {
	orgId: string | undefined;
	templateId: string | undefined;
	overrides: Readonly<Record<string, string>>;
	onChange: (next: Record<string, string>) => void;
	disabled?: boolean;
	footer?: ReactNode;
	repositoryUrls?: readonly string[];
	projectWorkingDirectory?: string;
}) {
	const { t, i18n } = useTranslation();
	const { parameters: all, isError, settled } = useCoderTemplateParameters(orgId, templateId, true, overrides);
	const parameters = editableTemplateParameters(all);
	const orgCoder = useOrgCoderConfig(true, orgId);
	const { coderWorkerWorkingFolder } = useCloudOffering();
	const modeKnown = orgCoder.data !== undefined || (!orgCoder.isLoading && !orgCoder.isError);
	const duplicates = modeKnown && !coderReusesTemplateCheckout(orgCoder.data ?? null, projectWorkingDirectory)
		? duplicateCloneOptions(parameters, overrides, repositoryUrls)
		: undefined;
	const edited = useRef(false);
	useEffect(() => {
		if (!edited.current || !settled || all.length === 0) return;
		const declared = new Set(all.map((parameter) => parameter.name));
		const stale = Object.keys(overrides).filter((name) => !declared.has(name));
		if (stale.length === 0) return;
		const next = { ...overrides };
		for (const name of stale) delete next[name];
		onChange(next);
	}, [all, onChange, overrides, settled]);
	const collapsible = parameters.length > COLLAPSE_AFTER;
	const [open, setOpen] = useState(false);
	const collapsedWarningId = `${useId()}-duplicate-clone`;
	if (isError && parameters.length === 0) {
		return <p className={onboardingFieldErrorClass} role="alert">{t("coder.templateOptions.loadFailed")}</p>;
	}
	if (parameters.length === 0) return null;
	const expanded = !collapsible || open;
	const blocksWorkingFolder = coderWorkerWorkingFolder === true && (projectWorkingDirectory ?? "").trim() !== "";
	const duplicateMessage = (labels: readonly string[]) => t(
		blocksWorkingFolder ? "coder.templateOptions.duplicateCloneWorkingFolder" : "coder.templateOptions.duplicateClone",
		{ count: labels.length, repos: new Intl.ListFormat(i18n.language, { type: "conjunction" }).format(labels) },
	);
	const duplicateWarning = (labels: readonly string[], id: string) => (
		<TemplateOptionWarning id={id} startFails={blocksWorkingFolder}>{duplicateMessage(labels)}</TemplateOptionWarning>
	);
	const duplicateLabels = duplicates ? [...new Set([...duplicates.values()].flat())] : [];
	const collapsedDuplicates = expanded ? [] : duplicateLabels;
	return (
		<section className="flex flex-col gap-3 text-sm" aria-label={t("coder.templateOptions.title")} data-testid="coder-template-options">
			<LiveStatus text={duplicateLabels.length > 0 ? duplicateMessage(duplicateLabels) : ""} />
			{isError ? <p className={onboardingFieldErrorClass} role="alert">{t("coder.templateOptions.checkFailed")}</p> : null}
			<div className="flex flex-col gap-1">
				{collapsible ? (
					<Pressable
						className="flex w-fit items-center gap-1 font-medium text-foreground"
						aria-expanded={expanded}
						aria-describedby={collapsedDuplicates.length > 0 ? collapsedWarningId : undefined}
						onClick={() => setOpen((value) => !value)}
					>
						{expanded
							? <ChevronDown aria-hidden="true" className="size-icon-sm text-muted-foreground" />
							: <ChevronRight aria-hidden="true" className="size-icon-sm text-muted-foreground" />}
						{t("coder.templateOptions.titleCount", { total: parameters.length })}
					</Pressable>
				) : (
					<span className="font-medium text-foreground">{t("coder.templateOptions.title")}</span>
				)}
				{collapsedDuplicates.length > 0 ? (
					duplicateWarning(collapsedDuplicates, collapsedWarningId)
				) : null}
				<p className={onboardingFieldHintClass}>{t("coder.templateOptions.subtitle")}</p>
			</div>
			{expanded ? (
				<div className="flex flex-col gap-4">
					{parameters.map((parameter) => (
						<TemplateParameterField
							key={parameter.name}
							parameter={parameter}
							value={effectiveParameterValue(parameter, overrides)}
							disabled={disabled}
							problem={parameter.name in overrides ? valueProblem(parameter) : undefined}
							warning={duplicates?.has(parameter.name)
								? (warningId) => duplicateWarning(duplicates.get(parameter.name) ?? [], warningId)
								: undefined}
							onChange={(value) => {
								edited.current = true;
								onChange(withParameterValue(overrides, parameter, value));
							}}
						/>
					))}
				</div>
			) : null}
			{footer}
		</section>
	);
}

function valueProblem(parameter: CloudCpCoderTemplateParameter): string | undefined {
	return parameter.diagnostics?.find((diagnostic) => diagnostic.severity === "error")?.summary?.trim() || undefined;
}

function TemplateOptionWarning({ id, startFails, children }: { id: string; startFails: boolean; children: ReactNode }) {
	return (
		<p id={id} className={cn(onboardingFieldHintClass, "flex items-start gap-1.5", startFails ? "text-warning" : "text-muted-foreground")}>
			<TriangleAlert aria-hidden="true" className="mt-1 size-3 shrink-0 text-warning" />
			<span className="min-w-0">{children}</span>
		</p>
	);
}

function TemplateParameterField({
	parameter,
	value,
	disabled,
	problem,
	warning,
	onChange,
}: {
	parameter: CloudCpCoderTemplateParameter;
	value: string;
	disabled?: boolean;
	problem?: string;
	warning?: (id: string) => ReactNode;
	onChange: (value: string) => void;
}) {
	const { t } = useTranslation();
	const id = useId();
	const hintId = `${id}-hint`;
	const label = parameter.displayName?.trim() || parameter.name;
	const description = parameter.description?.trim() ?? "";
	const options = parameter.options ?? [];
	const mandatory = parameter.required || (options.length > 0 && parameter.type !== "list(string)" && parameter.type !== "bool");
	const heading = (
		<Label htmlFor={id} className="text-foreground">
			<span>{label}{mandatory ? <span aria-hidden="true"> *</span> : null}</span>
			{mandatory ? <span className="sr-only">{t("coder.templateOptions.required")}</span> : null}
		</Label>
	);
	const problemId = `${id}-problem`;
	const warningId = `${id}-warning`;
	const hint = description ? <p id={hintId} className={onboardingFieldHintClass}>{description}</p> : null;
	const problemText = problem ? <p id={problemId} className={onboardingFieldErrorClass} role="alert">{problem}</p> : null;
	const describedBy = [description ? hintId : "", warning ? warningId : "", problem ? problemId : ""].filter(Boolean).join(" ") || undefined;

	if (parameter.type === "bool") {
		return (
			<div className="flex flex-col gap-1">
				<div className="flex items-center justify-between gap-3">
					{heading}
					<Switch
						id={id}
						checked={value.trim().toLowerCase() === "true"}
						disabled={disabled}
						aria-describedby={describedBy}
						onCheckedChange={(checked) => onChange(checked ? "true" : "false")}
					/>
				</div>
				{hint}
				{problemText}
			</div>
		);
	}

	let control: ReactNode;
	if (parameter.type === "list(string)" && options.length > 0) {
		const selected = decodeListValue(value);
		control = (
			<div
				id={id}
				role="group"
				aria-label={label}
				aria-describedby={describedBy}
				className="flex flex-col gap-2 rounded-md border border-border px-3 py-2"
			>
				{options.map((option) => {
					const checked = selected.includes(option.value);
					return (
						<label key={option.value} className="flex items-center gap-2 text-sm text-foreground">
							<Checkbox
								checked={checked}
								disabled={disabled}
								onCheckedChange={(next) => {
									const set = new Set(selected);
									if (next === true) set.add(option.value);
									else set.delete(option.value);
									onChange(encodeListValue(options.map((item) => item.value).filter((item) => set.has(item))));
								}}
							/>
							<span>{option.name || option.value}</span>
						</label>
					);
				})}
			</div>
		);
	} else if (parameter.type === "list(string)") {
		control = <ListTextInput id={id} value={value} disabled={disabled} describedBy={describedBy} onChange={onChange} />;
	} else if (options.length > 0) {
		const toSelect = (raw: string) => (raw === "" ? EMPTY_OPTION_VALUE : raw);
		control = (
			<Select
				value={options.some((option) => option.value === value) ? toSelect(value) : undefined}
				disabled={disabled}
				onValueChange={(next) => onChange(next === EMPTY_OPTION_VALUE ? "" : next)}
			>
				<SelectTrigger id={id} className="w-full min-w-0" aria-describedby={describedBy} aria-required={mandatory || undefined}>
					<SelectValue placeholder={t("coder.templateOptions.selectPlaceholder")} />
				</SelectTrigger>
				<SelectContent>
					{options.map((option) => (
						<SelectItem key={option.value} value={toSelect(option.value)}>
							{option.name || option.value}
						</SelectItem>
					))}
				</SelectContent>
			</Select>
		);
	} else {
		control = (
			<Input
				id={id}
				type={parameter.type === "number" ? "number" : "text"}
				inputMode={parameter.type === "number" ? "decimal" : undefined}
				maxLength={TEMPLATE_PARAMETER_MAX_LENGTH}
				value={value}
				disabled={disabled}
				spellCheck={false}
				autoComplete="off"
				aria-describedby={describedBy}
				aria-required={mandatory || undefined}
				onChange={(event) => onChange(event.target.value)}
			/>
		);
	}

	return (
		<div className="flex flex-col gap-1.5">
			{heading}
			{hint}
			{control}
			{warning ? warning(warningId) : null}
			{problemText}
		</div>
	);
}

function ListTextInput({
	id,
	value,
	disabled,
	describedBy,
	onChange,
}: {
	id: string;
	value: string;
	disabled?: boolean;
	describedBy?: string;
	onChange: (value: string) => void;
}) {
	const { t } = useTranslation();
	const [text, setText] = useState(() => decodeListValue(value).join(", "));
	return (
		<Input
			id={id}
			value={text}
			maxLength={TEMPLATE_PARAMETER_MAX_LENGTH}
			disabled={disabled}
			spellCheck={false}
			autoComplete="off"
			aria-describedby={describedBy}
			placeholder={t("coder.templateOptions.listPlaceholder")}
			onChange={(event) => {
				setText(event.target.value);
				onChange(encodeListValue(event.target.value.split(",").map((item) => item.trim()).filter((item) => item !== "")));
			}}
		/>
	);
}
