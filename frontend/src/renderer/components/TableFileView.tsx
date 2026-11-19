import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { ArrowDown, ArrowUp } from "@/components/app-icons";
import { delimitedTable, nextTableSort, sortTableRows, type TableSort } from "../../../../packages/shared/files/file-formats";
import { cn } from "../lib/utils";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui/table";
import { Pressable } from "./ui/pressable";
import { PanelMessage } from "./WorkspaceDiffView";

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });

/**
 * A CSV or TSV file's rendered view: the first row is the header, and clicking
 * a header sorts by that column (ascending, descending, then file order).
 */
export function TableFileView({ content, path }: { content: string; path: string }) {
	const { t } = useTranslation();
	const [sort, setSort] = useState<TableSort | null>(null);
	// Numeric columns right-align so their digits line up.
	const { header, rows, numeric } = useMemo(() => delimitedTable(content, path), [content, path]);
	const sortedRows = useMemo(() => sortTableRows(rows, numeric, sort, collator.compare), [numeric, rows, sort]);

	if (header.length === 0) return <PanelMessage>{t("files.emptyTable")}</PanelMessage>;
	const toggleSort = (column: number) => setSort((current) => nextTableSort(current, column));
	return (
		<div className="p-3" data-selection-feedback="">
			<Table className="text-xs">
				<TableHeader>
					<TableRow className="hover:bg-transparent">
						{header.map((label, column) => {
							const sorted = sort?.column === column ? sort.direction : undefined;
							const SortIcon = sorted === "descending" ? ArrowDown : ArrowUp;
							return (
								<TableHead aria-sort={sorted ?? "none"} className={cn("px-0", numeric[column] && "text-right")} key={column}>
									<Pressable
										className={cn("inline-flex h-full w-full items-center gap-1 rounded-xs px-2 hover:text-foreground", numeric[column] && "flex-row-reverse text-right", !sorted && "text-muted-foreground")}
										onClick={() => toggleSort(column)}
									>
										<span className="truncate">{label}</span>
										<SortIcon aria-hidden="true" className={cn("size-icon-xs shrink-0", !sorted && "invisible")} />
									</Pressable>
								</TableHead>
							);
						})}
					</TableRow>
				</TableHeader>
				<TableBody>
					{sortedRows.map((row, index) => (
						<TableRow key={index}>
							{row.map((cell, column) => (
								<TableCell className={cn("max-w-[32rem] align-top whitespace-pre-wrap break-words", numeric[column] && "text-right tabular-nums")} key={column}>
									{cell}
								</TableCell>
							))}
						</TableRow>
					))}
				</TableBody>
			</Table>
		</div>
	);
}
