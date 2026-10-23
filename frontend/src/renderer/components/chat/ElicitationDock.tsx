import { useEffect, useId, useMemo, useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode } from "react";
import { ArrowUp, Check, ChevronDown, ChevronLeft, ExternalLink, Loader2, X } from "lucide-react";
import { aoBridge } from "../../lib/bridge";
import { cn } from "../../lib/utils";
import type { ConversationActivity } from "../../types/conversation";
import { ACCENT_ACTION_PILL, QUIET_ACTION_PILL } from "./action-pill";

type InputAction = "accept" | "decline" | "cancel";
type InputValue = string | number | boolean | string[];
type PropertyEntry = [string, Record<string, unknown>];

/**
 * Pending questions dock above the composer. After resolution, their prompt and
 * submitted answers remain readable in the transcript through ElicitationHistory.
 */
export function ElicitationDock({
	activity,
	onResolve,
}: {
	activity: ConversationActivity;
	onResolve?: (
		requestId: string,
		action: InputAction,
		content?: Record<string, unknown>,
	) => Promise<unknown> | void;
}) {
	const requestId = activity.requestId;
	const unavailable = !requestId || !onResolve;
	const [submitting, setSubmitting] = useState(false);
	const [error, setError] = useState<string>();

	async function resolve(action: InputAction, content?: Record<string, unknown>) {
		if (!requestId || !onResolve || submitting) return;
		setSubmitting(true);
		setError(undefined);
		try {
			await onResolve(requestId, action, content);
		} catch (reason) {
			setError(reason instanceof Error ? reason.message : "The answer could not be sent.");
		} finally {
			setSubmitting(false);
		}
	}

	return (
		<div
			role="group"
			aria-label="Agent question"
			data-testid="elicitation-dock"
			className="elicitation-dock animate-in fade-in-0 slide-in-from-bottom-1 duration-200 overflow-hidden rounded-[var(--radius-chat-composer)] border border-border-strong bg-surface shadow-sm"
		>
			{activity.detail?.inputMode === "url" ? (
				<URLRequest activity={activity} disabled={submitting || unavailable} onResolve={resolve} />
			) : (
				<FormRequest activity={activity} disabled={submitting || unavailable} onResolve={resolve} />
			)}

			{error ? (
				<p role="alert" className="px-3 pb-2 text-xs leading-snug text-destructive">
					{error}
				</p>
			) : null}
		</div>
	);
}

export function ElicitationHistory({ activity }: { activity: ConversationActivity }) {
	const response = activity.inputResponse;
	const answered = (activity.status === "resolved" || activity.status === "completed") && response?.action === "accept";
	const outcome = activity.status === "failed" ? "Question expired"
		: activity.status === "cancelled" || response?.action === "cancel" ? "Cancelled"
			: response?.action === "decline" ? "Skipped"
				: answered ? "Answered" : "Question closed";
	const schema = activity.detail?.schema;
	const properties = Object.entries(schema?.properties ?? {});
	const groups = claudeQuestionGroups(properties);
	const message = activity.detail?.message ?? schema?.title ?? activity.summary;
	const content = answered ? response.content : undefined;

	return (
		<section aria-label="Agent question history" className="min-w-0 space-y-3 rounded-lg border border-border bg-surface/45 p-3 text-xs leading-relaxed">
			<p className="font-medium text-muted-foreground">{outcome}</p>
			{groups ? groups.map((group) => {
				const question = group[0];
				if (!question) return null;
				return (
					<div key={question[0]} className="space-y-1">
						<p className="whitespace-pre-wrap break-words font-medium text-foreground">
							{questionPrompt(question, groups.length === 1 ? activity.detail?.message : undefined)}
						</p>
						{answered ? <HistoryAnswers properties={group} content={content} /> : null}
					</div>
				);
			}) : (
				<div className="space-y-1">
					<p className="whitespace-pre-wrap break-words font-medium text-foreground">{message}</p>
					{schema?.description ? <p className="whitespace-pre-wrap break-words">{schema.description}</p> : null}
					{answered && properties.length > 0 ? <HistoryAnswers properties={properties} content={content} /> : null}
				</div>
			)}
			{activity.detail?.inputMode === "url" && activity.detail.url ? (
				<p className="break-all text-muted-foreground">{activity.detail.url}</p>
			) : null}
		</section>
	);
}

