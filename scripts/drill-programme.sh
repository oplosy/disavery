#!/usr/bin/env bash
# Runs a drill programme against the lab, as the nightly and weekly CI jobs do
# (spec §12). Runs on the host, next to `make`.
#
#   nightly  the random restore test (S6) plus one scenario that rotates by
#            day of the year: S1 on pilot light, S1 on warm standby, S2, S3,
#            S4, S5. Site losses are followed by their failback (S7).
#   weekly   the whole catalog.
#   scaling  RTO vs. data size (spec §14), locally: for each size in
#            SCALING_SIZES (default "1GB 5GB 10GB") seed the database up to it,
#            then run S1 + S7 on both tiers SCALING_RUNS times (default 3).
#            Not S6: it restores a random backup set, not the current size.
#            Summarise with `disavery report scaling`.
#
# A drill whose preflight fails changed nothing (ERROR); it is retried for up
# to five minutes, because the previous drill's recovery may still be settling
# (a firing PostgresPrimaryDown, a minute of steady canary writes).
# Exits with the worst result: 0 PASS, 2 MISSED_TARGET, 3 FAILED, 4 ERROR.
# Usage: scripts/drill-programme.sh nightly|weekly|scaling
set -uo pipefail

worst=0
tb() { docker compose exec -T toolbox "$@"; }

drill() {
  local out code
  for _ in $(seq 1 15); do
    out=$(tb build/disavery drill run "$@" 2>&1)
    code=$?
    printf '%s\n' "$out"
    if (( code == 4 )) && grep -q 'preflight failed' <<<"$out"; then
      sleep 20
      continue
    fi
    break
  done
  (( code > worst )) && worst=$code
  return 0
}

tier() {
  echo "== baseline: $1"
  if ! tb build/disavery env reset --tier "$1"; then
    worst=4
  fi
}

scenario() {
  case $1 in
    s1-pilot-light)  drill s1-site-loss --tier pilot-light --yes; drill s7-failback --tier pilot-light ;;
    s1-warm-standby) tier warm-standby
                     drill s1-site-loss --tier warm-standby --yes; drill s7-failback --tier warm-standby
                     tier pilot-light ;;
    s5)              drill s5-secret-loss --yes; drill s7-failback --tier pilot-light ;;
    s2)              drill s2-drop-table --yes ;;
    s3)              drill s3-bad-migration --yes ;;
    s4)              drill s4-ransomware --yes ;;
    *)               echo "unknown scenario $1" >&2; exit 4 ;;
  esac
}

rotation=(s1-pilot-light s1-warm-standby s2 s3 s4 s5)

case ${1:-} in
  nightly)
    drill s6-restore-test
    pick=${rotation[$(( 10#$(date -u +%j) % ${#rotation[@]} ))]}
    echo "== rotating scenario: $pick"
    scenario "$pick"
    ;;
  weekly)
    drill s6-restore-test
    for s in s2 s3 s4 s5 s1-pilot-light s1-warm-standby; do
      scenario "$s"
    done
    ;;
  scaling)
    for size in ${SCALING_SIZES:-1GB 5GB 10GB}; do
      # Every size starts from the pilot-light baseline; a lab stuck in site b
      # would seed the wrong primary and measure nothing comparable.
      echo "== seed: $size"
      if ! tb build/disavery env reset --tier pilot-light || ! tb build/disavery seed --size "$size"; then
        worst=4
        break
      fi
      for run in $(seq 1 "${SCALING_RUNS:-3}"); do
        echo "== $size, run $run"
        scenario s1-pilot-light
        scenario s1-warm-standby
      done
    done
    ;;
  *)
    echo "usage: $0 nightly|weekly|scaling" >&2
    exit 4
    ;;
esac
exit "$worst"
