import { Search } from "@/components/app-icons";
import type { ReactNode } from "react";
import { Input } from "../ui/input";

export function SettingsSearch({
	value,
	onChange,
	placeholder,
	"aria-label": ariaLabel,
	trailing,
}: {
	value: string;
	onChange: (value: string) => void;
	placeholder?: string;
	"aria-label": string;
	trailing?: ReactNode;
}) {
	return (
		<div className="flex items-center gap-2 pb-3">
			<div className="relative min-w-0 flex-1">
				<Search
					aria-hidden="true"
					className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-settings-muted"
				/>
				<Input
					aria-label={ariaLabel}
					className="h-control-form rounded-md border border-(--color-border-settings-input) bg-(--color-bg-settings-input) pl-9 pr-3 text-settings-label placeholder:text-settings-muted"
					onChange={(event) => onChange(event.target.value)}
					placeholder={placeholder}
					type="search"
					value={value}
				/>
			</div>
			{trailing}
		</div>
	);
}
