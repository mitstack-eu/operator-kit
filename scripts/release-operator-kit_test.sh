#!/usr/bin/env bash
# release-operator-kit_test.sh runs scripts/release-operator-kit.sh against scratch repositories, each with a bare remote, and asserts the tags it cuts.
# Usage: bash scripts/release-operator-kit_test.sh
# It exists because every block resolves the kit by these tags, so a wrong or moved tag reaches every consumer.
set -uo pipefail

# A runner redirects git's object and config storage, so every scratch repository is isolated from the ambient environment.
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/testlib-hermetic-git.sh"
hermetic_git_env

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$HERE/release-operator-kit.sh"

pass=0
fail=0
ROOTS=()
cleanup() { local d; for d in "${ROOTS[@]:-}"; do [ -n "$d" ] && rm -rf "$d"; done; }
trap cleanup EXIT

ok()  { pass=$((pass + 1)); }
bad() { fail=$((fail + 1)); echo "FAIL: $1"; }

check() { # check <desc> <want-exit> <got-exit>
  if [ "$2" = "$3" ]; then ok; else
    bad "$1 (want exit $2, got $3)"
    [ -n "${OUT:-}" ] && sed 's/^/    /' <<<"$OUT"
  fi
}

assert_out_contains() { # assert_out_contains <desc> <needle>
  if grep -qF -- "$2" <<<"${OUT:-}"; then ok; else
    bad "$1 — '$2' not in output"
    sed 's/^/    /' <<<"${OUT:-}"
  fi
}

assert_file_contains() { # assert_file_contains <desc> <file> <needle>
  if grep -qF -- "$3" "$2" 2>/dev/null; then ok; else
    bad "$1 — '$3' not in $2"
    [ -f "$2" ] && sed 's/^/    /' "$2"
  fi
}

assert_file_lacks() { # assert_file_lacks <desc> <file> <needle>
  if grep -qF -- "$3" "$2" 2>/dev/null; then
    bad "$1 — '$3' unexpectedly in $2"
    [ -f "$2" ] && sed 's/^/    /' "$2"
  else ok; fi
}

assert_tag() { # assert_tag <desc> <repo> <tag>
  if git -C "$2" rev-parse -q --verify "refs/tags/$3" >/dev/null 2>&1; then ok; else
    bad "$1 — tag $3 missing from $2"
  fi
}

assert_no_tag() { # assert_no_tag <desc> <repo> <tag>
  if git -C "$2" rev-parse -q --verify "refs/tags/$3" >/dev/null 2>&1; then
    bad "$1 — tag $3 unexpectedly present in $2"
  else ok; fi
}

assert_tag_at() { # assert_tag_at <desc> <repo> <tag> <want-sha>
  local got
  got="$(git -C "$2" rev-parse -q --verify "refs/tags/$3^{commit}" 2>/dev/null)"
  if [ "$got" = "$4" ]; then ok; else
    bad "$1 — tag $3 points at ${got:-<missing>}, want $4"
  fi
}

# kit_tag_count <repo> counts vX.Y.Z tags alone, since `git tag --list "v*"` also lists a fixture tag such as version-9.9.9.
kit_tag_count() {
  local tags
  tags="$(git -C "$1" tag --list "v*")"
  grep -cE '^v[0-9]+\.[0-9]+\.[0-9]+$' <<<"$tags"
}

assert_kit_tag_count() { # assert_kit_tag_count <desc> <repo> <want>
  local got; got="$(kit_tag_count "$2")"
  if [ "$got" = "$3" ]; then ok; else
    bad "$1 — $got kit tags, want $3: $(git -C "$2" tag --list "v*" | tr '\n' ' ')"
  fi
}

# --- scratch repository construction ------------------------------------------