function HistoryAnswers({ properties, content }: { properties: PropertyEntry[]; content?: Record<string, unknown> }) {
	const answers = properties.filter(([name]) => {
		const value = content?.[name];
		return value !== undefined && value !== "" && !(Array.isArray(value) && value.length === 0);
	});
	if (answers.length === 0) {
		return <p className="text-muted-foreground">{content ? "No answer provided" : "Answer unavailable"}</p>;
	}
	return (
		<dl className="space-y-1">
			{answers.map(([name, property]) => {
				const value = content?.[name];
				const options = enumOptions(property);
				function label(value: unknown): string {
					if (typeof value === "boolean") return value ? "Yes" : "No";
					if (typeof value === "number") return String(value);
					if (typeof value !== "string") return "Answer unavailable";
					const option = options.find((option) => option.value === value);
					return option ? [option.label, option.description].filter(Boolean).join(" — ") : value;
				}
				return (
					<div key={name} className="min-w-0">
						<dt className="text-muted-foreground">{propertyLabel([name, property])}</dt>
						<dd className="mt-0.5 flex min-w-0 items-start gap-2 text-foreground">
							{/* The same tile the dock marks a picked choice with. */}
							<span aria-hidden="true" className="mt-0.5 grid size-4 shrink-0 place-items-center rounded bg-logo-accent text-logo-accent-foreground">
								<Check className="size-3" />
							</span>
							<span className="min-w-0 whitespace-pre-wrap break-words">
								{Array.isArray(value) ? value.map(label).join(", ") : label(value)}
							</span>
						</dd>
					</div>
				);
			})}
		</dl>
	);
}

/** The full question stays visible above its answers, alongside the step count. */
function DockHeader({
	id,
	title,
	pager,
	onDismiss,
	disabled,
	collapsed,
	onToggleCollapsed,
}: {
	id?: string;
	title: string;
	pager?: string;
	/** Cancel lives in the header as a quiet close, not as a footer pill. */
	onDismiss?: () => void;
	disabled?: boolean;
	/** Folds the answers away so the transcript above can be read first. */
	collapsed?: boolean;
	onToggleCollapsed?: () => void;
}) {
	return (
		<div className="flex items-start gap-2 px-4 pb-2 pt-3">
			<div className="min-w-0 flex-1">
				<p className="text-caption leading-snug text-muted-foreground">
					Question{pager ? <span className="tabular-nums"> · {pager}</span> : null}
				</p>
				<p id={id} className="mt-0.5 whitespace-pre-wrap break-words text-sm font-medium leading-snug text-foreground">
					{title}
				</p>
			</div>
			{onToggleCollapsed ? (
				<button
					type="button"
					aria-label={collapsed ? "Expand question" : "Collapse question"}
					aria-expanded={!collapsed}
					title={collapsed ? "Expand" : "Collapse"}
					onClick={onToggleCollapsed}
					className="grid size-6 shrink-0 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
				>
					<ChevronDown aria-hidden="true" className={cn("size-3.5 transition-transform", collapsed && "-rotate-90")} />
				</button>
			) : null}
			{onDismiss ? (
				<button
					type="button"
					aria-label="Cancel"
					title="Cancel"
					disabled={disabled}
					onClick={onDismiss}
					className="-mr-1 grid size-6 shrink-0 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 disabled:opacity-50"
				>
					<X aria-hidden="true" className="size-3.5" />
				</button>
			) : null}
		</div>
	);
}

function DockFooter({ children }: { children: ReactNode }) {
	return <div className="flex flex-wrap items-center justify-between gap-1.5 px-4 pb-3 pt-1.5">{children}</div>;
}

