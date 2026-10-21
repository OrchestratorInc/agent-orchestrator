#!/usr/bin/env bash
# Тест гейта frontend-typecheck в ao-sync.sh.
# Ловит класс «merge перестроил файл по апстриму и потерял часть fork-дельты»
# (инцидент 2026-10-10: ProjectAgentRoleHeader is not defined — потерянный
# импорт доехал до собранного .app, потому что tsc в синке не гонялся).
set -euo pipefail

sync="$(cd "$(dirname "$0")" && pwd)/ao-sync.sh"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# 1. Гейт определён И вызывается (одна только мёртвая функция — тоже провал).
[ "$(grep -c 'gate_frontend_typecheck' "$sync")" -ge 2 ] || {
	echo "!! ao-sync.sh не содержит (или не вызывает) gate_frontend_typecheck" >&2
	exit 1
}

# 2. Тело функции действительно падает на дереве с TS-ошибкой.
#    Извлекаем функцию и исполняем её против tmp-репо с фейковым npm.
mkdir -p "$tmp/frontend"
printf '%s\n' '{"scripts":{"typecheck":"./tsc-fake"}}' > "$tmp/frontend/package.json"
printf '%s\n' '#!/bin/sh' 'exit 0' > "$tmp/frontend/tsc-fake"
chmod +x "$tmp/frontend/tsc-fake"
sed -n '/^gate_frontend_typecheck()/,/^}/p' "$sync" > "$tmp/gate.sh"
[ -s "$tmp/gate.sh" ] || { echo "!! функция gate_frontend_typecheck не извлекается из ao-sync.sh" >&2; exit 1; }

# зелёный случай: typecheck проходит — гейт пропускает.
# REPO_ROOT/TS_LOG потребляет извлечённая функция — для shellcheck они «unused».
# shellcheck disable=SC1091,SC2034
( source "$tmp/gate.sh"; REPO_ROOT="$tmp" TS_LOG="$tmp/tsc.log"; gate_frontend_typecheck )

# красный случай: typecheck падает (симуляция потерянного импорта) — гейт обязан упасть.
printf '%s\n' '#!/bin/sh' 'echo "src/x.tsx(207,6): error TS2304: Cannot find name '"'"'ProjectAgentRoleHeader'"'"'."' 'exit 2' > "$tmp/frontend/tsc-fake"
# shellcheck disable=SC1091,SC2034
if ( source "$tmp/gate.sh"; REPO_ROOT="$tmp" TS_LOG="$tmp/tsc.log"; gate_frontend_typecheck ) >/dev/null 2>&1; then
	echo "!! гейт пропустил дерево с TS-ошибкой — потери при синке остаются незамеченными" >&2
	exit 1
fi

# 3. Сообщение вызывающего кода упоминает потерю fork-дельты (иначе инцидент
#    снова придётся диагностировать с нуля).
grep -q 'fork-дельты' "$sync" || {
	echo "!! ao-sync.sh не объясняет при падении гейта, что искать потерю fork-дельты" >&2
	exit 1
}

# 4. Классификатор fixes-only (политика 2026-10-21 «из апстрима только
#    багфиксы»): типизированные fix — take, остальное — skip; леджер на месте.
grep -q 'fixes-only' "$sync" || { echo "!! ao-sync.sh не содержит режим fixes-only" >&2; exit 1; }
grep -q 'ao-upstream-ledger.txt' "$sync" || { echo "!! ao-sync.sh не работает с леджером ao-upstream-ledger.txt" >&2; exit 1; }
sed -n '/^classify_upstream_subject()/,/^}/p' "$sync" > "$tmp/cls.sh"
[ -s "$tmp/cls.sh" ] || { echo "!! classify_upstream_subject не извлекается из ao-sync.sh" >&2; exit 1; }
(
	# shellcheck disable=SC1090
	source "$tmp/cls.sh"
	[ "$(classify_upstream_subject "fix(cloud): wait for the user (#6961)")" = "take" ] || exit 1
	[ "$(classify_upstream_subject "fix: plain polish (#6980)")" = "take" ] || exit 1
	[ "$(classify_upstream_subject "feat(mobile): files screen (#6922)")" = "skip" ] || exit 1
	[ "$(classify_upstream_subject "chore(sync): merge origin/main 2026-10-21")" = "skip" ] || exit 1
	[ "$(classify_upstream_subject "fixup: not a typed fix")" = "skip" ] || exit 1
	[ "$(classify_upstream_subject "perf(api): speed up list")" = "skip" ] || exit 1
) || { echo "!! classify_upstream_subject классифицирует неверно" >&2; exit 1; }

# 5. Инварианты канонизации fork-миграций (класс инцидента 2026-10-21: в
#    CANONICAL_LIST фиксера не хватало записи — settle выдал «канон» и
#    переименовал уже применённую живой БД миграцию, раннер повторил SQL,
#    демон не стартовал). Канон миграции = номер её первой публикации;
#    применённые живыми БД миграции не переименовываются никогда.
root="$(cd "$(dirname "$0")/../.." && pwd)"
reserved="$root/backend/internal/storage/sqlite/migrate_fork_reserved_range_test.go"
migdir="$root/backend/internal/storage/sqlite/migrations"
if [ ! -f "$reserved" ]; then
	echo "!! (5) $reserved не найден — инварианты канонизации гонять только в форке" >&2
	exit 1
fi
fork_suffixes="$(sed -n '/var forkLocalMigrations = \[\]string{/,/^}/p' "$reserved" | grep -oE '"[a-zA-Z_0-9]+"' | tr -d '"' | sort -u || true)"
fixer="$(cd "$(dirname "$0")" && pwd)/ao-fix-migration-collision.sh"
canon_list="$(sed -n '/^CANONICAL_LIST="/,/^"$/p' "$fixer" | sed 's/^CANONICAL_LIST="//; s/"$//' | grep -E '^[a-zA-Z_0-9]+:[0-9]+$' || true)"
[ -n "$fork_suffixes" ] || { echo "!! (5) forkLocalMigrations пуст — вне форка это ожидаемо, здесь нет" >&2; exit 1; }
[ -n "$canon_list" ] || { echo "!! (5) CANONICAL_LIST в ao-fix-migration-collision.sh пуст или не читается" >&2; exit 1; }

inv_fail=""
for s in $fork_suffixes; do
	entry="$(printf '%s\n' "$canon_list" | grep -E "^$s:" | cut -d: -f2 || true)"
	if [ -z "$entry" ]; then
		inv_fail="$inv_fail
  в CANONICAL_LIST нет записи для '$s' (settle авто-присвоит и, возможно, переименует применённое)"
		continue
	fi
	if [ ! -f "$migdir/${entry}_${s}.sql" ]; then
		inv_fail="$inv_fail
  канон '$s' = $entry, но файла ${entry}_${s}.sql нет — settle захочет переименовать применённую миграцию"
	fi
done
for e in $canon_list; do
	s="${e%%:*}"
	printf '%s\n' "$fork_suffixes" | grep -qx "$s" || inv_fail="$inv_fail
  CANONICAL_LIST содержит '$s', которого нет в forkLocalMigrations (протухшая запись)"
done
[ -z "$inv_fail" ] || {
	echo "!! (5) инварианты канонизации миграций нарушены:$inv_fail" >&2
	exit 1
}

echo "ao-sync.test.sh: OK"