# setup_repo builds a work repository that is the kit at its root and a bare origin, setting $W, $R and $SEED.
setup_repo() {
  local t; t="$(mktemp -d)"
  ROOTS+=("$t")
  R="$t/remote.git"
  W="$t/work"
  git init -q --bare "$R"
  git init -q -b main "$W"
  git -C "$W" remote add origin "$R"
  mkdir -p "$W/health"
  printf 'module github.com/mitstack-eu/operator-kit\n' >"$W/go.mod"
  printf 'package kit\n' >"$W/kit.go"
  printf 'package health\n' >"$W/health/health.go"
  git -C "$W" add -A
  git -C "$W" commit -q -m "seed"
  git -C "$W" push -q origin main
  SEED="$(git -C "$W" rev-parse HEAD)"
}

commit_change() { # commit_change <subject> [extra -m args...] commits a real file change
  local subject="$1"; shift
  printf 'package health // rev %s\n' "$RANDOM$RANDOM" >"$W/health/health.go"
  git -C "$W" add -A
  git -C "$W" commit -q -m "$subject" "$@"
}

commit_nested_change() { # commit_nested_change <subject> commits a change deep under an unrelated path
  mkdir -p "$W/deeply/nested/dir"
  printf 'more %s\n' "$RANDOM$RANDOM" >"$W/deeply/nested/dir/file.txt"
  git -C "$W" add -A
  git -C "$W" commit -q -m "$1"
}

commit_empty() { # commit_empty <subject> commits no change, the one unchanged range when the whole repository is the kit
  git -C "$W" commit -q --allow-empty -m "$1"
}

tag_at() { git -C "$W" tag "$1" "${2:-HEAD}"; }

head_sha() { git -C "$W" rev-parse HEAD; }

# --- running the script -------------------------------------------------------

# A runner sets GITHUB_ACTIONS and GITHUB_OUTPUT, so every run strips them and the cases reading GITHUB_OUTPUT set it.
run() { # run <args...>
  OUT="$(env -u GITHUB_ACTIONS -u GITHUB_OUTPUT bash "$SCRIPT" "$@" 2>&1)"
  RC=$?
}

run_with_gh_output() { # run_with_gh_output <file> <args...>
  local f="$1"; shift
  OUT="$(env -u GITHUB_ACTIONS GITHUB_OUTPUT="$f" bash "$SCRIPT" "$@" 2>&1)"
  RC=$?
}

# 1. Nothing changed in the range (an empty commit): nothing is cut and the run says so.
setup_repo
tag_at "v0.5.0"
git -C "$W" push -q origin "v0.5.0"
commit_empty "chore: an empty commit touching nothing"
run --repo "$W" --base "$SEED" --head "$(head_sha)"
check "unchanged repo exits 0" 0 "$RC"
assert_out_contains "unchanged run says unchanged" "unchanged"
assert_kit_tag_count "unchanged run cuts no tag" "$W" 1

# 2. The repository changed and v0.5.0 is newest: v0.5.1 is cut, locally and on origin.
setup_repo
tag_at "v0.5.0"
git -C "$W" push -q origin "v0.5.0"
commit_change "fix: fix the health probe"
H="$(head_sha)"
run --repo "$W" --base "$SEED" --head "$H"
check "changed repo exits 0" 0 "$RC"
assert_tag "patch raise cuts v0.5.1" "$W" "v0.5.1"
assert_tag_at "the cut tag points at head" "$W" "v0.5.1" "$H"
assert_tag "the cut tag is pushed to the remote" "$R" "v0.5.1"
assert_tag_at "the pushed tag points at head" "$R" "v0.5.1" "$H"
assert_kit_tag_count "exactly one new tag" "$W" 2

# 3. `kit-release: minor` in a range commit raises the minor level.
setup_repo
tag_at "v0.5.0"
commit_change "feat: add a capability" -m "kit-release: minor"
run --repo "$W" --base "$SEED" --head "$(head_sha)"
check "minor raise exits 0" 0 "$RC"
assert_tag "minor raise cuts v0.6.0" "$W" "v0.6.0"
assert_no_tag "minor raise does not cut a patch" "$W" "v0.5.1"
assert_tag "minor raise reaches the remote" "$R" "v0.6.0"

# 3b. The level is matched case-insensitively.
setup_repo
tag_at "v0.5.0"
commit_change "feat: add a capability" -m "kit-release: MINOR"
run --repo "$W" --base "$SEED" --head "$(head_sha)"
check "uppercase minor exits 0" 0 "$RC"
assert_tag "uppercase minor still cuts v0.6.0" "$W" "v0.6.0"