function URLRequest({
	activity,
	disabled,
	onResolve,
}: {
	activity: ConversationActivity;
	disabled: boolean;
	onResolve: (action: InputAction, content?: Record<string, unknown>) => Promise<void>;
}) {
	const rawURL = activity.detail?.url ?? "";
	const parsed = safeExternalURL(rawURL);
	const [opening, setOpening] = useState(false);
	const [openError, setOpenError] = useState<string>();

	async function consent() {
		if (!parsed || opening) return;
		setOpening(true);
		setOpenError(undefined);
		try {
			// Opening is the consented action. Tell the provider only after Electron
			// accepted it, so an OS-level refusal is never reported as success.
			await aoBridge.app.openExternal(parsed.href);
			await onResolve("accept");
		} catch {
			setOpenError("The link could not be opened. Nothing was approved.");
		} finally {
			setOpening(false);
		}
	}

	return (
		<>
			<DockHeader title={activity.detail?.message || activity.summary} />
			{parsed ? (
				<div className="flex min-h-10 min-w-0 items-center gap-2.5 px-3 py-2">
					<div className="min-w-0 flex-1">
						<p className="truncate text-xs leading-relaxed text-foreground">{parsed.hostname}</p>
						<p className="mt-0.5 break-all font-mono text-caption leading-relaxed text-muted-foreground">
							{parsed.href}
						</p>
					</div>
				</div>
			) : (
				<p role="alert" className="px-3 py-2 text-xs leading-snug text-destructive">
					The provider supplied an unsafe or invalid URL. It was not opened.
				</p>
			)}
			{openError ? (
				<p role="alert" className="px-3 pb-1 text-xs leading-snug text-destructive">
					{openError}
				</p>
			) : null}
			<DockFooter>
				<div className="flex items-center gap-1.5">
					<button type="button" className={QUIET_ACTION_PILL} disabled={disabled} onClick={() => onResolve("cancel")}>
						Cancel
					</button>
					<button type="button" className={QUIET_ACTION_PILL} disabled={disabled} onClick={() => onResolve("decline")}>
						Decline
					</button>
				</div>
				<button
					type="button"
					className={ACCENT_ACTION_PILL}
					disabled={disabled || !parsed}
					onClick={() => void consent()}
				>
					{opening ? (
						<Loader2 aria-hidden="true" className="size-3.5 animate-spin" />
					) : (
						<ExternalLink aria-hidden="true" className="size-3.5" />
					)}
					Open {parsed?.hostname ?? "link"}
				</button>
			</DockFooter>
		</>
	);
}

