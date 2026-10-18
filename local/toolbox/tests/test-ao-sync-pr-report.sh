#!/bin/bash
# TDD-тесты шага 9 ao-sync.sh (mergeable-отчёт по PR).
# Баг: под «!! конфликтуют» список рендерится пустым при реальных конфликтах,
# а ошибка gh api молча засчитывается как «конфликтов нет».
set -u
SYNC="${SYNC:-/Users/axisrow/bin/ao-sync.sh}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Извлекаем шаг 9 (блок if command -v gh ... fi — конец файла) как отдельный скрипт.
awk '/^if command -v gh >\/dev\/null 2>&1; then/,0' "$SYNC" > "$TMP/block.sh"

# Стаб gh: два PR, #3930 dirty, #4000 clean.
cat > "$TMP/gh" <<'STUB'
#!/bin/bash
if [ "$1" = "pr" ]; then
  echo '[{"number":3930,"title":"feat(config): user-scope layer","url":"https://example.test/pull/3930","mergeable":"CONFLICTING"},{"number":4000,"title":"clean pr","url":"https://example.test/pull/4000","mergeable":"MERGEABLE"}]'
  exit 0
fi
if [ "$1" = "api" ]; then
  if [ -n "${FAKE_GH_API_FAIL:-}" ]; then exit 1; fi
  case "$2" in
    *pulls/3930*) echo "dirty" ;;
    *) echo "clean" ;;
  esac
  exit 0
fi
exit 1
STUB
chmod +x "$TMP/gh"

fail=0

# T1: конфликтующий PR реально виден под «!! конфликтуют» (и clean-PR не в списке).
out="$(PATH="$TMP:$PATH" bash "$TMP/block.sh")"
if printf '%s' "$out" | grep -q '!! конфликтуют' \
   && printf '%s' "$out" | grep -q '#3930  feat(config): user-scope layer' \
   && ! printf '%s' "$out" | grep -q '#4000'; then
  echo "green T1: dirty-PR отрендерен, clean-PR не в списке"
else
  echo "RED T1: конфликтующий PR не отрендерился (или мусор в списке):"
  printf '%s\n' "$out"
  fail=1
fi

# T2: ошибка gh api НЕ должна выглядеть как «OK, конфликтующих нет».
out2="$(PATH="$TMP:$PATH" FAKE_GH_API_FAIL=1 bash "$TMP/block.sh")"
if printf '%s' "$out2" | grep -q 'OK, конфликтующих нет'; then
  echo "RED T2: ошибка gh api проглочена и показана как «конфликтов нет»:"
  printf '%s\n' "$out2"
  fail=1
elif printf '%s' "$out2" | grep -q 'не удалось проверить'; then
  echo "green T2: сбой gh api отчитан отдельным списком"
else
  echo "RED T2: при сбое gh api нет ни OK, ни предупреждения:"
  printf '%s\n' "$out2"
  fail=1
fi

if [ "$fail" = "0" ]; then
  echo "ALL GREEN"
else
  echo "FAILED"
fi
exit "$fail"