# 3c. The key itself is matched case-insensitively too, not only the level.
setup_repo
tag_at "v0.5.0"
commit_change "feat: add a capability" -m "KIT-RELEASE: minor"
run --repo "$W" --base "$SEED" --head "$(head_sha)"
check "uppercase key exits 0" 0 "$RC"
assert_tag "an uppercase key still cuts v0.6.0" "$W" "v0.6.0"
assert_no_tag "an uppercase key does not fall back to a patch" "$W" "v0.5.1"

# 3d. Key and level may each carry their own mixture of cases.
setup_repo
tag_at "v0.5.0"
commit_change "feat: add a capability" -m "Kit-Release: MINOR"
run --repo "$W" --base "$SEED" --head "$(head_sha)"
check "mixed-case key and level exit 0" 0 "$RC"
assert_tag "a mixed-case trailer still cuts v0.6.0" "$W" "v0.6.0"
assert_no_tag "a mixed-case trailer does not fall back to a patch" "$W" "v0.5.1"

# 3e. Prose after the level means the trailer is discussed rather than declared, so the raise is the patch.
setup_repo
tag_at "v0.5.0"
commit_change "feat: add a capability" -m "kit-release: minor is what we should have used here"
run --repo "$W" --base "$SEED" --head "$(head_sha)"
check "trailing prose after the level exits 0" 0 "$RC"
assert_tag "trailing prose leaves the ordinary patch raise" "$W" "v0.5.1"
assert_no_tag "trailing prose raises no minor" "$W" "v0.6.0"

# 3f. Only leading whitespace may precede the key, so a key mentioned mid-sentence declares nothing.
setup_repo
tag_at "v0.5.0"
commit_change "feat: add a capability" -m "see kit-release: minor for the convention"
run --repo "$W" --base "$SEED" --head "$(head_sha)"
check "an embedded key exits 0" 0 "$RC"
assert_tag "an embedded key leaves the ordinary patch raise" "$W" "v0.5.1"
assert_no_tag "an embedded key raises no minor" "$W" "v0.6.0"

# 3g. Leading prose alone is enough to refuse, with the level ending the line and nothing trailing it.
setup_repo
tag_at "v0.5.0"
commit_change "feat: add a capability" -m "see kit-release: minor"
run --repo "$W" --base "$SEED" --head "$(head_sha)"
check "leading prose alone exits 0" 0 "$RC"
assert_tag "leading prose alone leaves the ordinary patch raise" "$W" "v0.5.1"
assert_no_tag "leading prose alone raises no minor" "$W" "v0.6.0"

# 4. `kit-release: major` in a range commit raises the major level and zeroes the levels below it.
setup_repo
tag_at "v0.5.0"
commit_change "feat!: break the webhook signature" -m "kit-release: major"
run --repo "$W" --base "$SEED" --head "$(head_sha)"
check "major raise exits 0" 0 "$RC"
assert_tag "major raise cuts v1.0.0" "$W" "v1.0.0"
assert_no_tag "major raise does not cut a minor" "$W" "v0.6.0"
assert_tag "major raise reaches the remote" "$R" "v1.0.0"

# 5. Both levels declared in one range: major wins.
setup_repo
tag_at "v0.5.0"
commit_change "feat: add a capability" -m "kit-release: minor"
commit_change "feat!: break the webhook signature" -m "kit-release: major"
run --repo "$W" --base "$SEED" --head "$(head_sha)"
check "mixed raise exits 0" 0 "$RC"
assert_tag "major wins over minor" "$W" "v1.0.0"
assert_no_tag "the minor raise is not also cut" "$W" "v0.6.0"
assert_kit_tag_count "mixed raise cuts exactly one tag" "$W" 2

# 6. The base tag is chosen by numeric version order, not lexical order.
setup_repo
tag_at "v0.9.0"
tag_at "v0.10.0"
commit_change "fix: another fix"
run --repo "$W" --base "$SEED" --head "$(head_sha)"
check "numeric sort exits 0" 0 "$RC"
assert_tag "v0.10.0 is newer than v0.9.0, so v0.10.1 is cut" "$W" "v0.10.1"
assert_no_tag "the lexically largest tag is not the base" "$W" "v0.9.1"

