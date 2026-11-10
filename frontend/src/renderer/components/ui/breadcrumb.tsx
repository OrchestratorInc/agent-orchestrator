import { ChevronRightIcon } from "@/components/app-icons";
import * as React from "react";
import { useTranslation } from "react-i18next";

import { cn } from "@/lib/utils";

function Breadcrumb({ ...props }: React.ComponentProps<"nav">) {
	const { t } = useTranslation();
	return <nav aria-label={t("common.breadcrumb")} data-slot="breadcrumb" {...props} />;
}

function BreadcrumbList({ className, ...props }: React.ComponentProps<"ol">) {
	return <ol data-slot="breadcrumb-list" className={cn("flex min-w-0 items-baseline gap-2", className)} {...props} />;
}

function BreadcrumbItem({ className, ...props }: React.ComponentProps<"li">) {
	return <li data-slot="breadcrumb-item" className={cn("inline-flex min-w-0 items-baseline", className)} {...props} />;
}

function BreadcrumbPage({ className, ...props }: React.ComponentProps<"span">) {
	return (
		<span
			aria-current="page"
			data-slot="breadcrumb-page"
			className={cn("truncate font-bold text-foreground", className)}
			{...props}
		/>
	);
}

function BreadcrumbSeparator({ children, className, ...props }: React.ComponentProps<"li">) {
	return (
		<li
			aria-hidden="true"
			data-slot="breadcrumb-separator"
			className={cn("inline-flex shrink-0 items-center text-passive [&>svg]:size-3.5", className)}
			{...props}
		>
			{children ?? <ChevronRightIcon />}
		</li>
	);
}

export { Breadcrumb, BreadcrumbItem, BreadcrumbList, BreadcrumbPage, BreadcrumbSeparator };