function FormRequest({
	activity,
	disabled,
	onResolve,
}: {
	activity: ConversationActivity;
	disabled: boolean;
	onResolve: (action: InputAction, content?: Record<string, unknown>) => Promise<void>;
}) {
	const schema = activity.detail?.schema;
	const properties = useMemo(() => Object.entries(schema?.properties ?? {}), [schema?.properties]);
	const questionGroups = useMemo(() => claudeQuestionGroups(properties), [properties]);
	const otherAnswers = useMemo(() => otherAnswerFields(properties), [properties]);
	const required = useMemo(() => new Set(schema?.required ?? []), [schema?.required]);
	const [values, setValues] = useState<Record<string, InputValue>>(() => initialValues(properties));
	const [missing, setMissing] = useState<Set<string>>(new Set());
	const [activeQuestion, setActiveQuestion] = useState(0);
	const [collapsed, setCollapsed] = useState(false);
	const visibleProperties = questionGroups?.[activeQuestion] ?? properties;
	const hasPreviousQuestion = questionGroups !== undefined && activeQuestion > 0;
	const hasNextQuestion = questionGroups !== undefined && activeQuestion < questionGroups.length - 1;
	const headerId = useId();
	const formRef = useRef<HTMLFormElement>(null);
	const shownQuestion = useRef(activeQuestion);

	// Changing question unmounts the focused choice (and Back, on the first
	// question), which drops focus out of the card. Put it on the new question so
	// the next number key and Enter still reach it.
	useEffect(() => {
		if (shownQuestion.current === activeQuestion) return;
		shownQuestion.current = activeQuestion;
		const form = formRef.current;
		if (!form || form.contains(document.activeElement)) return;
		(form.querySelector<HTMLInputElement>("input:checked") ?? form.querySelector<HTMLInputElement>("input"))?.focus();
	}, [activeQuestion]);

	// Claude puts a single prompt in message and each prompt of a multi-question
	// form in its field description. The field title is only a short topic label.
	const question = questionGroups ? visibleProperties[0] : undefined;
	const title = question
		? questionPrompt(question, questionGroups?.length === 1 ? activity.detail?.message : undefined)
		: activity.detail?.message ?? schema?.title ?? activity.summary;
	const pager =
		questionGroups && questionGroups.length > 1
			? `${activeQuestion + 1} of ${questionGroups.length}`
			: undefined;

	// Number keys pick the matching choice and 0 jumps to "Other", the same
	// shortcuts the rows print. Typing in a text field is left alone.
	function pickByNumber(event: KeyboardEvent<HTMLFormElement>) {
		if (!questionGroups || disabled || event.metaKey || event.ctrlKey || event.altKey) return;
		const onChoice = event.target instanceof HTMLInputElement
			&& (event.target.type === "radio" || event.target.type === "checkbox");
		if (event.target instanceof HTMLInputElement && !onChoice) return;
		const form = event.currentTarget;
		// Browsers don't submit a form when Enter is pressed on a radio.
		if (event.key === "Enter" && onChoice) {
			event.preventDefault();
			form.requestSubmit();
			return;
		}
		if (!/^[0-9]$/.test(event.key)) return;
		const target = event.key === "0"
			? form.querySelector<HTMLInputElement>("input[type=text], input[type=number]")
			: form.querySelectorAll<HTMLInputElement>("input[type=radio], input[type=checkbox]")[Number(event.key) - 1];
		if (!target) return;
		event.preventDefault();
		if (event.key === "0") target.focus();
		else target.click();
	}

	function submit(event: FormEvent) {
		event.preventDefault();
		// A marked Other answer stands in for its question's choice; the daemon
		// accepts it in the question's place on the same marker.
		const absent = new Set(
			visibleProperties
				.filter(([name]) => {
					if (!required.has(name) || !isEmpty(values[name])) return false;
					const other = otherAnswers.get(name);
					return other === undefined || isBlank(values[other]);
				})
				.map(([name]) => name),
		);
		setMissing(absent);
		if (absent.size > 0) return;
		if (hasNextQuestion) {
			setActiveQuestion((current) => current + 1);
			return;
		}
		void onResolve("accept", values);
	}

	return (
		<form ref={formRef} onSubmit={submit} onKeyDown={pickByNumber}>
			<DockHeader
				id={headerId}
				title={title}
				pager={pager}
				disabled={disabled}
				onDismiss={() => void onResolve("cancel")}
				collapsed={collapsed}
				onToggleCollapsed={questionGroups ? () => setCollapsed((current) => !current) : undefined}
			/>
			{collapsed ? null : (
				<>
				{!questionGroups && schema?.description ? (
					<p className="px-3 pb-1 text-caption leading-relaxed text-muted-foreground">{schema.description}</p>
				) : null}
				<div className={cn(questionGroups ? "flex flex-col" : "flex flex-col gap-3 px-3 pb-1")}>
					{visibleProperties.map(([name, property], index) => (
						<FormField
							key={name}
							name={name}
							property={property}
							value={values[name]}
							required={required.has(name)}
							invalid={missing.has(name)}
							disabled={disabled}
							// Inside a Claude question the header is the prompt, so the first
							// field borrows it as its accessible name instead of printing it.
							labelledBy={questionGroups && index === 0 ? headerId : undefined}
							rows={Boolean(questionGroups)}
							onChange={(value) => {
								// A choice and its marked "Other" answer are alternatives: picking
								// one clears the other so it is always clear which one is sent.
								// Unmarked fields are independent, whatever their names.
								const question = [...otherAnswers].find(([, other]) => other === name)?.[0];
								const sibling = question ?? otherAnswers.get(name);
								setValues((current) => {
									const next: Record<string, InputValue> = { ...current, [name]: value };
									const singleChoice = sibling && !Array.isArray(current[question ?? name]) && !Array.isArray(value);
									if (singleChoice && !isBlank(value) && sibling in current) delete next[sibling];
									return next;
								});
								setMissing((current) => {
									if (!current.has(name)) return current;
									const next = new Set(current);
									next.delete(name);
									return next;
								});
							}}
						/>
					))}
				</div>
				<DockFooter>
					<div className="flex items-center gap-1">
						{hasPreviousQuestion ? (
							<button
								type="button"
								aria-label="Back"
								title="Back"
								className="grid size-7 place-items-center rounded-full text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground disabled:opacity-50"
								disabled={disabled}
								onClick={() => {
									setMissing(new Set());
									setActiveQuestion((current) => current - 1);
								}}
							>
								<ChevronLeft aria-hidden="true" className="size-4" />
							</button>
						) : null}
						{questionGroups && questionGroups.length > 1 ? (
							<div className="flex items-center gap-1 px-1" aria-hidden="true">
								{questionGroups.map((group, step) => (
									<button
										key={group[0]?.[0] ?? step}
										type="button"
										tabIndex={-1}
										disabled={disabled || step >= activeQuestion}
										onClick={() => {
											setMissing(new Set());
											setActiveQuestion(step);
										}}
										className={cn(
											"size-1.5 rounded-full transition-colors",
											step === activeQuestion
												? "bg-foreground"
												: step < activeQuestion
													? "bg-logo-accent hover:bg-logo-accent-bright"
													: "bg-border-strong",
										)}
									/>
								))}
							</div>
						) : null}
						<button
							type="button"
							className="h-7 rounded-full px-2 text-xs text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground disabled:opacity-50"
							disabled={disabled}
							onClick={() => onResolve("decline")}
						>
							Skip
						</button>					</div>
					{/* Sending an answer is the composer's send, so it wears the composer's send colour. */}
					<button
						type="submit"
						className={cn(
							ACCENT_ACTION_PILL,
							"min-w-20 justify-center bg-foreground text-background hover:bg-foreground/90",
						)}
						disabled={disabled}
					>
						{disabled ? (
							<Loader2 aria-label="Sending answer" className="size-3.5 animate-spin" />
						) : hasNextQuestion ? (
							"Next"
						) : (
							<>
								Continue
								<ArrowUp aria-hidden="true" className="size-3.5" />
							</>
						)}
					</button>
				</DockFooter>
				</>
			)}
		</form>
	);
}