# 7. A tag that is not shaped vX.Y.Z is ignored when picking the base.
setup_repo
tag_at "v1"
tag_at "version-9.9.9"
tag_at "v0.5.0"
commit_change "fix: another fix"
run --repo "$W" --base "$SEED" --head "$(head_sha)"
check "malformed tags ignored, exits 0" 0 "$RC"
assert_tag "the well-shaped tag is still the base" "$W" "v0.5.1"
assert_no_tag "a bare v1 tag is not raised" "$W" "v2"
assert_kit_tag_count "one new tag (the two malformed tags do not count)" "$W" 2

# 8. No release tag exists at all: the implied base is v0.0.0, so a patch raise gives v0.0.1.
setup_repo
commit_change "feat: the first change ever tagged"
run --repo "$W" --base "$SEED" --head "$(head_sha)"
check "first tag exits 0" 0 "$RC"
assert_tag "an untagged repo starts at v0.0.1" "$W" "v0.0.1"
assert_tag "the first tag reaches the remote" "$R" "v0.0.1"

# 9. One tag per push: a re-run over the same range cuts nothing more.
setup_repo
tag_at "v0.5.0"
commit_change "fix: fix the health probe"
H="$(head_sha)"
run --repo "$W" --base "$SEED" --head "$H"
check "first run cuts and exits 0" 0 "$RC"
run --repo "$W" --base "$SEED" --head "$H"
check "re-run over the same range exits 0" 0 "$RC"
assert_kit_tag_count "a re-run cuts no second tag" "$W" 2
assert_no_tag "a re-run does not raise again" "$W" "v0.5.2"

# 10. An empty --base means unknown, so the script resolves the base itself: the newest tag's commit, or <head>^ when there is no tag at all.
setup_repo
tag_at "v0.5.0"
commit_change "fix: fix the health probe"
run --repo "$W" --base "" --head "$(head_sha)"
check "empty base, repo changed, exits 0" 0 "$RC"
assert_tag "empty base resolves the base itself and cuts" "$W" "v0.5.1"

setup_repo
tag_at "v0.5.0"
commit_empty "chore: an empty commit"
run --repo "$W" --base "" --head "$(head_sha)"
check "empty base, repo unchanged, exits 0" 0 "$RC"
assert_out_contains "empty base still reports unchanged" "unchanged"
assert_kit_tag_count "empty base cuts nothing when unchanged" "$W" 1

# 10c. No tag and head has no parent either, so the repo counts as changed.
setup_repo
run --repo "$W" --base "" --head "$SEED"
check "rootless head exits 0" 0 "$RC"
assert_tag "a head with no parent counts as a change" "$W" "v0.0.1"

# 11. The all-zero sha means unknown in exactly the same way, and it resolves to the newest tag's commit rather than to <head>^.
ZERO="0000000000000000000000000000000000000000"
setup_repo
tag_at "v0.5.0"
commit_change "fix: fix the health probe"
run --repo "$W" --base "$ZERO" --head "$(head_sha)"
check "zero base, repo changed, exits 0" 0 "$RC"
assert_tag "zero base resolves the base itself and cuts" "$W" "v0.5.1"

# 11b. head^ and the newest tag's commit differ, and the tag is what counts: the real change sits one commit further back than <head>^ can see.
setup_repo
tag_at "v0.5.0"
commit_change "fix: fix the health probe"
commit_change "chore: a later change"
H="$(head_sha)"
run --repo "$W" --base "$ZERO" --head "$H"
check "zero base reaching past head^ exits 0" 0 "$RC"
assert_tag "the base is the newest tag's commit, not head^" "$W" "v0.5.1"
assert_tag_at "the cut tag points at head" "$W" "v0.5.1" "$H"

setup_repo
tag_at "v0.5.0"
commit_empty "chore: an empty commit"
run --repo "$W" --base "$ZERO" --head "$(head_sha)"
check "zero base, repo unchanged, exits 0" 0 "$RC"
assert_kit_tag_count "zero base cuts nothing when unchanged" "$W" 1

