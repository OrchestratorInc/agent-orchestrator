// App-wide icon set. Icons come from Myna UI (@mynaui/icons-react). The few
// Myna doesn't draw yet fall back to lucide at Myna's 1.5 stroke so they sit
// in the same visual family. Import icons from here, never from either
// library directly, so the whole app switches sets in one place.
//
// Every icon carries an `icon icon-<name>` class (e.g. `icon-folder-open`)
// that tests and styles can target without depending on the library.
//
// Generated from the lucide names the app used; add new icons by hand.
import { forwardRef, type ForwardRefExoticComponent, type RefAttributes, type SVGProps } from "react";
import {
	Activity as MynaActivity,
	Archive as MynaArchive,
	ArrowDown as MynaArrowDown,
	ArrowLeft as MynaArrowLeft,
	ArrowLeftRight as MynaArrowLeftRight,
	ArrowRight as MynaArrowRight,
	ArrowUp as MynaArrowUp,
	ArrowUpCircle as MynaArrowUpCircle,
	ArrowUpRight as MynaArrowUpRight,
	ArrowUpRightSquare as MynaArrowUpRightSquare,
	Ban as MynaBan,
	Bell as MynaBell,
	BellOn as MynaBellOn,
	BookOpen as MynaBookOpen,
	Box as MynaBox,
	Bug as MynaBug,
	Camera as MynaCamera,
	CheckCircle as MynaCheckCircle,
	CheckWaves as MynaCheckWaves,
	ChevronDown as MynaChevronDown,
	ChevronLeft as MynaChevronLeft,
	ChevronRight as MynaChevronRight,
	ChevronUp as MynaChevronUp,
	ChevronsUpDown as MynaChevronsUpDown,
	Circle as MynaCircle,
	CircleDashed as MynaCircleDashed,
	Clock3 as MynaClock3,
	ClockCircle as MynaClockCircle,
	Cloudy as MynaCloudy,
	Code as MynaCode,
	Coffee as MynaCoffee,
	Cog as MynaCog,
	Columns as MynaColumns,
	Cookie as MynaCookie,
	CornerDownLeft as MynaCornerDownLeft,
	CornerDownRight as MynaCornerDownRight,
	CornerUpRight as MynaCornerUpRight,
	CreditCard as MynaCreditCard,
	DangerCircle as MynaDangerCircle,
	DangerTriangle as MynaDangerTriangle,
	Download as MynaDownload,
	ExternalLink as MynaExternalLink,
	Eye as MynaEye,
	EyeOff as MynaEyeOff,
	File as MynaFile,
	FilePlus as MynaFilePlus,
	FileText as MynaFileText,
	FlaskConical as MynaFlaskConical,
	Folder as MynaFolder,
	FolderPlus as MynaFolderPlus,
	GitBranch as MynaGitBranch,
	GitCommit as MynaGitCommit,
	GitDiff as MynaGitDiff,
	GitMerge as MynaGitMerge,
	GitPullRequest as MynaGitPullRequest,
	Globe as MynaGlobe,
	Grid as MynaGrid,
	Hand as MynaHand,
	Heading1 as MynaHeading1,
	Heading2 as MynaHeading2,
	Heading3 as MynaHeading3,
	Heading4 as MynaHeading4,
	Heading5 as MynaHeading5,
	Home as MynaHome,
	Image as MynaImage,
	InfoCircle as MynaInfoCircle,
	Key as MynaKey,
	Keyboard as MynaKeyboard,
	LayoutDashboard as MynaLayoutDashboard,
	Link as MynaLink,
	List as MynaList,
	ListCheck as MynaListCheck,
	ListNumber as MynaListNumber,
	Lock as MynaLock,
	Login as MynaLogin,
	Logout as MynaLogout,
	Maximize as MynaMaximize,
	Message as MynaMessage,
	MessageDots as MynaMessageDots,
	MessagePlus as MynaMessagePlus,
	Microphone as MynaMicrophone,
	Minimize as MynaMinimize,
	Minus as MynaMinus,
	MinusCircle as MynaMinusCircle,
	Mobile as MynaMobile,
	Monitor as MynaMonitor,
	MousePointer as MynaMousePointer,
	MousePointerClick as MynaMousePointerClick,
	PanelLeft as MynaPanelLeft,
	PanelRight as MynaPanelRight,
	Paperclip as MynaPaperclip,
	Pencil as MynaPencil,
	Pin as MynaPin,
	Plus as MynaPlus,
	Presentation as MynaPresentation,
	QuestionCircle as MynaQuestionCircle,
	Refresh as MynaRefresh,
	Rows as MynaRows,
	Save as MynaSave,
	Search as MynaSearch,
	SearchCheck as MynaSearchCheck,
	SearchX as MynaSearchX,
	Send as MynaSend,
	Servers as MynaServers,
	Share as MynaShare,
	ShieldCheck as MynaShieldCheck,
	ShieldX as MynaShieldX,
	Shuffle as MynaShuffle,
	Sparkles as MynaSparkles,
	Spinner as MynaSpinner,
	Square as MynaSquare,
	Star as MynaStar,
	StopCircle as MynaStopCircle,
	Sun as MynaSun,
	Table as MynaTable,
	Tablet as MynaTablet,
	Tag as MynaTag,
	Terminal as MynaTerminal,
	Trash as MynaTrash,
	TypeBold as MynaTypeBold,
	TypeItalic as MynaTypeItalic,
	Upload as MynaUpload,
	User as MynaUser,
	Users as MynaUsers,
	Building as MynaBuilding,
	Mail as MynaMail,
	SearchMinus as MynaSearchMinus,
	SearchPlus as MynaSearchPlus,
	UserPlus as MynaUserPlus,
	Video as MynaVideo,
	Wrench as MynaWrench,
	X as MynaX,
	XCircle as MynaXCircle,
	Zap as MynaZap,
	Chat as MynaChat,
	Package as MynaPackage,
	Filter as MynaFilter,
	SelectMultiple as MynaSelectMultiple,
	Music as MynaMusic,
	Database as MynaDatabase,
	ChartBar as MynaChartBar,
} from "@mynaui/icons-react";
import {
	ArrowDownUp as LucideArrowDownUp,
	Bot as LucideBot,
	Brain as LucideBrain,
	CalendarClock as LucideCalendarClock,
	CaseSensitive as LucideCaseSensitive,
	CheckCheck as LucideCheckCheck,
	ChevronsDownUp as LucideChevronsDownUp,
	ClipboardCheck as LucideClipboardCheck,
	CircleDot as LucideCircleDot,
	Eraser as LucideEraser,
	FileCode2 as LucideFileCode2,
	FileDiff as LucideFileDiff,
	FileJson2 as LucideFileJson2,
	FilePen as LucideFilePen,
	FileSearch as LucideFileSearch,
	FolderGit2 as LucideFolderGit2,
	FolderOpen as LucideFolderOpen,
	FolderTree as LucideFolderTree,
	Folders as LucideFolders,
	Gauge as LucideGauge,
	GitFork as LucideGitFork,
	GitPullRequestClosed as LucideGitPullRequestClosed,
	GitPullRequestDraft as LucideGitPullRequestDraft,
	Hammer as LucideHammer,
	History as LucideHistory,
	Hourglass as LucideHourglass,
	Import as LucideImport,
	Laptop as LucideLaptop,
	ListChevronsDownUp as LucideListChevronsDownUp,
	ListChevronsUpDown as LucideListChevronsUpDown,
	MessageSquareDiff as LucideMessageSquareDiff,
	MessageSquareQuote as LucideMessageSquareQuote,
	MonitorCog as LucideMonitorCog,
	MonitorSmartphone as LucideMonitorSmartphone,
	NotebookPen as LucideNotebookPen,
	Paintbrush as LucidePaintbrush,
	Pilcrow as LucidePilcrow,
	PinOff as LucidePinOff,
	Plug as LucidePlug,
	QrCode as LucideQrCode,
	Quote as LucideQuote,
	Regex as LucideRegex,
	RotateCcw as LucideRotateCcw,
	RotateCw as LucideRotateCw,
	Route as LucideRoute,
	ScanSearch as LucideScanSearch,
	ScrollText as LucideScrollText,
	Sheet as LucideSheet,
	ShieldAlert as LucideShieldAlert,
	ShieldQuestion as LucideShieldQuestion,
	Settings2 as LucideSettings2,
	SlidersHorizontal as LucideSlidersHorizontal,
	Split as LucideSplit,
	StickyNote as LucideStickyNote,
	Strikethrough as LucideStrikethrough,
	WifiOff as LucideWifiOff,
	Workflow as LucideWorkflow,
	WrapText as LucideWrapText,
	Ungroup as LucideUngroup,
	Check as LucideCheck,
	Ellipsis as LucideEllipsis,
	EllipsisVertical as LucideEllipsisVertical,
	GripVertical as LucideGripVertical,
	Pause as LucidePause,
	Play as LucidePlay,
	Copy as LucideCopy,
	Undo2 as LucideUndo2,
	type LucideIcon as LucideComponent,
} from "lucide-react";
import { cn } from "../lib/utils";