function FormField({
	name,
	property,
	value,
	required,
	invalid,
	disabled,
	labelledBy,
	rows,
	onChange,
}: {
	name: string;
	property: Record<string, unknown>;
	value: InputValue | undefined;
	required: boolean;
	invalid: boolean;
	disabled: boolean;
	/** Use the dock header as this field's name instead of drawing a legend. */
	labelledBy?: string;
	/** Draw choices and free text as dock rows rather than a labelled form field. */
	rows?: boolean;
	onChange: (value: InputValue) => void;
}) {
	const label = propertyLabel([name, property]);
	const description = typeof property.description === "string" ? property.description : undefined;
	const id = `elicitation-${name}`;
	const errorId = `${id}-error`;
	const options = enumOptions(property);
	const multi = property.type === "array";

	if (options.length > 0) {
		return (
			<fieldset
				className="min-w-0"
				disabled={disabled}
				aria-required={required || undefined}
				aria-invalid={invalid || undefined}
				aria-labelledby={labelledBy}
				aria-describedby={invalid ? errorId : undefined}
			>
				{labelledBy ? null : (
					<legend className="px-3 pb-1 text-xs font-medium text-muted-foreground">
						{label}
						{required ? " *" : ""}
					</legend>
				)}
				{description && !labelledBy ? (
					<p className="px-3 pb-1 text-caption leading-relaxed text-muted-foreground">{description}</p>
				) : null}
				<div className="flex flex-col gap-0.5 px-2">
					{options.map((option, index) => {
						const checked = multi
							? Array.isArray(value) && value.includes(option.value)
							: value === option.value;
						return (
							<label
								key={option.value}
								className={cn(
									"group relative flex min-w-0 cursor-pointer items-start gap-3 rounded-lg px-2 py-2 transition-colors has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring/40",
									checked ? "bg-logo-accent/[0.1]" : "hover:bg-interactive-hover",
								)}
							>
								<input
									type={multi ? "checkbox" : "radio"}
									name={name}
									value={option.value}
									checked={checked}
									onChange={() =>
										onChange(multi ? toggleValue(Array.isArray(value) ? value : [], option.value) : option.value)
									}
									// Invisible but laid over the whole row, so a pointer on the row
									// lands on the real radio rather than on its label.
									className="absolute inset-0 m-0 size-full cursor-pointer opacity-0"
								/>
								{/* The number doubles as the keyboard shortcut and the selection mark. */}
								<span
									aria-hidden="true"
									className={cn(
										"mt-px grid size-[18px] shrink-0 place-items-center rounded-[5px] border text-[10px] font-medium leading-none tabular-nums transition-colors",
										checked
											? "border-logo-accent bg-logo-accent text-logo-accent-foreground"
											: "border-border-strong text-muted-foreground group-hover:text-foreground",
									)}
								>
									{index < 9 ? index + 1 : ""}
								</span>
								<span className="min-w-0 flex-1">
									<span className="block text-xs font-medium leading-5 text-foreground">{option.label}</span>
									{option.description ? (
										<span className="block text-caption leading-relaxed text-muted-foreground">
											{option.description}
										</span>
									) : null}
								</span>
							</label>
						);
					})}
				</div>
				{invalid ? (
					<p id={errorId} className="px-3 pt-1 text-xs text-destructive">
						Choose an answer.
					</p>
				) : null}
			</fieldset>
		);
	}

	if (property.type === "boolean") {
		return (
			<>
				<label
					className={cn(
						"flex min-h-10 min-w-0 cursor-pointer items-center gap-2.5 px-3 py-2 transition-colors hover:bg-interactive-hover",
						rows ? "" : "rounded-lg",
					)}
				>
					<input
						type="checkbox"
						checked={value === true}
						disabled={disabled}
						aria-required={required || undefined}
						aria-invalid={invalid || undefined}
						aria-describedby={invalid ? errorId : undefined}
						onChange={(event) => onChange(event.target.checked)}
						className="size-3 shrink-0 accent-[var(--logo-accent)]"
					/>
					<span className="min-w-0 flex-1">
						<span className="block text-xs leading-relaxed text-foreground">{label}</span>
						{description ? (
							<span className="mt-0.5 block text-caption leading-relaxed text-muted-foreground">
								{description}
							</span>
						) : null}
					</span>
				</label>
				{invalid ? (
					<p id={errorId} className={cn("px-3 text-xs text-destructive", rows ? "pb-1" : "pt-1")}>
						This field is required.
					</p>
				) : null}
			</>
		);
	}

	const numeric = property.type === "number" || property.type === "integer";
	const control = (
		<input
			id={id}
			type={numeric ? "number" : "text"}
			placeholder={rows ? `${label}…` : undefined}			aria-required={required || undefined}
			min={typeof property.minimum === "number" ? property.minimum : undefined}
			max={typeof property.maximum === "number" ? property.maximum : undefined}
			step={property.type === "integer" ? 1 : undefined}
			minLength={typeof property.minLength === "number" ? property.minLength : undefined}
			maxLength={typeof property.maxLength === "number" ? property.maxLength : undefined}
			value={typeof value === "string" || typeof value === "number" ? value : ""}
			disabled={disabled}
			aria-invalid={invalid || undefined}
			aria-describedby={invalid ? errorId : undefined}
			onChange={(event) => onChange(numeric && event.target.value !== "" ? Number(event.target.value) : event.target.value)}
			className={cn(
				"w-full min-w-0 rounded-lg border bg-background/40 px-2.5 text-xs leading-relaxed text-foreground outline-none transition-colors focus-visible:border-border-strong",
				rows ? "h-7 border-0 bg-transparent px-0 focus-visible:border-transparent" : "mt-1.5 h-8",
				invalid ? "border-destructive" : rows ? "border-transparent" : "border-border",
			)}
		/>
	);

	// A Claude "Other" answer is one more way to answer the question above it, so
	// it is one more row, indented into the column the choices keep for their
	// radio. Its title is a quiet visible label above the field rather than a
	// placeholder: DESIGN.md §9 allows a placeholder as an example, never as the
	// only name a field has.
	if (rows) {
		return (
			<>
				{/* The field itself is borderless here and global CSS strips rings from
				    the focused element, so the row carries focus like a choice row does. */}
				<div className="mx-2 mt-0.5 flex min-w-0 items-center gap-3 rounded-lg px-2 py-1 has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring/40">
					<span aria-hidden="true" className="grid size-[18px] shrink-0 place-items-center rounded-[5px] border text-[10px] font-medium leading-none tabular-nums transition-colors border-border-strong text-muted-foreground">
						0
					</span>
					<label htmlFor={id} className="sr-only">
						{label}
						{required ? " *" : ""}
					</label>
					<span className="min-w-0 flex-1">{control}</span>
				</div>
				{invalid ? (
					<p id={errorId} className="px-3 pb-1 text-xs text-destructive">
						This field is required.
					</p>
				) : null}
			</>
		);
	}

	return (
		<label htmlFor={id} className="block">
			<span className="text-xs font-medium text-foreground">
				{label}
				{required ? " *" : ""}
			</span>
			{description ? (
				<span className="mt-0.5 block text-caption leading-relaxed text-muted-foreground">{description}</span>
			) : null}
			{control}
			{invalid ? (
				<span id={errorId} className="mt-1 block text-xs text-destructive">
					This field is required.
				</span>
			) : null}
		</label>
	);
}

