import { useEffect, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { GitHubIssueChoice } from "../../shared/github-issue-artifact";
import { aoBridge } from "../lib/bridge";
import { gitHubFullName } from "../lib/cloud-project-repos";
import { GitHubRepoPicker } from "./GitHubRepoPicker";
import { Button } from "./ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./ui/dialog";
import { Input } from "./ui/input";

function parseIssueLink(value: string) {
	const match = /^https:\/\/(?:www\.)?github\.com\/([a-z\d-]+\/[\w.-]+)\/issues\/([1-9]\d*)\/?(?:[?#]\S*)?$/i.exec(value.trim());
	return match && { repository: match[1], number: match[2] };
}

export function GitHubIssueArtifactDialog({ name, readContent, onClose }: {
	name: string;
	readContent: () => Promise<string>;
	onClose: () => void;
}) {
	const { t } = useTranslation();
	const [remote, setRemote] = useState("");
	const [query, setQuery] = useState("");
	const [term, setTerm] = useState("");
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
		enabled: Boolean(repo) && !picked && !invalidLink && (Boolean(link) || term === query.trim()),
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
			return;
		}
		setRemote(value);
		setQuery("");
		setTerm("");
		setSelected(undefined);
		add.reset();
	};
	const loading = issues.isFetching || (!link && term !== query.trim());
	return (
		<Dialog open onOpenChange={(open) => { if (!open && !add.isPending) onClose(); }}>
			<DialogContent data-browser-native-overlay="true" showCloseButton={!add.isPending}>
				<DialogTitle>{t(add.isSuccess ? "issueArtifact.added" : "issueArtifact.add")}</DialogTitle>
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
						<Input id="issue-artifact-number" disabled={add.isPending} placeholder={t("issueArtifact.search")} value={picked ? `#${picked.number} · ${picked.title}` : query}
							onChange={(event) => editIssue(event.target.value)}
							onFocus={(event) => { if (selected) event.target.select(); }} />
						{link && selected && <a className="inline-flex max-w-full items-center gap-1.5 rounded px-2 py-1 text-sm text-link hover:underline"
							href={`https://github.com/${repo}/issues/${selected.number}`} onClick={event => {
								event.preventDefault();
								void aoBridge.app.openExternal(event.currentTarget.href);
							}}>
							<span>#{selected.number}</span><span className="truncate">{selected.title}</span>
						</a>}
						{invalidLink && <p className="px-2 py-1 text-xs text-muted-foreground" role="status">{t("issueArtifact.linkHint")}</p>}
						{!repo && !invalidLink && query.trim() && <p role="status" className="text-xs text-muted-foreground">{t("issueArtifact.pickRepo")}</p>}
						{repo && !selected && !invalidLink && <div className="max-h-44 overflow-y-auto">
							{loading ? <p role="status" className="px-2 py-2 text-xs text-muted-foreground">{t("common.loading")}</p> : issues.error ? <p role="alert" className="px-2 py-2 text-xs text-destructive">{/HTTP 404/i.test(issues.error.message) ? t("issueArtifact.unavailable") : issues.error.message}</p> : issues.data?.length ? issues.data.map((issue) =>
								<Button key={issue.number} className="w-full justify-start" variant="ghost" size="sm" onClick={() => setSelected(issue)}>
									<span className="text-muted-foreground">#{issue.number}</span><span className="truncate">{issue.title}</span>
								</Button>) : <p role="status" className="rounded-md bg-muted/40 px-3 py-2 text-xs text-muted-foreground">{t(link ? "issueArtifact.linkHint" : "issueArtifact.empty")}</p>}
						</div>}
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