export type AppIconProps = Omit<SVGProps<SVGSVGElement>, "ref"> & {
	size?: number | string;
	strokeWidth?: number | string;
	/** Lucide-only; accepted for compatibility and ignored by Myna icons. */
	absoluteStrokeWidth?: boolean;
};
export type AppIcon = ForwardRefExoticComponent<AppIconProps & RefAttributes<SVGSVGElement>>;

const STROKE = 1.5;

// Myna draws inside an 18-unit frame (3-21) while lucide uses 20 (2-22), so
// at the same CSS size Myna glyphs read about 10% smaller than lucide ones.
// Crop the viewBox to Myna's frame so both libraries render at one optical
// size, and thin the stroke by the same ratio so line weight stays at 1.5px
// per 24px. Glyphs that already fill the box (wide clouds, messages) opt out.
const MYNA_CROP = 21.6 / 24;
const MYNA_VIEWBOX = "1.2 1.2 21.6 21.6";

type MynaComponent = (props: Record<string, unknown>) => unknown;

function fromMyna(Icon: MynaComponent, name: string, frame: "crop" | "full" = "crop"): AppIcon {
	const Component = Icon as unknown as ForwardRefExoticComponent<Record<string, unknown> & RefAttributes<SVGSVGElement>>;
	const scale = frame === "crop" ? MYNA_CROP : 1;
	const Wrapped = forwardRef<SVGSVGElement, AppIconProps>(({ absoluteStrokeWidth: _ignored, className, strokeWidth, ...rest }, ref) => (
		<Component
			aria-hidden={rest["aria-label"] ? undefined : true}
			{...(frame === "crop" ? { viewBox: MYNA_VIEWBOX } : {})}
			{...rest}
			className={cn("icon", `icon-${name}`, className)}
			ref={ref}
			strokeWidth={Number(strokeWidth ?? STROKE) * scale}
		/>
	));
	Wrapped.displayName = `Icon(${name})`;
	return Wrapped;
}

