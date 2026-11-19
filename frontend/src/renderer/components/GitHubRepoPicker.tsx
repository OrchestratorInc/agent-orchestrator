import { ChevronsUpDown, Link2, Lock, LoaderCircle } from "@/components/app-icons";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../lib/bridge";
import { Command, CommandInput, CommandItem, CommandList } from "./ui/command";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";
import { Pressable } from "./ui/pressable";

type Repo = Awaited<ReturnType<typeof aoBridge.app.listGitHubRepos>>[number];

// `gh` takes a couple of seconds per call, so the user's own repositories load
// once up front and filter instantly; GitHub-wide search fills in behind it.
let ownRepoRequest: Promise<Repo[]> | null = null;
// Held so the browser keeps the decoded avatars until the rows render them.
const preloadedAvatars = new Map<string, HTMLImageElement>();
function preloadAvatars(rows: Repo[]) {
	for (const { avatarUrl } of rows) {
		if (preloadedAvatars.has(avatarUrl)) continue;
		const image = new Image();
		image.referrerPolicy = "no-referrer";
		image.decoding = "async";
		image.src = avatarUrl;
		preloadedAvatars.set(avatarUrl, image);
	}
}

/** Starts loading the user's repositories and their avatars. Safe to call repeatedly. */
export function preloadGitHubRepos(): Promise<Repo[]> {
	ownRepoRequest ??= aoBridge.app.listGitHubRepos("").catch(() => []).then((rows) => {
		if (rows.length === 0) ownRepoRequest = null;
		preloadAvatars(rows);
		return rows;
	});
	return ownRepoRequest;
}

const MAX_ROWS = 12;

export function GitHubRepoPicker({
	disabled,
	onChange,
	value,
}: {
	disabled?: boolean;
	onChange: (remoteUrl: string) => void;
	value: string;
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const [query, setQuery] = useState("");
	const [own, setOwn] = useState<Repo[] | null>(null);
	const [remote, setRemote] = useState<Repo[]>([]);
	const [searching, setSearching] = useState(false);
	const term = query.trim();

	useEffect(() => {
		let cancelled = false;
		void preloadGitHubRepos().then((rows) => {
			if (!cancelled) setOwn(rows);
		});
		return () => {
			cancelled = true;
		};
	}, []);

	useEffect(() => {
		setRemote([]);
		if (!open || term.length < 2 || /^(https?:\/\/|git@|ssh:\/\/)/.test(term)) {
			setSearching(false);
			return;
		}
		let cancelled = false;
		setSearching(true);
		const timer = window.setTimeout(() => {
			void aoBridge.app.listGitHubRepos(term).catch(() => []).then((rows) => {
				if (cancelled) return;
				preloadAvatars(rows);
				setRemote(rows);
				setSearching(false);
			});
		}, 400);
		return () => {
			cancelled = true;
			window.clearTimeout(timer);
		};
	}, [open, term]);

	const rows = useMemo(() => {
		const needle = term.toLowerCase();
		const mine = (own ?? []).filter((repo) => !needle || repo.fullName.toLowerCase().includes(needle) || repo.description.toLowerCase().includes(needle));
		const seen = new Set(mine.map((repo) => repo.fullName.toLowerCase()));
		return [...mine, ...remote.filter((repo) => !seen.has(repo.fullName.toLowerCase()))].slice(0, MAX_ROWS);
	}, [own, remote, term]);

	const label = value.replace(/^https?:\/\/github\.com\//, "").replace(/\.git$/, "");
	const pastedUrl = /^(https?:\/\/|git@|ssh:\/\/)\S+$/.test(term) ? term : null;
	const loading = own === null || searching;

	const choose = (url: string) => {
		onChange(url);
		setOpen(false);
		setQuery("");
	};

	return (
		<Popover open={open} onOpenChange={setOpen}>
			<PopoverTrigger asChild>
				<Pressable
					id="cloneRepositoryUrl"
					role="combobox"
					aria-haspopup="listbox"
					aria-expanded={open}
					disabled={disabled}
					className="flex h-control-form w-full items-center gap-2 rounded-md border border-transparent bg-[var(--color-bg-import-card)] px-3 text-[13px] transition-colors hover:bg-foreground/5 focus-visible:bg-foreground/10 disabled:pointer-events-none disabled:opacity-50"
				>
					<Link2 className="size-4 shrink-0 text-[var(--color-text-import-muted)]" aria-hidden="true" />
					<span className={`min-w-0 flex-1 truncate ${label ? "" : "text-muted-foreground"}`}>{label || t("firstRun.chooseRepo")}</span>
					<ChevronsUpDown className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
				</Pressable>
			</PopoverTrigger>
			<PopoverContent align="start" className="z-overlay w-(--radix-popover-trigger-width) overflow-hidden p-0">
				<Command shouldFilter={false} loop>
					<CommandInput
						autoFocus
						placeholder={t("firstRun.searchRepos")}
						value={query}
						onValueChange={setQuery}
						className="h-8"
						trailing={loading ? <LoaderCircle className="size-3.5 shrink-0 animate-spin text-muted-foreground" aria-label={t("firstRun.reposLoading")} /> : null}
					/>
					<CommandList className="pb-1" style={{ maxHeight: "13rem" }}>
						{pastedUrl ? (
							<CommandItem className="min-h-7 py-0.5 text-[13px]" value={`url:${pastedUrl}`} onSelect={() => choose(pastedUrl)}>
								<Link2 aria-hidden="true" />
								<span className="min-w-0 flex-1 truncate">{t("firstRun.useUrl", { url: pastedUrl })}</span>
							</CommandItem>
						) : null}
						{rows.map((repo) => (
							<CommandItem className="min-h-7 py-0 text-[13px]" key={repo.fullName} value={repo.fullName} onSelect={() => choose(repo.url)}>
								<img alt="" className="size-icon-base shrink-0 rounded-full" decoding="async" draggable={false} src={repo.avatarUrl} referrerPolicy="no-referrer" />
								<span className="min-w-0 flex-1 truncate">{repo.fullName}</span>
								{repo.isPrivate ? <Lock aria-label={t("firstRun.privateRepo")} className="!size-3" /> : null}
							</CommandItem>
						))}
						{!loading && rows.length === 0 && !pastedUrl ? (
							<p className="px-3 py-3 text-center text-xs text-muted-foreground">{t(term ? "firstRun.noRepos" : "firstRun.signInHint")}</p>
						) : null}
					</CommandList>
				</Command>
			</PopoverContent>
		</Popover>
	);
}
