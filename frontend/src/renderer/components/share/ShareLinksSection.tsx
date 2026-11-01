import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link2, Plus } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../../hooks/useCloudCp";
import { aoBridge } from "../../lib/bridge";
import type { CloudCpShareLink } from "../../lib/cloud-cp";
import { shareLinkUrl } from "../../lib/share-links";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";
import type { ShareModeCap, ShareScope } from "./accessLabels";

export type ShareAccess = "viewer" | "editor";
export type ShareExpiry = "24" | "168" | "never";

const EXPIRY_HOURS: Record<ShareExpiry, number | undefined> = { "24": 24, "168": 168, never: undefined };

/**
 * The link a choice makes: one session, or the whole project (no session).
 * Only an editor link may write; a viewer link is read-only.
 */
export function shareLinkRequest({ scope, access, modeCap, expiry, sessionId }: {
	scope: ShareScope;
	access: ShareAccess;
	modeCap: ShareModeCap;
	expiry: ShareExpiry;
	sessionId: string;
}) {
	const hours = EXPIRY_HOURS[expiry];
	return {
		...(scope === "session" ? { sessionId } : {}),
		role: access,
		accessScope: "anyone" as const,
		interaction: access === "editor" ? "interact" as const : "view" as const,
		modeCap: access === "editor" ? modeCap : "read-only" as const,
		...(hours ? { expiresInHours: hours } : {}),
	};
}

/** What an active link lets people do, naming a whole-project link as such. */
export function shareLinkLabel(link: Pick<CloudCpShareLink, "role" | "sessionId">, t: (key: "share.accessEditor" | "share.accessViewer" | "share.accessStartSessions" | "share.wholeProject") => string): string {
	if (link.sessionId) return link.role === "editor" ? t("share.accessEditor") : t("share.accessViewer");
	return `${t("share.wholeProject")} · ${link.role === "editor" ? t("share.accessStartSessions") : t("share.accessViewer")}`;
}

export function shareLinksQueryKey(baseUrl: string, orgId: string, projectId: string) {
	return ["cloud-share-links", baseUrl, orgId, projectId] as const;
}

/**
 * "Anyone with the link": the live links for this session or its project,
 * each revocable, and a way to make one. A new link is shown once: the
 * control plane keeps only a hash of its token.
 */
