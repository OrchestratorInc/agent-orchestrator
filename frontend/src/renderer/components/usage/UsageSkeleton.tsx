import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { Skeleton } from "../ui/skeleton";

const AGENT_ROWS = ["w-28", "w-20", "w-24", "w-16"];
const STATS = ["processed", "cached", "uncached", "output", "savings"];
const PAIR = [0, 1];

function Bone({ className }: { className?: string }) {
	return <Skeleton className={cn("motion-reduce:animate-none", className)} />;
}

export function UsageSkeleton({ view }: { view: "spend" | "limits" | "windows" }) {
	const { t } = useTranslation();
	return (
		<div role="status" aria-busy="true">
			<span className="sr-only">{t("usage.loading")}</span>
			{view === "limits" ? <LimitsSkeleton /> : view === "windows" ? <WindowsSkeleton /> : <SpendSkeleton />}
		</div>
	);
}

function SpendSkeleton() {
	return (
		<div className="flex flex-col gap-8">
			<section className="grid gap-8 lg:grid-cols-[minmax(0,19rem)_minmax(0,1fr)]">
				<div className="flex min-w-0 flex-col gap-5">
					<div className="flex flex-col gap-2">
						<Bone className="h-4 w-20" />
						<Bone className="h-7 w-32" />
						<Bone className="h-3 w-24" />
					</div>
					<div className="flex flex-col gap-4">
						{AGENT_ROWS.map((width) => (
							<div key={width} className="flex flex-col gap-2">
								<div className="flex items-center gap-2.5">
									<Bone className="size-4 rounded-full" />
									<Bone className={cn("h-3.5", width)} />
									<Bone className="ms-auto h-3.5 w-14" />
								</div>
								<Bone className="ms-6.5 h-1 rounded-full" />
							</div>
						))}
					</div>
				</div>
				<div className="flex min-w-0 flex-col gap-3">
					<Bone className="h-4 w-28" />
					<div className="flex gap-3">
						<div className="w-14 shrink-0" />
						<Bone className="h-[200px] flex-1" />
					</div>
				</div>
			</section>
			<section className="flex flex-col gap-3">
				<Bone className="h-4 w-16" />
				<div className="grid grid-cols-2 gap-x-6 gap-y-4 sm:grid-cols-3 lg:grid-cols-5">
					{STATS.map((stat) => (
						<div key={stat} className="flex flex-col gap-1.5">
							<Bone className="h-3 w-20" />
							<Bone className="h-5 w-16" />
						</div>
					))}
				</div>
			</section>
		</div>
	);
}

function LimitsSkeleton() {
	return (
		<div className="flex flex-col gap-10">
			{PAIR.map((section) => (
				<div key={section} className="flex flex-col gap-4">
					<div className="flex items-center gap-2">
						<Bone className="size-4 rounded-full" />
						<Bone className="h-4 w-24" />
						<Bone className="h-3 w-16" />
					</div>
					<div className="grid grid-cols-[minmax(7rem,auto)_5.5rem_minmax(0,1fr)_auto] items-center gap-x-4 gap-y-3">
						{PAIR.map((row) => (
							<div key={row} className="contents">
								<Bone className="h-3.5 w-28" />
								<Bone className="h-3.5 w-16" />
								<Bone className="h-1.5 rounded-full" />
								<Bone className="h-3 w-20" />
							</div>
						))}
					</div>
				</div>
			))}
		</div>
	);
}

function WindowsSkeleton() {
	return (
		<div className="flex flex-col gap-10">
			<Bone className="h-3 w-2/3 max-w-md" />
			<div className="flex flex-col gap-3">
				<Bone className="h-4 w-32" />
				<div className="grid grid-cols-[minmax(0,13rem)_minmax(0,1fr)] gap-x-4 gap-y-2">
					{PAIR.map((row) => (
						<div key={row} className="contents">
							<div className="flex flex-col justify-center gap-1.5">
								<Bone className="h-3.5 w-28" />
								<Bone className="h-3 w-20" />
							</div>
							<Bone className="h-11" />
						</div>
					))}
				</div>
			</div>
			<div className="flex flex-col gap-4">
				<Bone className="h-4 w-36" />
				<div className="grid gap-8 lg:grid-cols-2">
					{PAIR.map((card) => <Bone key={card} className="h-28" />)}
				</div>
			</div>
		</div>
	);
}
