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

echo "ao-sync.test.sh: OK"
