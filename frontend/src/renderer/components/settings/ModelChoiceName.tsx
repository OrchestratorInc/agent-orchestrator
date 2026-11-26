import type { ReactNode } from "react";
import { isConcreteModelID } from "../../lib/agent-model-choices";
import { cn } from "../../lib/utils";

/** Use the exact selection value, never a group key or a placeholder label. */
export function modelChoiceIDHint(id: string | undefined, label: string): string | undefined {
	if (!id || !isConcreteModelID(id) || /^(?:auto|__.*__)$/i.test(id) || id === label) return undefined;
	return id;
}

/** The raw ID is visible, with callers exposing it as the row's accessible description. */
export function ModelChoiceName({ label, idHint, children, className, title }: {
	label: string;
	idHint?: string;
	children?: ReactNode;
	className?: string;
	title?: string;
}) {
	return <span className="min-w-0 flex-1">
		<span className={cn("block truncate", className)} title={title}>{label}</span>
		{idHint ? <code aria-hidden="true" dir="ltr" className="block wrap-anywhere whitespace-normal text-xs text-settings-muted" title={idHint}>{idHint}</code> : null}
		{children}
	</span>;
}