# 12. --verify-only fails loudly when the repo changed and no tag contains head.
setup_repo
tag_at "v0.5.0"
commit_change "fix: fix the health probe"
run --repo "$W" --base "$SEED" --head "$(head_sha)" --verify-only
check "verify-only fails on an untagged change" 1 "$RC"
assert_kit_tag_count "verify-only cuts nothing even when it fails" "$W" 1

# 13. --verify-only passes when a tag does contain head.
setup_repo
tag_at "v0.5.0"
commit_change "fix: fix the health probe"
tag_at "v0.5.1"
run --repo "$W" --base "$SEED" --head "$(head_sha)" --verify-only
check "verify-only passes when head is tagged" 0 "$RC"
assert_kit_tag_count "verify-only cuts nothing when it passes" "$W" 2

# 14. --verify-only passes when nothing changed at all.
setup_repo
tag_at "v0.5.0"
commit_empty "chore: an empty commit"
run --repo "$W" --base "$SEED" --head "$(head_sha)" --verify-only
check "verify-only passes on an unchanged repo" 0 "$RC"
assert_kit_tag_count "verify-only on an unchanged repo cuts nothing" "$W" 1

# 15. --dry-run names the tag it would cut and creates nothing.
setup_repo
tag_at "v0.5.0"
commit_change "fix: fix the health probe"
run --repo "$W" --base "$SEED" --head "$(head_sha)" --dry-run
check "dry run exits 0" 0 "$RC"
assert_out_contains "dry run prints the tag it would cut" "v0.5.1"
assert_no_tag "dry run creates no local tag" "$W" "v0.5.1"
assert_no_tag "dry run pushes nothing" "$R" "v0.5.1"
assert_kit_tag_count "dry run leaves the tag set alone" "$W" 1

# 16. An omitted --base takes the newest tag's commit, so a skipped or red run never loses its release.
setup_repo
tag_at "v0.5.0"
commit_change "fix: fix the health probe"
H="$(head_sha)"
run --repo "$W" --head "$H"
check "an omitted --base is not a usage error" 0 "$RC"
assert_tag "an omitted --base cuts from the newest tag" "$W" "v0.5.1"
assert_tag_at "the cut tag points at head" "$W" "v0.5.1" "$H"
assert_tag "the cut tag reaches the remote" "$R" "v0.5.1"

# 17. An unknown flag is a usage error.
setup_repo
run --repo "$W" --base "$SEED" --head "$(head_sha)" --publish-everything
check "an unknown flag is a usage error" 2 "$RC"
assert_kit_tag_count "a usage error cuts nothing" "$W" 0

# 18. The workflow reads the outcome from GITHUB_OUTPUT.
setup_repo
tag_at "v0.5.0"
commit_change "fix: fix the health probe"
GH_OUT="$W/../gh-output-cut"
: >"$GH_OUT"
run_with_gh_output "$GH_OUT" --repo "$W" --base "$SEED" --head "$(head_sha)"
check "a cut with GITHUB_OUTPUT set exits 0" 0 "$RC"
assert_file_contains "a cut reports released=true" "$GH_OUT" "released=true"
assert_file_contains "a cut reports the tag it made" "$GH_OUT" "tag=v0.5.1"

setup_repo
tag_at "v0.5.0"
commit_empty "chore: an empty commit"
GH_OUT="$W/../gh-output-unchanged"
: >"$GH_OUT"
run_with_gh_output "$GH_OUT" --repo "$W" --base "$SEED" --head "$(head_sha)"
check "an unchanged range with GITHUB_OUTPUT set exits 0" 0 "$RC"
assert_file_contains "an unchanged range reports released=false" "$GH_OUT" "released=false"
assert_file_lacks "an unchanged range never reports a release" "$GH_OUT" "released=true"

