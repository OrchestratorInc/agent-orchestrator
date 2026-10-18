#!/usr/bin/env bash
# ao-pregate.sh — гейты, которые надо прогнать ПЕРЕД push в upstream.
#
# Зачем отдельный скрипт: ao-sync.sh гоняет быстрые гейты после синка
# (gofmt + go build), но этого мало. Два реальных красных CI за 2026-08-13
# были ровно на тех проверках, которых не было в локальном прогоне:
#   * golangci-lint (predeclared: `var comparable int` в тесте)  -> PR #3900
#   * typecheck:e2e (не обновлён e2e/support/fake-bridge.ts)     -> PR #3898
# Оба ловятся за секунды локально и стоят красного CI на сутки, если не поймать.
#
# Скрипт запускает только те гейты, которых касается дифф: backend-гейты, если
# менялся Go, frontend-гейты — если TS/TSX. Так он остаётся дешёвым.
#
# Использование:
#   ~/bin/ao-pregate.sh            # сравнить с upstream/main
#   ~/bin/ao-pregate.sh origin/main
set -uo pipefail

REPO="${AO_REPO:-$HOME/Projects/agent-orchestrator}"
BASE="${1:-upstream/main}"

# Версия строго та же, что в .github/workflows/go.yml — иначе гейт врёт:
# другой набор правил ловит/пропускает не то, что CI.
GOLANGCI_VERSION="v2.12.2"

cd "$REPO" || { echo "!! нет каталога $REPO" >&2; exit 1; }

FAILED=()
note_fail() { FAILED+=("$1"); }

if ! git rev-parse --verify --quiet "$BASE" >/dev/null; then
  echo "!! база '$BASE' не найдена. Сначала: git fetch upstream" >&2
  exit 1
fi

CHANGED="$(git diff --name-only "$BASE"...HEAD)"
# Незакоммиченное тоже проверяем: пушить всё равно предстоит его.
CHANGED="$CHANGED
$(git diff --name-only HEAD)"

has_changes() { echo "$CHANGED" | grep -qE "$1"; }

echo "==> база: $BASE ($(git rev-parse --short "$BASE"))"

# ---------------------------------------------------------------- backend ----
if has_changes '^backend/.*\.go$'; then
  echo "==> gofmt -l . (весь backend-модуль)..."
  FMT_OUT="$(cd backend && gofmt -l .)"
  if [ -n "$FMT_OUT" ]; then
    echo "!! неотформатированные файлы:" >&2
    echo "$FMT_OUT" >&2
    echo "   Почини: (cd backend && gofmt -w .)" >&2
    note_fail "gofmt"
  else
    echo "   OK"
  fi

  # go test отдельно от golangci-lint (не через `npm run lint`, которое их
  # связывает && — один известный failing пакет там завалил бы весь шаг).
  # crush/auth_test.go::TestCrushLocalAuthStatusDoesNotUseProviderCatalog
  # падает и на чистом upstream/main (проверено 2026-08-29 в отдельном
  # worktree) — читает locale/config состояние этой машины, а не наша
  # регрессия. Гейт зелёный, пока падает ТОЛЬКО этот пакет.
  echo "==> go test ./... (backend)..."
  GO_TEST_OUT="$(cd backend && go test ./... 2>&1)"
  GO_TEST_FAILED_PKGS="$(printf '%s\n' "$GO_TEST_OUT" | grep '^FAIL\s' | awk '{print $2}' | sort -u)"
  KNOWN_GO_FAILS="github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/crush"
  UNEXPECTED_GO_FAILS="$(comm -23 <(printf '%s\n' "$GO_TEST_FAILED_PKGS" | grep -v '^$') <(printf '%s\n' "$KNOWN_GO_FAILS" | sort))"
  if [ -z "$UNEXPECTED_GO_FAILS" ]; then
    echo "   OK$([ -n "$GO_TEST_FAILED_PKGS" ] && echo " (известное падение: crush, environment-зависимый)")"
  else
    echo "!! новые падения go test (нет на upstream/main):" >&2
    printf '   %s\n' "$UNEXPECTED_GO_FAILS" >&2
    note_fail "go test"
  fi

  # golangci-lint. Не ставим бинарь руками: тот же `go run` вызов, что и в
  # `npm run lint` / CI, с той же версией. Один источник правды — если
  # апстрим поднимет версию, гейт поднимется вместе с ним. Первый прогон
  # медленный (компиляция линтера), дальше go build cache.
  echo "==> golangci-lint $GOLANGCI_VERSION..."
  if ( cd backend && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$GOLANGCI_VERSION run --path-mode=abs ); then
    echo "   OK"
  else
    note_fail "golangci-lint"
  fi

  echo "==> go build ./... (backend)..."
  if ( cd backend && go build ./... ); then echo "   OK"; else note_fail "go build"; fi
