import { Pencil, type AppIcon } from "@/components/app-icons";
import { useEffect, useRef, useState, type ComponentPropsWithoutRef, type KeyboardEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { Pressable } from "../ui/pressable";

function SettingsRowLabel({
	icon: Icon,
	leading,
	label,
	description,
}: {
	icon?: AppIcon;
	leading?: ReactNode;
	label: ReactNode;
	description?: ReactNode;
}) {
	const lead = leading ? (
		<div className="flex shrink-0 items-center self-center">{leading}</div>
	) : null;
	if (description === undefined) {
		return (
			<div className="flex min-w-0 flex-1 items-center gap-(--size-settings-row-icon-gap)">
				{lead}
				{Icon ? <Icon className="size-icon-lg shrink-0 text-settings-muted" aria-hidden="true" /> : null}
				<span className="min-w-0 truncate text-sm leading-5 text-settings-label">{label}</span>
			</div>
		);
	}
	return (
		<div className="flex min-w-0 flex-1 items-start gap-(--size-settings-row-icon-gap)">
			{lead}
			{Icon ? <Icon className="mt-0.5 size-icon-lg shrink-0 text-settings-muted" aria-hidden="true" /> : null}
			<div className="min-w-0 flex-1">
				<div className="text-sm leading-5 text-settings-label">{label}</div>
				<div className="mt-0.5 text-pretty text-control leading-4 text-settings-muted">{description}</div>
			</div>
		</div>
	);
}

export function SettingsRow({
	icon,
	leading,
	label,
	description,
	children,
	className,
	...rowProps
}: Omit<ComponentPropsWithoutRef<"div">, "children"> & {
	icon?: AppIcon;
	leading?: ReactNode;
	label: ReactNode;
	description?: ReactNode;
	children?: ReactNode;
	className?: string;
}) {
	return (
		<div
			{...rowProps}
			className={cn(
				"settings-row-bar",
				className,
			)}
		>
			<SettingsRowLabel icon={icon} leading={leading} label={label} description={description} />
			<div
				className={cn(
					"flex max-w-[60%] shrink-0 items-center justify-end [--size-settings-inline-input-max-percent:100%] [--size-settings-row-value-max-percent:100%]",
					description !== undefined && "self-center",
				)}
			>
				{children}
			</div>
		</div>
	);
}

export function SettingsInlineInput({
	id,
	label,
	value,
	onChange,
	onCommit,
	onCancel,
	placeholder,
	className,
}: {
	id: string;
	label: string;
	value: string;
	onChange: (value: string) => void;
	onCommit?: (value: string) => void;
	onCancel?: () => void;
	placeholder?: string;
	className?: string;
}) {
	const { t } = useTranslation();
	const [editing, setEditing] = useState(false);
	const inputRef = useRef<HTMLInputElement>(null);

	useEffect(() => {
		if (!editing) return;
		const input = inputRef.current;
		if (!input) return;
		input.focus();
		input.select();
	}, [editing]);

	const finishEditing = () => {
		setEditing(false);
		onCommit?.(value);
	};

	const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
		if (event.key === "Enter") {
			event.preventDefault();
			finishEditing();
			return;
		}
		if (event.key === "Escape") {
			event.preventDefault();
			event.stopPropagation();
			setEditing(false);
			onCancel?.();
		}
	};

	if (!editing) {
		return (
			<Pressable
				className={cn("settings-inline-edit-trigger", className)}
				aria-label={t("settings.field.edit", { label })}
				onClick={() => setEditing(true)}
			>
				<span className="settings-row-value" title={value || placeholder}>
					{value || placeholder}
				</span>
				<Pencil className="settings-inline-edit-icon" aria-hidden="true" />
			</Pressable>
		);
	}

	return (
		<input
			ref={inputRef}
			id={id}
			aria-label={label}
			className={cn("settings-inline-edit-input", className)}
			value={value}
			onChange={(event) => onChange(event.target.value)}
			onBlur={finishEditing}
			onKeyDown={onKeyDown}
			placeholder={placeholder}
		/>
	);
}

export function SettingsInputRow({
	icon,
	label,
	id,
	value,
	onChange,
	onCommit,
	onCancel,
	placeholder,
}: {
	icon?: AppIcon;
	label: string;
	id: string;
	value: string;
	onChange: (value: string) => void;
	onCommit?: (value: string) => void;
	onCancel?: () => void;
	placeholder?: string;
}) {
	return (
		<SettingsRow icon={icon} label={label}>
			<SettingsInlineInput
				id={id}
				label={label}
				value={value}
				onChange={onChange}
				onCommit={onCommit}
				onCancel={onCancel}
				placeholder={placeholder}
			/>
		</SettingsRow>
	);
}
