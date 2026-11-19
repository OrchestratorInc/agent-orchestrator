import { useEffect, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { GitHubIssueChoice } from "../../shared/github-issue-artifact";
import { aoBridge } from "../lib/bridge";
import { gitHubFullName } from "../lib/cloud-project-repos";
import { GitHubRepoPicker } from "./GitHubRepoPicker";
import { Button } from "./ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./ui/dialog";
import { Check, ChevronsUpDown, CircleDot, ExternalLink } from "@/components/app-icons";
import { Command, CommandInput, CommandItem, CommandList } from "./ui/command";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";
import { Pressable } from "./ui/pressable";

function parseIssueLink(value: string) {
	const match = /^https:\/\/(?:www\.)?github\.com\/([a-z\d-]+\/[\w.-]+)\/issues\/([1-9]\d*)\/?(?:[?#]\S*)?$/i.exec(value.trim());
	return match && { repository: match[1], number: match[2] };
}

export function GitHubIssueArtifactDialog({ name, readContent, onClose, returnFocus, actionLabel }: {
	name: string;
	readContent: () => Promise<string>;
	onClose: () => void;
	returnFocus?: HTMLElement;
	actionLabel?: string;
}) {
	const { t } = useTranslation();
	const [remote, setRemote] = useState("");
	const [query, setQuery] = useState("");
	const [term, setTerm] = useState("");
	const [pickerOpen, setPickerOpen] = useState(false);
	const [picked, setSelected] = useState<GitHubIssueChoice>();
	const repo = gitHubFullName(remote);
	const link = parseIssueLink(query);
	const invalidLink = /^https?:\/\//i.test(query.trim()) && !link;
	const search = link?.number ?? term;
	useEffect(() => {
		const timer = setTimeout(() => setTerm(query.trim()), 250);
		return () => clearTimeout(timer);
	}, [query]);
	const issues = useQuery({
		queryKey: ["github-artifact-issues", repo, search],
		queryFn: () => aoBridge.app.findArtifactIssues(repo!, search),
		enabled: Boolean(repo) && (pickerOpen || Boolean(link)) && !invalidLink && (Boolean(link) || term === query.trim()),
		retry: false,
	});
	const selected = link ? issues.data?.find(issue => String(issue.number) === link.number) : picked;
	const editIssue = (value: string) => {
		const pasted = parseIssueLink(value);
		if (pasted) setRemote(`https://github.com/${pasted.repository}`);
		setSelected(undefined);
		setQuery(value);
		add.reset();
	};
	const add = useMutation({
		mutationFn: async () => {
			if (!repo || !selected) throw new Error(t("issueArtifact.selectIssue"));
			return aoBridge.app.publishIssueArtifact({ repository: repo, issue: selected.number, name, body: await readContent() });
		},
		retry: false,
	});
	const chooseRepo = (value: string) => {
		if (parseIssueLink(value)) {
			editIssue(value);
			setPickerOpen(false);
			return;
		}
		setRemote(value);
		setQuery("");
		setTerm("");
		setSelected(undefined);
		add.reset();
	};
	const loading = issues.isFetching || (!link && term !== query.trim());
	const status = invalidLink ? t("issueArtifact.linkHint")
		: !repo ? t("issueArtifact.pickRepo")
		: loading ? t("common.loading")
		: issues.error ? (/HTTP 404/i.test(issues.error.message) ? t("issueArtifact.unavailable") : issues.error.message)
		: !issues.data?.length ? t(link ? "issueArtifact.linkHint" : "issueArtifact.empty")
		: null;
	const pasteLink = (event: React.ClipboardEvent) => {
		const value = event.clipboardData.getData("text/plain");
		if (!parseIssueLink(value)) return;
		event.preventDefault();
		editIssue(value);
		setPickerOpen(false);
	};
	return (
		<Dialog open onOpenChange={(open) => { if (!open && !add.isPending) onClose(); }}>
			<DialogContent data-browser-native-overlay="true" showCloseButton={!add.isPending} onCloseAutoFocus={event => {
				if (!returnFocus?.isConnected) return;
				event.preventDefault();
				returnFocus.focus();
			}}>
				<DialogTitle>{add.isSuccess ? t("issueArtifact.added") : actionLabel ?? t("issueArtifact.add")}</DialogTitle>
				<DialogDescription>{add.isSuccess ? `${repo}#${selected?.number} · ${selected?.title}` : t("issueArtifact.copy", { name })}</DialogDescription>
				{add.isSuccess ? (
					<Button onClick={() => void aoBridge.app.openExternal(add.data)}>{t("issueArtifact.view")}</Button>
				) : <>
					<div className="space-y-1">
						<label htmlFor="cloneRepositoryUrl">{t("issueArtifact.repo")}</label>
						<GitHubRepoPicker disabled={add.isPending} value={remote} onChange={chooseRepo} />
						{remote && !repo && <p role="alert">{t("issueArtifact.invalidRepo")}</p>}
					</div>
					<div className="space-y-1">
						<label htmlFor="issue-artifact-number">{t("issueArtifact.issue")}</label>
						<Popover open={pickerOpen} onOpenChange={setPickerOpen}>
							<PopoverTrigger asChild>
								<Pressable id="issue-artifact-number" role="combobox" aria-haspopup="listbox" aria-expanded={pickerOpen}
									disabled={add.isPending} onPaste={pasteLink}
									className="flex h-control-form w-full cursor-pointer items-center gap-2 rounded-md border border-border bg-[var(--color-bg-import-card)] px-3 text-sm hover:border-foreground/40 hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring data-[state=open]:border-foreground/40 disabled:cursor-default">
									<CircleDot className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
									<span className="min-w-0 flex-1 truncate">{picked ? `#${picked.number} · ${picked.title}` : query || t("issueArtifact.search")}</span>
									<ChevronsUpDown className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
								</Pressable>
							</PopoverTrigger>
							<PopoverContent align="start" className="z-overlay w-(--radix-popover-trigger-width) overflow-hidden p-0">
								<Command shouldFilter={false} loop>
									<CommandInput autoFocus aria-label={t("issueArtifact.search")} placeholder={t("issueArtifact.search")} value={query}
										onValueChange={editIssue} onPaste={pasteLink} onFocus={event => event.target.select()} />
									<CommandList className="max-h-44 p-1">
										{status ? <p role={issues.error ? "alert" : "status"} className="px-2 py-2 text-xs text-muted-foreground">{status}</p> : issues.data?.map(issue =>
											<CommandItem key={issue.number} value={String(issue.number)} className="cursor-pointer gap-2 rounded-sm px-2 py-2 hover:bg-accent data-[selected=true]:bg-accent" onSelect={() => {
												if (!link) {
													setSelected(issue);
													setQuery("");
													setTerm("");
												}
												setPickerOpen(false);
											}}>
												<span className="shrink-0 text-muted-foreground">#{issue.number}</span>
												<span className="min-w-0 flex-1 truncate">{issue.title}</span>
												{issue.number === selected?.number && <Check className="size-4" aria-hidden="true" />}
											</CommandItem>)}
									</CommandList>
								</Command>
							</PopoverContent>
						</Popover>
						{!pickerOpen && link && !selected && status && <p role="status" className="px-2 py-1 text-xs text-muted-foreground">{status}</p>}
						{selected && <a className="inline-flex max-w-full cursor-pointer items-center gap-1.5 rounded px-1 py-1 text-sm underline underline-offset-2 hover:text-foreground/70 focus-visible:ring-2 focus-visible:ring-ring"
							href={`https://github.com/${repo}/issues/${selected.number}`} onClick={event => {
								event.preventDefault();
								void aoBridge.app.openExternal(event.currentTarget.href);
							}}>
							<span className="shrink-0">#{selected.number}</span><span className="truncate">{selected.title}</span>
							<ExternalLink className="size-3.5 shrink-0" aria-hidden="true" />
						</a>}
					</div>
					{add.error && <p role="alert" className="text-destructive">{add.error.message}</p>}
					<Button className="self-end" disabled={!selected || add.isPending} onClick={() => add.mutate()}>
						{add.isPending ? t("issueArtifact.adding") : selected ? t("issueArtifact.confirmNumber", { number: selected.number }) : t("issueArtifact.confirm")}
					</Button>
				</>}
			</DialogContent>
		</Dialog>
	);
}
