#!/usr/bin/env bash
# Session search benchmark: ranking quality on public conversational-retrieval
# datasets (LongMemEval-S, LoCoMo) plus AO's own agentic suite, compared against
# the committed baseline in
# backend/internal/service/sessionsearch/testdata/search_benchmark_baseline.json.
#
#   scripts/search-benchmark.sh              quick: agentic + LoCoMo + 100 LongMemEval questions (~3 min)
#   scripts/search-benchmark.sh full         every LongMemEval question (~8 min more)
#   scripts/search-benchmark.sh ablate       quick, with every ranking variant side by side (~15 min)
#   scripts/search-benchmark.sh full ablate  both
#   scripts/search-benchmark.sh --update     record this run as the new baseline
#
# Datasets are downloaded once into the cache below and verified by SHA-256, so a
# silent upstream change cannot move the numbers. Nothing is written to ~/.ao.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
backend_dir="${repo_root}/backend"
data_dir="${AO_SEARCH_BENCH_DATA:-${XDG_CACHE_HOME:-${HOME}/.cache}/aoagents/agent-orchestrator/search-benchmark}"

longmemeval_url="https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned/resolve/main/longmemeval_s_cleaned.json"
longmemeval_sha="d6f21ea9d60a0d56f34a05b609c79c88a451d2ae03597821ea3d5a9678c3a442"
locomo_url="https://raw.githubusercontent.com/snap-research/locomo/main/data/locomo10.json"
locomo_sha="79fa87e90f04081343b8c8debecb80a9a6842b76a7aa537dc9fdf651ea698ff4"

profile="quick"
variants=""
update=""
for arg in "$@"; do
  case "${arg}" in
    quick | full) profile="${arg}" ;;
    ablate) variants="all" ;;
    --update) update="1" ;;
    -h | --help)
      sed -n '2,14p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "unknown argument: ${arg}" >&2
      exit 2
      ;;
  esac
done

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

fetch() {
  local url="$1" sha="$2" target="$3"
  if [[ -f "${target}" && "$(sha256_of "${target}")" == "${sha}" ]]; then
    return
  fi
  mkdir -p "$(dirname "${target}")"
  echo "downloading $(basename "${target}") ..." >&2
  curl -fsSL --retry 3 -o "${target}.partial" "${url}"
  local got
  got="$(sha256_of "${target}.partial")"
  if [[ "${got}" != "${sha}" ]]; then
    rm -f "${target}.partial"
    echo "checksum mismatch for ${url}: got ${got}, want ${sha}" >&2
    exit 1
  fi
  mv "${target}.partial" "${target}"
}

fetch "${longmemeval_url}" "${longmemeval_sha}" "${data_dir}/longmemeval_s_cleaned.json"
fetch "${locomo_url}" "${locomo_sha}" "${data_dir}/locomo10.json"

cd "${backend_dir}"
AO_SEARCH_BENCH_LONGMEMEVAL="${data_dir}/longmemeval_s_cleaned.json" \
AO_SEARCH_BENCH_LOCOMO="${data_dir}/locomo10.json" \
AO_SEARCH_BENCH_PROFILE="${profile}" \
AO_SEARCH_BENCH_VARIANTS="${variants}" \
AO_SEARCH_BENCH_UPDATE="${update}" \
  go test ./internal/service/sessionsearch/ -run '^TestSearchBenchmark(Agentic|LoCoMo|LongMemEval)$' -count=1 -v -timeout 120m 2>&1 |
  grep --line-buffered -Ev '^(=== RUN|=== PAUSE|=== CONT)' |
  awk '{ sub(/^ +search_benchmark_test\.go:[0-9]+: /, ""); print; fflush() }'