export function ShareLinksSection({ orgId, projectId, sessionId, scope, open }: {
	orgId: string;
	projectId: string;
	sessionId: string;
	scope: ShareScope;
	open: boolean;
}) {
	const { t } = useTranslation();
	const { client, baseUrl, ready } = useCloudCp();
	const queryClient = useQueryClient();
	const [composing, setComposing] = useState(false);
	const [access, setAccess] = useState<ShareAccess>("viewer");
	const [modeCap, setModeCap] = useState<ShareModeCap>("standard");
	const [expiry, setExpiry] = useState<ShareExpiry>("168");
	const [created, setCreated] = useState<string>();
	const [copied, setCopied] = useState(false);
	const linksKey = shareLinksQueryKey(baseUrl, orgId, projectId);
	const links = useQuery({
		queryKey: linksKey,
		enabled: open && ready,
		queryFn: async () => (await client.listShareLinks(orgId, projectId)).links,
		select: (all: CloudCpShareLink[]) => all.filter((link) => link.status === "active" && (scope === "project" ? !link.sessionId : link.sessionId === sessionId || !link.sessionId)),
	});
	const create = useMutation({
		mutationFn: async () => {
			const { link } = await client.createShareLink(orgId, projectId, shareLinkRequest({ scope, access, modeCap, expiry, sessionId }));
			if (!link.token) throw new Error(t("share.noToken"));
			return shareLinkUrl(baseUrl, orgId, link.token);
		},
		onSuccess: (url) => {
			setCreated(url);
			setCopied(false);
			setComposing(false);
			void queryClient.invalidateQueries({ queryKey: linksKey });
		},
	});
	const revoke = useMutation({
		mutationFn: (linkId: string) => client.revokeShareLink(orgId, projectId, linkId),
		onSettled: () => void queryClient.invalidateQueries({ queryKey: linksKey }),
	});
	const copy = async () => {
		if (!created) return;
		await aoBridge.clipboard.writeText(created);
		setCopied(true);
	};
	const error = create.error ?? revoke.error ?? links.error;
	const active = links.data ?? [];

	return (
		<section className="flex flex-col gap-1.5" aria-label={t("share.anyoneWithLink")} data-share-links="">
			<p className="text-xs text-muted-foreground">{t("share.anyoneWithLink")}</p>
			{active.length > 0 ? (
				<ul className="flex flex-col divide-y divide-border rounded-md bg-muted/40">
					{active.map((link) => (
						<li key={link.id} className="flex min-h-9.5 items-center gap-2.5 py-1 pr-1.5 pl-2.5 text-control">
							<Link2 className="size-icon-md shrink-0 text-muted-foreground" aria-hidden="true" />
							<span className="min-w-0 flex-1 truncate">
								{shareLinkLabel(link, t)}
								<span className="text-muted-foreground"> · {link.expiresAt
									? t("share.expiresOn", { date: new Date(link.expiresAt).toLocaleDateString() })
									: t("share.expiryNever")}</span>
							</span>
							<Button type="button" variant="ghost" size="sm" disabled={revoke.isPending} onClick={() => revoke.mutate(link.id)}>
								{t("share.revoke")}
							</Button>
						</li>
					))}
				</ul>
			) : null}
			{created ? (
				<div className="flex flex-col gap-1.5">
					<div className="flex items-center gap-2">
						<Input readOnly value={created} aria-label={t("share.link")} className="min-w-0 flex-1 font-mono text-xs" onFocus={(event) => event.currentTarget.select()} data-share-link />
						<Button type="button" variant="secondary" size="sm" onClick={() => void copy()}>
							{copied ? t("share.copied") : t("share.copy")}
						</Button>
					</div>
					<p className="text-xs text-muted-foreground">{t("share.copyOnce")}</p>
				</div>
			) : null}
			{composing ? (
				<div className="flex flex-col gap-2 rounded-md bg-muted/40 p-3" data-share-link-form="">
					<div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] items-center gap-x-3 gap-y-2">
						<Label htmlFor="share-access" className="text-control">{t("share.access")}</Label>
						<Select value={access} onValueChange={(value) => setAccess(value as ShareAccess)}>
							<SelectTrigger id="share-access" className="w-full min-w-0"><SelectValue /></SelectTrigger>
							<SelectContent>
								<SelectItem value="viewer">{t("share.accessViewer")}</SelectItem>
								<SelectItem value="editor">{scope === "project" ? t("share.accessStartSessions") : t("share.accessEditor")}</SelectItem>
							</SelectContent>
						</Select>
						{access === "editor" ? (
							<>
								<Label htmlFor="share-mode" className="text-control">{t("share.modeCap")}</Label>
								<Select value={modeCap} onValueChange={(value) => setModeCap(value as ShareModeCap)}>
									<SelectTrigger id="share-mode" className="w-full min-w-0"><SelectValue /></SelectTrigger>
									<SelectContent>
										<SelectItem value="read-only">{t("share.modeReadOnly")}</SelectItem>
										<SelectItem value="standard">{t("share.modeStandard")}</SelectItem>
										<SelectItem value="trusted">{t("share.modeTrusted")}</SelectItem>
									</SelectContent>
								</Select>
							</>
						) : null}
						<Label htmlFor="share-expiry" className="text-control">{t("share.expiry")}</Label>
						<Select value={expiry} onValueChange={(value) => setExpiry(value as ShareExpiry)}>
							<SelectTrigger id="share-expiry" className="w-full min-w-0"><SelectValue /></SelectTrigger>
							<SelectContent>
								<SelectItem value="24">{t("share.expiryDay")}</SelectItem>
								<SelectItem value="168">{t("share.expiryWeek")}</SelectItem>
								<SelectItem value="never">{t("share.expiryNever")}</SelectItem>
							</SelectContent>
						</Select>
					</div>
					<div className="flex justify-end gap-2">
						<Button type="button" variant="ghost" size="sm" onClick={() => setComposing(false)}>{t("confirm.cancel")}</Button>
						<Button type="button" variant="primary" size="sm" disabled={!ready || create.isPending} onClick={() => create.mutate()}>
							{t("share.create")}
						</Button>
					</div>
				</div>
			) : (
				<div>
					<Button
						type="button"
						variant="ghost"
						size="sm"
						className="-ml-2.5 gap-1.5"
						onClick={() => {
							setComposing(true);
							setCreated(undefined);
						}}
						data-share-create-link=""
					>
						<Plus className="size-3" aria-hidden="true" />
						{t("share.createLink")}
					</Button>
				</div>
			)}
			{error ? <p role="alert" className="text-caption leading-4 text-error">{error instanceof Error ? error.message : String(error)}</p> : null}
		</section>
	);
}
