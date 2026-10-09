#!/usr/bin/env bash
# Appends this lab's drill history to the history branch and refreshes the
# badge and the trend tables there (spec §12): main stays free of bot commits.
# Runs on the CI host after scripts/drill-programme.sh.
# Usage: scripts/publish-history.sh [branch]   (default drill-history)
set -euo pipefail

branch=${1:-drill-history}
dir=.drill-history # inside the repository, so the toolbox (/work) sees it

[[ -s reports/history.jsonl ]] || { echo "no drill history to publish"; exit 0; }
git worktree remove --force "$dir" 2>/dev/null || true
if git fetch --quiet origin "$branch" 2>/dev/null; then
  git worktree add --quiet -B "$branch" "$dir" "origin/$branch"
else
  git worktree add --quiet --detach "$dir"
  git -C "$dir" checkout --quiet --orphan "$branch"
  git -C "$dir" rm -rfq .
fi

cat reports/history.jsonl >> "$dir/history.jsonl"
tb() { docker compose exec -T toolbox build/disavery "$@"; }
tb report badge --history "$dir/history.jsonl" > "$dir/badge.json"
{
  echo "# Drill history"
  echo
  echo "Written by the drills workflow; the badge in the README reads badge.json."
  echo
  echo "## Trend"
  echo
  tb report trend --history "$dir/history.jsonl"
  echo
  echo "## DR tiers (site loss, S1)"
  echo
  tb report tiers --history "$dir/history.jsonl"
} > "$dir/README.md"

git -C "$dir" add history.jsonl badge.json README.md
git -C "$dir" -c user.name="github-actions[bot]" -c user.email="41898282+github-actions[bot]@users.noreply.github.com" \
  commit --quiet -m "chore: drill history of run ${GITHUB_RUN_ID:-local}"
git -C "$dir" push --quiet origin "HEAD:$branch"
git worktree remove --force "$dir"
echo "published to $branch"
