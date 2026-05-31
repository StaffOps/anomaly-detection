#!/bin/bash
# Controller detail view
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$(dirname "$SCRIPT_DIR")/controller"

prom_val() { echo "$1" | grep -v '^#' | grep "$2" | awk '{print $NF}' | head -1; }
prom_sum() { echo "$1" | grep -v '^#' | grep "$2" | awk '{s+=$NF}END{print s+0}'; }

while true; do
  CTRL=$(curl -s http://localhost:8080/metrics)
  DEDUP_KEYS=$(docker exec 06-staffops-redis-1 redis-cli KEYS "alert:dedup:*" 2>/dev/null | grep -c "alert" | tr -d '\r\n')
  DEDUP_TTLS=$(docker exec 06-staffops-redis-1 redis-cli KEYS "alert:dedup:*" 2>/dev/null | tr -d '\r' | while read k; do
    [ -z "$k" ] && continue
    TTL=$(docker exec 06-staffops-redis-1 redis-cli TTL "$k" 2>/dev/null | tr -d '\r\n')
    echo "${TTL}s"
  done | paste -sd, -)

  ANOM_BY_NS=$(docker compose logs controller 2>&1 | grep "anomaly_detected" | \
    sed -n 's/.*"metric":"\([^"]*\)".*"namespace":"\([^"]*\)".*"severity":"\([^"]*\)".*/\3|\1|\2/p' | \
    sort | uniq -c | sort -rn | head -20)

  RECENT=$(docker compose logs controller 2>&1 | grep "anomaly_detected" | tail -10 | \
    sed -n 's/.*"time":"\([^"]*\)".*"metric":"\([^"]*\)".*"namespace":"\([^"]*\)".*"pod":"\([^"]*\)".*"value":\([0-9.]*\).*"detector":"\([^"]*\)".*/\1|\2|\3|\4|\5|\6/p')

  CYC=$(prom_val "$CTRL" 'cycles_total{status="success"}')
  CSUM=$(prom_val "$CTRL" 'cycle_duration_seconds_sum')
  CCNT=$(prom_val "$CTRL" 'cycle_duration_seconds_count')
  CAVG=$(awk "BEGIN{printf \"%.2f\", ${CSUM:-0}/(${CCNT:-1}+0.001)}")
  WARN=$(prom_sum "$CTRL" 'detected_total{severity="warning"')
  CRIT=$(prom_sum "$CTRL" 'detected_total{severity="critical"')
  CORR_W=$(prom_val "$CTRL" 'correlated_total{severity="warning"}')
  CORR_C=$(prom_val "$CTRL" 'correlated_total{severity="critical"}')
  ALRT=$(prom_sum "$CTRL" 'alerts_fired_total')
  DDUP=$(prom_val "$CTRL" 'alerts_deduplicated_total')
  JOBS=$(prom_sum "$CTRL" 'jobs_dispatched_total')

  clear
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo " CONTROLLER — $(date '+%H:%M:%S')"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo ""
  echo " Ciclos: ${CYC:-0}  |  Avg: ${CAVG}s  |  Jobs dispatched: ${JOBS:-0}"
  echo ""
  echo " ANOMALIAS DETECTADAS"
  echo "   Total: warning=${WARN:-0}  critical=${CRIT:-0}"
  echo ""
  echo "   Por namespace:"
  echo "   COUNT  SEVERIDADE  REGRA                NAMESPACE"
  echo "   -----  ----------  -------------------  -----------------"
  echo "$ANOM_BY_NS" | while read -r CNT REST; do
    SEV=$(echo "$REST" | cut -d'|' -f1)
    RULE=$(echo "$REST" | cut -d'|' -f2)
    NS=$(echo "$REST" | cut -d'|' -f3)
    [ -z "$CNT" ] && continue
    printf "   %-5s  %-10s  %-19s  %s\n" "$CNT" "$SEV" "$RULE" "$NS"
  done
  echo ""
  echo " CORRELACAO & DEDUP"
  echo "   Correlacoes disparadas:  warning=${CORR_W:-0}  critical=${CORR_C:-0}"
  echo "   Alertas enviados: ${ALRT:-0}"
  echo "   Dedup (suprimidos): ${DDUP:-0}"
  echo "   Em cooldown agora: ${DEDUP_KEYS:-0}  (TTLs: ${DEDUP_TTLS:--})"
  echo ""
  echo " ULTIMAS DETECCOES"
  printf "   %-8s %-18s %-12s %-40s %s\n" "HORA" "REGRA" "NAMESPACE" "POD" "VALOR"
  printf "   %-8s %-18s %-12s %-40s %s\n" "--------" "------------------" "------------" "----------------------------------------" "-------"
  echo "$RECENT" | while IFS='|' read -r T M N P V D; do
    [ -z "$M" ] && continue
    HORA=$(echo "$T" | grep -oP '\d{2}:\d{2}:\d{2}')
    printf "   %-8s %-18s %-12s %-40s %s\n" "$HORA" "$M" "$N" "${P:0:40}" "$V"
  done
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo " Ctrl+C sair | Refresh 15s"
  sleep 15
done
