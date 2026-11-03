import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { ArrowDown, ArrowUp } from "lucide-react";
import { parseDelimitedText } from "../lib/delimited-text";
import { cn } from "../lib/utils";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui/table";
import { PanelMessage } from "./WorkspaceDiffView";

type Sort = { column: number; direction: "ascending" | "descending" };

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });
// A comma is accepted only as a thousands separator (`1,000`), so a locale
// decimal like `1,5` is not read as a number.
const NUMBER = /^-?(?=\.?\d)(?:\d{1,3}(?:,\d{3})+|\d*)(?:\.\d+)?(?:e[+-]?\d+)?%?$/i;

/** A numeric cell's value without thousands separators or a percent sign. */
function cellNumber(cell: string): number {
	return Number(cell.trim().replace(/[,%]/g, ""));
}

/**
 * A CSV or TSV file's rendered view: the first row is the header, and clicking
 * a header sorts by that column (ascending, descending, then file order).
 */
export function TableFileView({ content, path }: { content: string; path: string }) {
	const { t } = useTranslation();
	const [sort, setSort] = useState<Sort | null>(null);
	const { header, rows, numeric } = useMemo(() => {
		const [first = [], ...rest] = parseDelimitedText(content, path);
		const width = rest.reduce((max, row) => Math.max(max, row.length), first.length);
		const pad = (row: string[]) => Array.from({ length: width }, (_, index) => row[index] ?? "");
		const body = rest.map(pad);
		// Numeric columns right-align so their digits line up.
		const numericColumns = Array.from({ length: width }, (_, column) => {
			const values = body.map((row) => row[column].trim()).filter(Boolean);
			return values.length > 0 && values.every((value) => NUMBER.test(value));
		});
		return { header: pad(first), rows: body, numeric: numericColumns };
	}, [content, path]);
	const sortedRows = useMemo(() => {
		if (!sort) return rows;
		const order = sort.direction === "ascending" ? 1 : -1;
		// Numeric columns sort by value, text columns by the collator; empty cells stay last either way.
		const compare = numeric[sort.column]
			? (a: string, b: string) => cellNumber(a) - cellNumber(b)
			: (a: string, b: string) => collator.compare(a, b);
		const keyed = rows.map((row) => ({ row, cell: row[sort.column], empty: !row[sort.column].trim() }));
		keyed.sort((a, b) => a.empty ? (b.empty ? 0 : 1) : b.empty ? -1 : order * compare(a.cell, b.cell));
		return keyed.map(({ row }) => row);
	}, [numeric, rows, sort]);

	if (header.length === 0) return <PanelMessage>{t("files.emptyTable")}</PanelMessage>;
	const toggleSort = (column: number) => setSort((current) =>
		current?.column !== column ? { column, direction: "ascending" }
			: current.direction === "ascending" ? { column, direction: "descending" }
			: null);
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
									<button
										className={cn("inline-flex h-full w-full items-center gap-1 rounded-xs px-2 text-left hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring", numeric[column] && "flex-row-reverse text-right", !sorted && "text-muted-foreground")}
										onClick={() => toggleSort(column)}
										type="button"
									>
										<span className="truncate">{label}</span>
										<SortIcon aria-hidden="true" className={cn("size-icon-xs shrink-0", !sorted && "invisible")} />
									</button>
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