fi

# --------------------------------------------------------------- frontend ----
if has_changes '^frontend/.*\.(ts|tsx|js|jsx|json)$'; then
  # Два соседних пакета держат свои package.json и НЕ ставятся при `npm ci`
  # во frontend. Без них vitest падает десятками файлов на ровном месте
  # (product-ui -> "Failed to resolve import clsx"), и легко принять это за
  # свою регрессию. Путь product-ui — в КОРНЕ репо, не под frontend/.
  if [ -d packages/product-ui ] && [ ! -d packages/product-ui/node_modules ]; then
    echo "==> npm ci (packages/product-ui)..."
    ( cd packages/product-ui && npm ci ) || note_fail "npm ci product-ui"
  fi
  if [ -d frontend/src/landing ] && [ ! -d frontend/src/landing/node_modules ]; then
    echo "==> npm ci (src/landing)..."
    ( cd frontend && npm ci --prefix src/landing ) || note_fail "npm ci landing"
  fi

  echo "==> npm run typecheck (frontend)..."
  if ( cd frontend && npm run typecheck --silent ); then echo "   OK"; else note_fail "typecheck"; fi

  # Тот самый гейт, что уронил #3898: e2e-фейки реализуют те же bridge-типы,
  # что и рантайм, но лежат вне основного tsconfig и обычным typecheck не
  # покрываются.
  echo "==> npm run typecheck:e2e (frontend)..."
  if ( cd frontend && npm run typecheck:e2e --silent ); then echo "   OK"; else note_fail "typecheck:e2e"; fi

  # vitest. Эти файлы падают и на чистом upstream/main — проверено в отдельном
  # worktree с полными зависимостями, так что это не наша регрессия:
  #   - AgentModelCombobox.test.tsx (2026-08-13, дефект теста)
  #   - ChatWorkspace.test.tsx / ChatTimelineItems.test.tsx (2026-08-29):
  #     формат даты жёстко ждёт en-US ("Yesterday", "Aug 26, 2026"), а тест
  #     не пиннит locale — читает системную. На non-en-US машине (здесь:
  #     ru-RU) Intl.DateTimeFormat отдаёт локализованную строку и regex не
  #     матчит. Гейт зелёный, пока падают ТОЛЬКО файлы из этого списка. Любой
  #     другой файл — уже наша регрессия, и скрипт её покажет поимённо.
  echo "==> npx vitest run (frontend)..."
  KNOWN_FAILS="src/renderer/components/settings/AgentModelCombobox.test.tsx
src/renderer/components/chat/ChatWorkspace.test.tsx
src/renderer/components/chat/ChatTimelineItems.test.tsx"
  VITEST_JSON="$(mktemp -t ao-pregate-vitest)"
  ( cd frontend && npx vitest run --reporter=json --outputFile="$VITEST_JSON" ) >/dev/null 2>&1
  if [ ! -s "$VITEST_JSON" ]; then
    echo "!! vitest не выдал отчёт — прогони вручную: (cd frontend && npx vitest run)" >&2
    note_fail "vitest (нет отчёта)"
  else
    ACTUAL="$(jq -r '.testResults[] | select(.status=="failed") | .name' "$VITEST_JSON" \
      | sed 's|.*/frontend/||' | sort -u)"
    UNEXPECTED="$(comm -23 <(printf '%s\n' "$ACTUAL" | grep -v '^$') <(printf '%s\n' "$KNOWN_FAILS" | sort))"
    if [ -z "$UNEXPECTED" ]; then
      echo "   OK (известные падения: $(printf '%s' "$ACTUAL" | grep -c . ) шт., все в списке предсуществующих)"
    else
      echo "!! новые падения (нет на upstream/main):" >&2
      printf '   %s\n' $UNEXPECTED >&2
      note_fail "vitest"
    fi
  fi
  rm -f "$VITEST_JSON"
fi

echo "=========================================="
if [ ${#FAILED[@]} -eq 0 ]; then
  echo "==> все гейты зелёные. Можно пушить."
  exit 0
fi
echo "!! провалились: ${FAILED[*]}" >&2
exit 1
