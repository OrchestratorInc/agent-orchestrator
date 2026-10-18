import { ChevronDown, ChevronRight } from "lucide-react";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useCoderTemplateParameters } from "../hooks/useCoderTemplateParameters";
import type { CloudCpCoderTemplateParameter } from "../lib/cloud-cp/types";
import {
	decodeListValue,
	editableTemplateParameters,
	effectiveParameterValue,
	encodeListValue,
	withParameterValue,
} from "../lib/coder-template-parameters";
import { onboardingFieldErrorClass, onboardingFieldHintClass } from "../lib/onboarding-ui";
import { Checkbox } from "./ui/checkbox";
import { Input } from "./ui/input";
import { Label } from "./ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { Switch } from "./ui/switch";

// Past this many options the section starts collapsed behind its header.
const COLLAPSE_AFTER = 4;
// Radix Select reserves "" for "no value", so an option whose value is empty
// rides on this stand-in.
const EMPTY_OPTION_VALUE = "\u0000empty";

/**
 * "Template options": the chosen Coder template's own parameters, prefilled
 * with their defaults. `overrides` holds only the values the user changed;
 * an edit back to the default drops its key. The parameters are evaluated with
 * the current overrides, so one the template declares only for some value of
 * another appears once that value is picked, and after an edit a value whose
 * parameter no longer applies is dropped. Renders nothing while loading,
 * for a template without parameters, and for a control plane without the
 * parameters route (404). The caller decides it is a Coder project with a
 * template; this component only fetches once both ids are present.
 */
/** The control plane refuses a template option value over 4 KB. */
const TEMPLATE_PARAMETER_MAX_LENGTH = 4096;

export function CoderTemplateOptions({
	orgId,
	templateId,
	overrides,
	onChange,
	disabled,
	footer,
}: {
	orgId: string | undefined;
	templateId: string | undefined;
	overrides: Readonly<Record<string, string>>;
	onChange: (next: Record<string, string>) => void;
	disabled?: boolean;
	/** Shown under the fields, e.g. the settings form's save state. */
	footer?: ReactNode;
}) {
	const { t } = useTranslation();
	const { parameters: all, isError, settled } = useCoderTemplateParameters(orgId, templateId, true, overrides);
	const parameters = editableTemplateParameters(all);
	// Only an edit made here prunes values: a saved value is never dropped just
	// because the form opened.
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
	if (isError && parameters.length === 0) {
		return <p className={onboardingFieldErrorClass} role="alert">{t("coder.templateOptions.loadFailed")}</p>;
	}
	if (parameters.length === 0) return null;
	const expanded = !collapsible || open;
	return (
		<section className="flex flex-col gap-3 text-sm" aria-label={t("coder.templateOptions.title")} data-testid="coder-template-options">
			{isError ? <p className={onboardingFieldErrorClass} role="alert">{t("coder.templateOptions.checkFailed")}</p> : null}
			{collapsible ? (
				<button
					type="button"
					className="flex w-fit items-center gap-1 font-medium text-foreground"
					aria-expanded={expanded}
					onClick={() => setOpen((value) => !value)}
				>
					{expanded
						? <ChevronDown aria-hidden="true" className="size-icon-sm text-muted-foreground" />
						: <ChevronRight aria-hidden="true" className="size-icon-sm text-muted-foreground" />}
					{t("coder.templateOptions.titleCount", { total: parameters.length })}
				</button>
			) : (
				<span className="font-medium text-foreground">{t("coder.templateOptions.title")}</span>
			)}
			{expanded ? (
				<div className="flex flex-col gap-4">
					{parameters.map((parameter) => (
						<TemplateParameterField
							key={parameter.name}
							parameter={parameter}
							value={effectiveParameterValue(parameter, overrides)}
							disabled={disabled}
							problem={parameter.name in overrides ? valueProblem(parameter) : undefined}
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

/** Coder's first error about a parameter's value, for showing under its field. */
function valueProblem(parameter: CloudCpCoderTemplateParameter): string | undefined {
	return parameter.diagnostics?.find((diagnostic) => diagnostic.severity === "error")?.summary?.trim() || undefined;
}

function TemplateParameterField({
	parameter,
	value,
	disabled,
	problem,
	onChange,
}: {
	parameter: CloudCpCoderTemplateParameter;
	value: string;
	disabled?: boolean;
	/** Coder's error about the current value, shown under the field. */
	problem?: string;
	onChange: (value: string) => void;
}) {
	const { t } = useTranslation();
	const id = useId();
	const hintId = `${id}-hint`;
	const label = parameter.displayName?.trim() || parameter.name;
	const description = parameter.description?.trim() ?? "";
	const options = parameter.options ?? [];
	// Coder calls a parameter required only when it has no default. A
	// single-choice parameter can't be left empty either, so it is marked too.
	const mandatory = parameter.required || (options.length > 0 && parameter.type !== "list(string)" && parameter.type !== "bool");
	const heading = (
		<Label htmlFor={id} className="text-foreground">
			<span>{label}{mandatory ? <span aria-hidden="true"> *</span> : null}</span>
			{mandatory ? <span className="sr-only">{t("coder.templateOptions.required")}</span> : null}
		</Label>
	);
	const problemId = `${id}-problem`;
	const hint = description ? <p id={hintId} className={onboardingFieldHintClass}>{description}</p> : null;
	const problemText = problem ? <p id={problemId} className={onboardingFieldErrorClass} role="alert">{problem}</p> : null;
	const describedBy = [description ? hintId : "", problem ? problemId : ""].filter(Boolean).join(" ") || undefined;

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
									// Keep the template's option order, not the click order.
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
			{problemText}
		</div>
	);
}

/**
 * A free-form list(string) as comma-separated text. The raw text is kept
 * locally so a trailing comma survives typing; the encoded list goes up.
 */
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