function fromLucide(Icon: LucideComponent, name: string): AppIcon {
	const Wrapped = forwardRef<SVGSVGElement, AppIconProps>(({ className, strokeWidth, ...rest }, ref) => (
		<Icon {...rest} className={cn("icon", `icon-${name}`, className)} ref={ref} strokeWidth={strokeWidth ?? STROKE} />
	));
	Wrapped.displayName = `Icon(${name})`;
	return Wrapped;
}

export const Activity = fromMyna(MynaActivity, "activity");
export const AlertCircle = fromMyna(MynaDangerCircle, "circle-alert");
export const AlertTriangle = fromMyna(MynaDangerTriangle, "triangle-alert");
export const Archive = fromMyna(MynaArchive, "archive");
export const ArrowDown = fromMyna(MynaArrowDown, "arrow-down", "full");
export const ArrowLeft = fromMyna(MynaArrowLeft, "arrow-left", "full");
export const ArrowLeftRight = fromMyna(MynaArrowLeftRight, "arrow-left-right");
export const ArrowRight = fromMyna(MynaArrowRight, "arrow-right", "full");
export const ArrowRightLeft = fromMyna(MynaArrowLeftRight, "arrow-right-left");
export const ArrowUp = fromMyna(MynaArrowUp, "arrow-up", "full");
export const ArrowUpFromLine = fromMyna(MynaUpload, "arrow-up-from-line");
export const ArrowUpRight = fromMyna(MynaArrowUpRight, "arrow-up-right", "full");
export const BadgeCheck = fromMyna(MynaCheckWaves, "badge-check", "full");
export const Ban = fromMyna(MynaBan, "ban");
export const Bell = fromMyna(MynaBell, "bell");
export const BellRing = fromMyna(MynaBellOn, "bell-ring");
export const Bold = fromMyna(MynaTypeBold, "bold", "full");
export const BookOpen = fromMyna(MynaBookOpen, "book-open");
export const Bot = fromLucide(LucideBot, "bot");
export const Box = fromMyna(MynaBox, "box");
export const Brain = fromLucide(LucideBrain, "brain");
export const Bug = fromMyna(MynaBug, "bug");
export const CalendarClock = fromLucide(LucideCalendarClock, "calendar-clock");
export const Camera = fromMyna(MynaCamera, "camera");
export const CaseSensitive = fromLucide(LucideCaseSensitive, "case-sensitive");
export const Check = fromLucide(LucideCheck, "check");
export const ChartBar = fromMyna(MynaChartBar, "chart-bar");
export const CheckCheck = fromLucide(LucideCheckCheck, "check-check");
export const CheckCircle2 = fromMyna(MynaCheckCircle, "circle-check");
export const ChevronDown = fromMyna(MynaChevronDown, "chevron-down", "full");
export const ChevronDownIcon = fromMyna(MynaChevronDown, "chevron-down", "full");
export const ChevronLeft = fromMyna(MynaChevronLeft, "chevron-left", "full");
export const ChevronRight = fromMyna(MynaChevronRight, "chevron-right", "full");
export const ChevronRightIcon = fromMyna(MynaChevronRight, "chevron-right", "full");
export const ChevronUp = fromMyna(MynaChevronUp, "chevron-up", "full");
export const ChevronUpIcon = fromMyna(MynaChevronUp, "chevron-up", "full");
export const ChevronsDownUp = fromLucide(LucideChevronsDownUp, "chevrons-down-up");
export const ChevronsUpDown = fromMyna(MynaChevronsUpDown, "chevrons-up-down", "full");
export const Circle = fromMyna(MynaCircle, "circle");
export const CircleAlert = fromMyna(MynaDangerCircle, "circle-alert");
export const CircleArrowUp = fromMyna(MynaArrowUpCircle, "circle-arrow-up");
export const CircleCheck = fromMyna(MynaCheckCircle, "circle-check");
export const CircleDashed = fromMyna(MynaCircleDashed, "circle-dashed");
export const CircleHelp = fromMyna(MynaQuestionCircle, "circle-help");
export const CircleMinus = fromMyna(MynaMinusCircle, "circle-minus");
export const CircleStop = fromMyna(MynaStopCircle, "circle-stop");
export const CircleX = fromMyna(MynaXCircle, "circle-x");
export const ClipboardCheck = fromLucide(LucideClipboardCheck, "clipboard-check");
export const Clock = fromMyna(MynaClockCircle, "clock");
export const Clock3 = fromMyna(MynaClock3, "clock-3");
// Myna's plain Cloud is an open arc that reads as clipped; Cloudy is closed.
export const Cloud = fromMyna(MynaCloudy, "cloud", "full");
export const Code2 = fromMyna(MynaCode, "code-xml");
export const Coffee = fromMyna(MynaCoffee, "coffee");
export const Columns2 = fromMyna(MynaColumns, "columns-2");
export const Cookie = fromMyna(MynaCookie, "cookie");
export const CircleDot = fromLucide(LucideCircleDot, "circle-dot");
export const Copy = fromLucide(LucideCopy, "copy");
export const Database = fromMyna(MynaDatabase, "database");
export const CornerDownLeft = fromMyna(MynaCornerDownLeft, "corner-down-left");
export const CornerDownRight = fromMyna(MynaCornerDownRight, "corner-down-right");
export const CornerUpRight = fromMyna(MynaCornerUpRight, "corner-up-right");
export const CreditCard = fromMyna(MynaCreditCard, "credit-card");
export const Download = fromMyna(MynaDownload, "download");
export const Ellipsis = fromLucide(LucideEllipsis, "ellipsis");
export const Eraser = fromLucide(LucideEraser, "eraser");
export const ExternalLink = fromMyna(MynaExternalLink, "external-link");
export const Eye = fromMyna(MynaEye, "eye", "full");
export const EyeOff = fromMyna(MynaEyeOff, "eye-off", "full");
export const File = fromMyna(MynaFile, "file");
export const FileCode2 = fromLucide(LucideFileCode2, "file-code-2");
export const FileDiff = fromLucide(LucideFileDiff, "file-diff");
export const FileIcon = fromMyna(MynaFile, "file");
export const FileJson2 = fromLucide(LucideFileJson2, "file-json-2");
export const FilePen = fromLucide(LucideFilePen, "file-pen");
export const FilePlus = fromMyna(MynaFilePlus, "file-plus");
export const FileSearch = fromLucide(LucideFileSearch, "file-search");
export const FileText = fromMyna(MynaFileText, "file-text");
export const Filter = fromMyna(MynaFilter, "filter");
export const FlaskConical = fromMyna(MynaFlaskConical, "flask-conical");
export const Folder = fromMyna(MynaFolder, "folder");
export const FolderClosed = fromMyna(MynaFolder, "folder-closed");
export const FolderGit2 = fromLucide(LucideFolderGit2, "folder-git-2");
export const FolderOpen = fromLucide(LucideFolderOpen, "folder-open");
export const FolderPlus = fromMyna(MynaFolderPlus, "folder-plus");
export const FolderTree = fromLucide(LucideFolderTree, "folder-tree");
export const Folders = fromLucide(LucideFolders, "folders");
export const Gauge = fromLucide(LucideGauge, "gauge");
export const GitBranch = fromMyna(MynaGitBranch, "git-branch");
export const GitCommitHorizontal = fromMyna(MynaGitCommit, "git-commit-horizontal");
export const GitCompareArrows = fromMyna(MynaGitDiff, "git-compare-arrows");
export const GitFork = fromLucide(LucideGitFork, "git-fork");
export const GitMerge = fromMyna(MynaGitMerge, "git-merge");
export const GitPullRequest = fromMyna(MynaGitPullRequest, "git-pull-request");
export const GitPullRequestArrow = fromMyna(MynaGitPullRequest, "git-pull-request-arrow");
export const GitPullRequestClosed = fromLucide(LucideGitPullRequestClosed, "git-pull-request-closed");
export const GitPullRequestDraft = fromLucide(LucideGitPullRequestDraft, "git-pull-request-draft");
export const Globe = fromMyna(MynaGlobe, "globe");
export const GripVertical = fromLucide(LucideGripVertical, "grip-vertical");
export const GripVerticalIcon = fromLucide(LucideGripVertical, "grip-vertical");
export const Hand = fromMyna(MynaHand, "hand");
export const Heading1 = fromMyna(MynaHeading1, "heading-1", "full");
export const Heading2 = fromMyna(MynaHeading2, "heading-2");
export const Heading3 = fromMyna(MynaHeading3, "heading-3");
export const Heading4 = fromMyna(MynaHeading4, "heading-4");
export const Heading5 = fromMyna(MynaHeading5, "heading-5");
export const Hammer = fromLucide(LucideHammer, "hammer");
export const History = fromLucide(LucideHistory, "history");
export const Hourglass = fromLucide(LucideHourglass, "hourglass");
export const House = fromMyna(MynaHome, "house");
export const Image = fromMyna(MynaImage, "image");
export const Import = fromLucide(LucideImport, "import");
export const Info = fromMyna(MynaInfoCircle, "info");
export const Italic = fromMyna(MynaTypeItalic, "italic", "full");
export const KeyRound = fromMyna(MynaKey, "key-round");
export const Keyboard = fromMyna(MynaKeyboard, "keyboard", "full");
export const Laptop = fromLucide(LucideLaptop, "laptop");
export const LayoutDashboard = fromMyna(MynaLayoutDashboard, "layout-dashboard");
export const LayoutGrid = fromMyna(MynaGrid, "layout-grid");
export const Link = fromMyna(MynaLink, "link");
export const Link2 = fromMyna(MynaLink, "link-2");
export const List = fromMyna(MynaList, "list");
export const ListChecks = fromMyna(MynaListCheck, "list-checks");
export const ListChevronsDownUp = fromLucide(LucideListChevronsDownUp, "list-chevrons-down-up");
export const ListChevronsUpDown = fromLucide(LucideListChevronsUpDown, "list-chevrons-up-down");
export const ArrowDownUp = fromLucide(LucideArrowDownUp, "arrow-down-up");
export const ListOrdered = fromMyna(MynaListNumber, "list-ordered");
export const ListTodo = fromMyna(MynaListCheck, "list-todo");
export const Loader2 = fromMyna(MynaSpinner, "loader-circle");
export const LoaderCircle = fromMyna(MynaSpinner, "loader-circle");
export const Lock = fromMyna(MynaLock, "lock");
export const LogIn = fromMyna(MynaLogin, "log-in");
export const LogOut = fromMyna(MynaLogout, "log-out");
export const Maximize2 = fromMyna(MynaMaximize, "maximize-2");
export const MessageSquare = fromMyna(MynaMessage, "message-square", "full");
export const MessageSquareDiff = fromLucide(LucideMessageSquareDiff, "message-square-diff");
export const MessageSquareDot = fromMyna(MynaMessageDots, "message-square-dot", "full");
export const MessageSquarePlus = fromMyna(MynaMessagePlus, "message-square-plus", "full");
export const MessageSquareQuote = fromLucide(LucideMessageSquareQuote, "message-square-quote");
export const Mic = fromMyna(MynaMicrophone, "mic");
export const Minimize2 = fromMyna(MynaMinimize, "minimize-2");
export const Minus = fromMyna(MynaMinus, "minus");
export const Monitor = fromMyna(MynaMonitor, "monitor");
export const MonitorCog = fromLucide(LucideMonitorCog, "monitor-cog");
export const MonitorSmartphone = fromLucide(LucideMonitorSmartphone, "monitor-smartphone");
export const MoreHorizontal = fromLucide(LucideEllipsis, "ellipsis");
export const MoreVertical = fromLucide(LucideEllipsisVertical, "ellipsis-vertical");
export const Music = fromMyna(MynaMusic, "music");
export const MousePointer2 = fromMyna(MynaMousePointer, "mouse-pointer-2", "full");
export const MousePointerClick = fromMyna(MynaMousePointerClick, "mouse-pointer-click");
export const NotebookPen = fromLucide(LucideNotebookPen, "notebook-pen");
export const PanelLeft = fromMyna(MynaPanelLeft, "panel-left");
export const PanelLeftIcon = fromMyna(MynaPanelLeft, "panel-left");
export const PanelRight = fromMyna(MynaPanelRight, "panel-right");
export const Paperclip = fromMyna(MynaPaperclip, "paperclip");
export const Paintbrush = fromLucide(LucidePaintbrush, "paintbrush");
export const Pause = fromLucide(LucidePause, "pause");
export const Pencil = fromMyna(MynaPencil, "pencil");
export const PencilLine = fromMyna(MynaPencil, "pencil-line");
export const Pilcrow = fromLucide(LucidePilcrow, "pilcrow");
export const Pin = fromMyna(MynaPin, "pin");
export const PinOff = fromLucide(LucidePinOff, "pin-off");
export const Play = fromLucide(LucidePlay, "play");
export const Plug = fromLucide(LucidePlug, "plug");
export const WifiOff = fromLucide(LucideWifiOff, "wifi-off");
export const Plus = fromMyna(MynaPlus, "plus");
export const Presentation = fromMyna(MynaPresentation, "presentation");
export const QrCode = fromLucide(LucideQrCode, "qr-code");
export const Quote = fromLucide(LucideQuote, "quote");
export const RefreshCw = fromMyna(MynaRefresh, "refresh-cw");
export const Regex = fromLucide(LucideRegex, "regex");
export const RotateCcw = fromLucide(LucideRotateCcw, "rotate-ccw");
export const RotateCw = fromLucide(LucideRotateCw, "rotate-cw");
export const Route = fromLucide(LucideRoute, "route");
export const Rows3 = fromMyna(MynaRows, "rows-3");
export const Save = fromMyna(MynaSave, "save");
export const ScanSearch = fromLucide(LucideScanSearch, "scan-search");
export const ScrollText = fromLucide(LucideScrollText, "scroll-text");
export const Search = fromMyna(MynaSearch, "search");
export const SelectMultiple = fromMyna(MynaSelectMultiple, "select-multiple");
export const SearchCheck = fromMyna(MynaSearchCheck, "search-check");
export const SearchX = fromMyna(MynaSearchX, "search-x");
export const Send = fromMyna(MynaSend, "send");
export const Server = fromMyna(MynaServers, "server");
export const Settings = fromMyna(MynaCog, "settings");
export const Settings2 = fromLucide(LucideSettings2, "settings-2");
export const Share = fromMyna(MynaShare, "share");
export const Share2 = fromMyna(MynaShare, "share-2");
export const Sheet = fromLucide(LucideSheet, "sheet");
export const ShieldAlert = fromLucide(LucideShieldAlert, "shield-alert");
export const ShieldCheck = fromMyna(MynaShieldCheck, "shield-check");
export const ShieldQuestion = fromLucide(LucideShieldQuestion, "shield-question");
export const ShieldX = fromMyna(MynaShieldX, "shield-x");
export const Shuffle = fromMyna(MynaShuffle, "shuffle");
export const SlidersHorizontal = fromLucide(LucideSlidersHorizontal, "sliders-horizontal");
export const Smartphone = fromMyna(MynaMobile, "smartphone");
export const Split = fromLucide(LucideSplit, "split");
export const Square = fromMyna(MynaSquare, "square");
export const SquareArrowOutUpRight = fromMyna(MynaArrowUpRightSquare, "square-arrow-out-up-right");
export const SquareTerminal = fromMyna(MynaTerminal, "square-terminal");
export const Star = fromMyna(MynaStar, "star");
export const StickyNote = fromLucide(LucideStickyNote, "sticky-note");
export const Strikethrough = fromLucide(LucideStrikethrough, "strikethrough");
export const Sun = fromMyna(MynaSun, "sun");
export const Table2 = fromMyna(MynaTable, "table-2");
export const Tablet = fromMyna(MynaTablet, "tablet");
export const Tag = fromMyna(MynaTag, "tag", "full");
export const TerminalSquare = fromMyna(MynaTerminal, "square-terminal");
export const Trash2 = fromMyna(MynaTrash, "trash-2");
export const TriangleAlert = fromMyna(MynaDangerTriangle, "triangle-alert");
export const Undo2 = fromLucide(LucideUndo2, "undo-2");
export const User = fromMyna(MynaUser, "user");
export const UserRound = fromMyna(MynaUser, "user-round");
export const Users = fromMyna(MynaUsers, "users");
export const Video = fromMyna(MynaVideo, "video");
export const WandSparkles = fromMyna(MynaSparkles, "wand-sparkles");
export const Workflow = fromLucide(LucideWorkflow, "workflow");
export const WrapText = fromLucide(LucideWrapText, "wrap-text");
export const Wrench = fromMyna(MynaWrench, "wrench");
export const X = fromMyna(MynaX, "x", "full");
export const XCircle = fromMyna(MynaXCircle, "xcircle");
export const XIcon = fromMyna(MynaX, "x", "full");
export const Zap = fromMyna(MynaZap, "zap");

// Added by hand (no lucide equivalent was in use): inspector tab icons.
export const Chat = fromMyna(MynaChat, "chat");
export const CheckWaves = fromMyna(MynaCheckWaves, "check-waves", "full");
export const Package = fromMyna(MynaPackage, "package");

// Added for screens that landed on main after the port.
export const Building2 = fromMyna(MynaBuilding, "building-2", "full");
export const Mail = fromMyna(MynaMail, "mail", "full");
export const Rows2 = fromMyna(MynaRows, "rows-2");
export const Terminal = fromMyna(MynaTerminal, "terminal", "full");
export const Ungroup = fromLucide(LucideUngroup, "ungroup");
export const UserPlus = fromMyna(MynaUserPlus, "user-plus");
export const ZoomIn = fromMyna(MynaSearchPlus, "zoom-in");
export const ZoomOut = fromMyna(MynaSearchMinus, "zoom-out");
