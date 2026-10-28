#!/usr/bin/env bash
set -euo pipefail

fixture_parent="${AO_ONBOARDING_FIXTURE_PARENT:-$HOME/dev/ao-onboarding-demo/fixtures}"
mkdir -p "$fixture_parent"
fixture_root="$(mktemp -d "$fixture_parent/run.XXXXXX")"
fixture_git="$(command -v git)"
mkdir -p "$fixture_root/source" "$fixture_root/available" "$fixture_root/conflict/sample" "$fixture_root/reuse" "$fixture_root/bin"
git init -q -b main "$fixture_root/source"
echo 'Onboarding test repository' > "$fixture_root/source/README.md"
git -C "$fixture_root/source" add README.md
git -C "$fixture_root/source" -c user.name='AO test' -c user.email='ao-test@example.invalid' commit -qm 'Initial test commit'
git clone -q --bare "$fixture_root/source" "$fixture_root/sample.git"
git clone -q "file://$fixture_root/sample.git" "$fixture_root/reuse/sample"
echo 'Leave this file alone' > "$fixture_root/conflict/sample/keep.txt"

# A test-only Git wrapper. Normal commands go to the real binary; only clone
# receives the selected fault. Each fixture directory owns its retry counter.
cat > "$fixture_root/bin/git" <<'WRAPPER'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == clone ]]; then
  case "${AO_ONBOARDING_FAULT:-none}" in
    slow) sleep 75 ;;
    retry)
      count_file="$AO_ONBOARDING_FIXTURES/retry-attempts"
      attempt=0
      [[ ! -f "$count_file" ]] || read -r attempt < "$count_file"
      attempt=$((attempt + 1))
      echo "$attempt" > "$count_file"
      if (( attempt < 3 )); then
        echo 'fatal: connection reset' >&2
        exit 128
      fi
      ;;
    offline) echo 'fatal: Could not resolve host: example.invalid' >&2; exit 128 ;;
  esac
fi
exec "$AO_ONBOARDING_REAL_GIT" "$@"
WRAPPER
chmod +x "$fixture_root/bin/git"

printf 'Fixture directory: %s\nRepository URL: file://%s/sample.git\nAvailable destination: %s/available\nConflicting destination: %s/conflict\nExisting checkout destination: %s/reuse\nReal Git binary: %s\n' \
  "$fixture_root" "$fixture_root" "$fixture_root" "$fixture_root" "$fixture_root" "$fixture_git"