function questionPrompt(question: PropertyEntry, message?: string): string {
	const description = question[1].description;
	return (typeof description === "string" && description.trim() ? description : undefined) ?? message ?? propertyLabel(question);
}

function propertyLabel([name, property]: PropertyEntry): string {
	return typeof property.title === "string" && property.title ? property.title : humanize(name);
}

function enumOptions(property: Record<string, unknown>): Array<{ value: string; label: string; description?: string }> {
	const source = Array.isArray(property.oneOf)
		? property.oneOf
		: property.type === "array" && isRecord(property.items) && Array.isArray(property.items.anyOf)
			? property.items.anyOf
			: Array.isArray(property.enum)
				? property.enum.map((value) => ({ const: value, title: String(value) }))
				: [];
	return source.flatMap((entry) => {
		if (!isRecord(entry) || typeof entry.const !== "string") return [];
		return [{
			value: entry.const,
			label: typeof entry.title === "string" ? entry.title : entry.const,
			description: typeof entry.description === "string" ? entry.description : undefined,
		}];
	});
}

function claudeQuestionGroups(properties: PropertyEntry[]): PropertyEntry[][] | undefined {
	if (properties.length === 0) return undefined;

	const groups = new Map<number, { question?: PropertyEntry; custom?: PropertyEntry }>();
	for (const entry of properties) {
		const match = /^question_(\d+)(_custom)?$/.exec(entry[0]);
		if (!match) return undefined;
		const index = Number(match[1]);
		const group = groups.get(index) ?? {};
		if (match[2]) {
			group.custom = entry;
		} else {
			group.question = entry;
		}
		groups.set(index, group);
	}

	const ordered: PropertyEntry[][] = [];
	for (const [, group] of [...groups.entries()].sort(([left], [right]) => left - right)) {
		if (!group.question) return undefined;
		ordered.push(group.custom ? [group.question, group.custom] : [group.question]);
	}
	return ordered;
}

