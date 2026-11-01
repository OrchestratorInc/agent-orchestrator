import { Check, ChevronDown, Eye, PencilLine, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import type { CloudCpShareRole } from "../../lib/cloud-cp";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { capLabel, roleLabel, type ShareModeCap, type ShareScope } from "./accessLabels";

/**
 * One row's access, as a borderless trigger like the Settings selects: the
 * level, and for writing its limit. The menu holds the two levels, the limit
 * while writing, and stopping the share.
 */
export function AccessMenu({
	role,
	modeCap,
	scope,
	onChange,
	onStop,
	stopLabel,
	disabled,
	showCap = true,
	triggerLabel,
	align = "end",
}: {
	role: CloudCpShareRole;
	modeCap: ShareModeCap;
	scope: ShareScope;
	onChange: (next: { role: CloudCpShareRole; modeCap: ShareModeCap }) => void;
	onStop?: () => void;
	stopLabel?: string;
	disabled?: boolean;
	/** Shows the limit in the trigger while writing. */
	showCap?: boolean;
	triggerLabel?: string;
	align?: "start" | "end";
}) {
	const { t } = useTranslation();
	const level = roleLabel(t, role, scope);
	const label = triggerLabel ?? level;
	return (
		<DropdownMenu modal={false}>
			<DropdownMenuTrigger asChild disabled={disabled}>
				<button
					type="button"
					className={cn(
						"inline-flex h-7 max-w-full shrink-0 items-center gap-1 rounded-md px-2 text-control whitespace-nowrap text-muted-foreground outline-none transition-colors duration-(--duration-fast)",
						"hover:bg-interactive-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring data-[state=open]:bg-interactive-active data-[state=open]:text-foreground",
						disabled && "pointer-events-none opacity-60",
					)}
					aria-label={t("share.changeAccess", { access: label })}
					data-access-menu=""
				>
					<span className="truncate">{label}</span>
					{showCap && role === "editor" ? <span className="text-passive">· {capLabel(t, modeCap)}</span> : null}
					<ChevronDown className="size-3 shrink-0" aria-hidden="true" />
				</button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align={align} className="w-68">
				<AccessOption
					icon={<Eye />}
					label={roleLabel(t, "viewer", scope)}
					description={t("share.accessViewerDescription")}
					selected={role === "viewer"}
					onSelect={() => onChange({ role: "viewer", modeCap: "read-only" })}
				/>
				<AccessOption
					icon={<PencilLine />}
					label={roleLabel(t, "editor", scope)}
					description={scope === "project" ? t("share.accessStartSessionsDescription") : t("share.accessEditorDescription")}
					selected={role === "editor"}
					onSelect={() => onChange({ role: "editor", modeCap: role === "editor" ? modeCap : "standard" })}
				/>
				{role === "editor" ? (
					<>
						<DropdownMenuSeparator />
						<DropdownMenuLabel>{t("share.modeCap")}</DropdownMenuLabel>
						{(["read-only", "standard", "trusted"] as const).map((cap) => (
							<DropdownMenuItem key={cap} onSelect={() => onChange({ role: "editor", modeCap: cap })} data-access-cap={cap}>
								<span className={cn("flex-1", cap === modeCap ? "text-foreground" : "")}>{capLabel(t, cap)}</span>
								{cap === modeCap ? <Check className="text-foreground!" aria-hidden="true" /> : null}
							</DropdownMenuItem>
						))}
					</>
				) : null}
				{onStop ? (
					<>
						<DropdownMenuSeparator />
						<DropdownMenuItem onSelect={onStop} className="text-destructive focus:text-destructive [&_svg]:text-destructive!" data-access-stop="">
							<X aria-hidden="true" />
							<span>{stopLabel ?? t("share.stopSharing")}</span>
						</DropdownMenuItem>
					</>
				) : null}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

function AccessOption({ icon, label, description, selected, onSelect }: {
	icon: React.ReactNode;
	label: string;
	description: string;
	selected: boolean;
	onSelect: () => void;
}) {
	return (
		<DropdownMenuItem onSelect={onSelect} className={cn("items-start py-1.5", selected && "bg-interactive-active")} data-access-level={selected ? "selected" : ""}>
			<span className="mt-0.5">{icon}</span>
			<span className="flex min-w-0 flex-1 flex-col">
				<span className="text-foreground">{label}</span>
				<span className="text-xs text-muted-foreground">{description}</span>
			</span>
			{selected ? <Check className="mt-0.5 text-foreground!" aria-hidden="true" /> : null}
		</DropdownMenuItem>
	);
}

/** Just the limit while writing, for the share dialog's confirm step. */
export function CapMenu({ value, onChange }: { value: ShareModeCap; onChange: (cap: ShareModeCap) => void }) {
	const { t } = useTranslation();
	return (
		<DropdownMenu modal={false}>
			<DropdownMenuTrigger asChild>
				<button
					type="button"
					className="inline-flex h-7 items-center gap-1 rounded-md px-2 text-control text-foreground/80 outline-none hover:bg-interactive-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring data-[state=open]:bg-interactive-active"
					aria-label={t("share.modeCap")}
					data-cap-menu=""
				>
					{capLabel(t, value)}
					<ChevronDown className="size-3" aria-hidden="true" />
				</button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-48">
				{(["read-only", "standard", "trusted"] as const).map((cap) => (
					<DropdownMenuItem key={cap} onSelect={() => onChange(cap)}>
						<span className={cn("flex-1", cap === value ? "text-foreground" : "")}>{capLabel(t, cap)}</span>
						{cap === value ? <Check className="text-foreground!" aria-hidden="true" /> : null}
					</DropdownMenuItem>
				))}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