# 19. A computed tag already published at an unrelated commit fails the run and stays where it was, since a moved tag is worse than a failed job.
setup_repo
tag_at "v0.5.0"
git -C "$W" push -q origin "v0.5.0"
# Somebody else's commit, tagged with the name our next patch raise computes and published, then dropped locally so the local version scan cannot see it.
commit_change "fix: somebody else's change"
UNRELATED="$(head_sha)"
tag_at "v0.5.1"
git -C "$W" push -q origin "v0.5.1"
git -C "$W" tag -d "v0.5.1" >/dev/null
git -C "$W" reset -q --hard "$SEED"
commit_change "fix: our change"
run --repo "$W" --base "$SEED" --head "$(head_sha)"
check "a colliding tag fails the run" 1 "$RC"
assert_tag_at "the published tag is not clobbered" "$R" "v0.5.1" "$UNRELATED"

# 20. A change under any path in the repository counts as a kit change.
setup_repo
tag_at "v0.5.0"
commit_nested_change "chore: touch a deeply nested unrelated-looking path"
run --repo "$W" --base "$SEED" --head "$(head_sha)"
check "a deeply nested path change exits 0" 0 "$RC"
assert_tag "a change anywhere in the repo cuts v0.5.1" "$W" "v0.5.1"

# 21. The steady state is quiet: with no --base and nothing changed since the newest tag, the run cuts nothing.
setup_repo
tag_at "v0.5.0"
commit_change "fix: fix the health probe"
tag_at "v0.5.1"
git -C "$W" push -q origin "v0.5.1"
run --repo "$W" --head "$(head_sha)"
check "head sitting on the newest tag exits 0" 0 "$RC"
assert_kit_tag_count "head at the newest tag cuts nothing" "$W" 2
assert_no_tag "no second tag for an already released repo" "$W" "v0.5.2"

# 22. No --base and no tag anywhere: the base falls back to <head>^.
setup_repo
commit_change "feat: the first change ever tagged"
run --repo "$W" --head "$(head_sha)"
check "no base and no tag exits 0" 0 "$RC"
assert_tag "with no tag the head^ range cuts v0.0.1" "$W" "v0.0.1"
assert_tag "the first tag reaches the remote" "$R" "v0.0.1"

setup_repo
commit_empty "chore: an empty commit"
run --repo "$W" --head "$(head_sha)"
check "no base, no tag, nothing changed in head^ range, exits 0" 0 "$RC"
assert_kit_tag_count "the head^ fallback cuts nothing when nothing changed" "$W" 0

# 23. A change whose release run was skipped or went red is released by the next push.
setup_repo
tag_at "v0.5.0"
git -C "$W" push -q origin "v0.5.0"
commit_change "fix: the change whose release run was skipped"
commit_change "chore: a later change"
commit_change "chore: another later change"
C="$(head_sha)"
run --repo "$W" --head "$C"
check "an untagged earlier change is picked up, exits 0" 0 "$RC"
assert_tag "the missed release is cut on the next push" "$W" "v0.5.1"
assert_tag_at "the healing tag points at the current head" "$W" "v0.5.1" "$C"
assert_tag "the healing tag reaches the remote" "$R" "v0.5.1"
assert_tag_at "the pushed healing tag points at the current head" "$R" "v0.5.1" "$C"
OUT="$(git -C "$W" tag --list "v*" --contains "$C")"
assert_out_contains "head is reachable from a tag afterwards" "v0.5.1"

# 24. The script's header describes this repository's own root and plain vX.Y.Z tags.
if [ -f "$SCRIPT" ]; then
  header="$(sed -n '1,60p' "$SCRIPT")"
  if grep -q "examples/operator-kit" <<<"$header"; then
    bad "release-operator-kit.sh header/usage still mentions the retired examples/operator-kit subdirectory layout"
  else
    ok
  fi
  if grep -qE 'v[0-9]+\.[0-9]+\.[0-9]+' <<<"$header"; then
    ok
  else
    bad "release-operator-kit.sh header/usage does not show a plain vX.Y.Z tag shape"
  fi
else
  bad "release-operator-kit.sh does not exist yet — its header cannot describe the present"
  bad "release-operator-kit.sh does not exist yet — its usage cannot show a plain vX.Y.Z tag shape"
fi

echo "----"
echo "release-operator-kit: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