/**
 * Marks the free-text "Other" field an AskUserQuestion bridge (claude-agent-acp)
 * adds after each question. The daemon recognises the same marker
 * (`askUserQuestionCustomAnswerMetaKey` in chatdriver/acp).
 */
const CUSTOM_ANSWER_META_KEY = "_askUserQuestionCustomAnswer";

/** Each marked question mapped to the field that answers it in free text. */
function otherAnswerFields(properties: PropertyEntry[]): Map<string, string> {
	const names = new Set(properties.map(([name]) => name));
	const fields = new Map<string, string>();
	for (const [name, property] of properties) {
		const marker = isRecord(property._meta) ? property._meta[CUSTOM_ANSWER_META_KEY] : undefined;
		if (!isRecord(marker) || marker.isCustomAnswer !== true || typeof marker.questionId !== "string") continue;
		if (marker.questionId !== name && names.has(marker.questionId)) fields.set(marker.questionId, name);
	}
	return fields;
}

function initialValues(properties: PropertyEntry[]): Record<string, InputValue> {
	const values: Record<string, InputValue> = {};
	for (const [name, property] of properties) {
		if (
			typeof property.default === "string" ||
			typeof property.default === "number" ||
			typeof property.default === "boolean"
		) {
			values[name] = property.default;
		} else if (property.type === "array") {
			values[name] = [];
		}
	}
	return values;
}

function safeExternalURL(raw: string): URL | undefined {
	try {
		const url = new URL(raw);
		return url.protocol === "https:" || url.protocol === "http:" ? url : undefined;
	} catch {
		return undefined;
	}
}

function toggleValue(values: string[], value: string): string[] {
	return values.includes(value) ? values.filter((item) => item !== value) : [...values, value];
}

function isEmpty(value: InputValue | undefined): boolean {
	return value === undefined || value === "" || (Array.isArray(value) && value.length === 0);
}

/** Empty, or text that is only whitespace, which the adapter ignores as an Other answer. */
function isBlank(value: InputValue | undefined): boolean {
	return typeof value === "string" ? value.trim() === "" : isEmpty(value);
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null;
}

function humanize(value: string): string {
	return value.replace(/_/g, " ").replace(/^./, (letter) => letter.toUpperCase());
}
