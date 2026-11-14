import { Check, ChevronDown, Eye, PencilLine, X } from "@/components/app-icons";
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
import { Button } from "../ui/button";

const ALL_CAPS: readonly ShareModeCap[] = ["read-only", "standard", "trusted"];

/**
 * One row's access, as a borderless trigger like the Settings selects: the
 * level, and for writing the agent's permissions. The menu holds the two
 * levels, the agent's permissions while writing, and removing access.
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
	caps = ALL_CAPS,
	describedBy,
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
	/** The agent permissions offered while writing; a link never offers Trusted. */
	caps?: readonly ShareModeCap[];
	/** The id of text that explains this access, read with the trigger. */
	describedBy?: string;
}) {
	const { t } = useTranslation();
	const level = roleLabel(t, role, scope);
	const label = triggerLabel ?? level;
	// Picking what is already set changes nothing, so it asks for nothing.
	const choose = (next: { role: CloudCpShareRole; modeCap: ShareModeCap }) => {
		if (next.role === role && next.modeCap === modeCap) return;
		onChange(next);
	};
	return (
		<DropdownMenu modal={false}>
			<DropdownMenuTrigger asChild disabled={disabled}>
				<Button
					type="button"
					variant="quiet"
					size="none"
					className="h-7 max-w-full gap-1 px-2 text-control"
					aria-label={t("share.changeAccess", { access: label })}
					aria-describedby={describedBy}
					data-access-menu=""
				>
					<span className="truncate">{label}</span>
					{/* Standard is what writing means by default; only another cap is worth naming. */}
					{showCap && role === "editor" && modeCap !== "standard" ? <span className="text-passive">· {capLabel(t, modeCap)}</span> : null}
					<ChevronDown className="size-3 shrink-0" aria-hidden="true" />
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align={align} className="w-68">
				<AccessOption
					icon={<Eye />}
					label={roleLabel(t, "viewer", scope)}
					description={t("share.accessViewerDescription")}
					selected={role === "viewer"}
					onSelect={() => choose({ role: "viewer", modeCap: "read-only" })}
				/>
				<AccessOption
					icon={<PencilLine />}
					label={roleLabel(t, "editor", scope)}
					description={scope === "project" ? t("share.accessStartSessionsDescription") : t("share.accessEditorDescription")}
					selected={role === "editor"}
					onSelect={() => choose({ role: "editor", modeCap: role === "editor" && caps.includes(modeCap) ? modeCap : "standard" })}
				/>
				{role === "editor" ? (
					<>
						<DropdownMenuSeparator />
						<DropdownMenuLabel>{t("share.modeCap")}</DropdownMenuLabel>
						{caps.map((cap) => (
							<DropdownMenuItem key={cap} onSelect={() => choose({ role: "editor", modeCap: cap })} data-access-cap={cap}>
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
							<span>{stopLabel ?? t("share.removeAccess")}</span>
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
				<Button type="button" variant="ghost" size="none" className="h-7 gap-1 px-2 text-control text-foreground/80 hover:text-foreground" aria-label={t("share.modeCap")} data-cap-menu="">
					{capLabel(t, value)}
					<ChevronDown className="size-3" aria-hidden="true" />
				</Button>
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
