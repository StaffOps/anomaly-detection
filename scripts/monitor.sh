#!/bin/bash
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$(dirname "$SCRIPT_DIR")/controller"

prom_val() { echo "$1" | grep -v '^#' | grep "$2" | awk '{print $NF}' | head -1; }
prom_sum() { echo "$1" | grep -v '^#' | grep "$2" | awk '{s+=$NF}END{print s+0}'; }

while true; do
  # Collect all data before rendering
  CTRL=$(curl -s http://localhost:8080/metrics)
  WIP=$(docker inspect 06-staffops-worker-1 2>/dev/null | grep -oP '"IPAddress": "\K[0-9.]+' | head -1)
  WORK=$(curl -s "http://$WIP:8081/metrics")
  BCNT=$(docker exec 06-staffops-redis-1 redis-cli HGET "baseline:cpu_by_workload:$(docker exec 06-staffops-redis-1 redis-cli KEYS 'baseline:cpu_by_workload:*' 2>/dev/null | head -1 | tr -d '\r\n' | awk -F: '{print $NF}')" count 2>/dev/null | tr -d '\r\n')
  BCNT=${BCNT:-0}
  ANOMALIES=$(docker compose logs controller 2>&1 | grep "anomaly_detected" | tail -40 | \
    sed -n 's/.*"metric":"\([^"]*\)".*"namespace":"\([^"]*\)".*"pod":"\([^"]*\)".*"value":\([0-9.]*\).*"detector":"\([^"]*\)".*/\1|\2|\3|\4|\5/p' | \
    sort -u | tail -15)

  # Parse prometheus metrics
  CYC=$(prom_val "$CTRL" 'cycles_total{status="success"}')
  WARN=$(prom_sum "$CTRL" 'detected_total{severity="warning"')
  CRIT=$(prom_sum "$CTRL" 'detected_total{severity="critical"')
  CORR=$(prom_sum "$CTRL" 'correlated_total')
  ALRT=$(prom_sum "$CTRL" 'alerts_fired_total')
  DDUP=$(prom_val "$CTRL" 'alerts_deduplicated_total')
  CSUM=$(prom_val "$CTRL" 'cycle_duration_seconds_sum')
  CCNT=$(prom_val "$CTRL" 'cycle_duration_seconds_count')
  CAVG=$(awk "BEGIN{printf \"%.2f\", ${CSUM:-0}/(${CCNT:-1}+0.001)}")
  SDET=$(prom_val "$WORK" 'detections_total{detector="static"}')
  ADET=$(prom_val "$WORK" 'detections_total{detector="adaptive"}')
  BUPD=$(prom_val "$WORK" 'baseline_updates_total')
  VMQ=$(prom_val "$WORK" 'query_duration_seconds_count{datasource="vm"}')
  VMS=$(prom_val "$WORK" 'query_duration_seconds_sum{datasource="vm"}')
  LKQ=$(prom_val "$WORK" 'query_duration_seconds_count{datasource="loki"}')
  LKS=$(prom_val "$WORK" 'query_duration_seconds_sum{datasource="loki"}')
  VMA=$(awk "BEGIN{printf \"%.0f\", (${VMS:-0}/(${VMQ:-1}+0.001))*1000}")
  LKA=$(awk "BEGIN{printf \"%.0f\", (${LKS:-0}/(${LKQ:-1}+0.001))*1000}")

  # Warm-up bar
  PCT=$((BCNT * 100 / 60))
  [ $PCT -gt 100 ] && PCT=100
  FILLED=$((PCT / 5))
  EMPT=$((20 - FILLED))
  BAR=""
  for ((i=0;i<FILLED;i++)); do BAR+="█"; done
  for ((i=0;i<EMPT;i++)); do BAR+="░"; done

  # Render
  clear
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo " ANOMALY DETECTION — $(date '+%Y-%m-%d %H:%M:%S')"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo ""
  echo " CONTROLLER"
  echo "   Ciclos: ${CYC:-0}    Avg duração: ${CAVG}s"
  echo "   Anomalias:  warning=${WARN:-0}  critical=${CRIT:-0}"
  echo "   Correlacoes: ${CORR:-0}    Alertas: ${ALRT:-0}    Dedup: ${DDUP:-0}"
  echo ""
  echo " WORKERS (3 replicas)"
  echo "   Deteccoes:  static=${SDET:-0}    adaptive=${ADET:-0}"
  echo "   Baseline:   updates=${BUPD:-0}"
  echo "   Queries:    Prometheus=${VMQ:-0} (avg ${VMA}ms)  Loki=${LKQ:-0} (avg ${LKA}ms)"
  echo ""
  if [ "$PCT" -lt 100 ]; then
    echo " WARM-UP: [${BAR}] ${PCT}% (${BCNT}/60)"
  else
    echo " BASELINE: Completo — adaptive detection ATIVA"
  fi
  echo ""
  ANOM_COUNT=$(echo "$ANOMALIES" | grep -c '|' || true)
  echo " ANOMALIAS (${ANOM_COUNT} unicas)"
  printf "   %-20s %-14s %-42s %-9s %s\n" "REGRA" "NAMESPACE" "POD" "VALOR" "TIPO"
  printf "   %-20s %-14s %-42s %-9s %s\n" "--------------------" "--------------" "------------------------------------------" "---------" "------"
  while IFS='|' read -r M N P V D; do
    [ -z "$M" ] && continue
    printf "   %-20s %-14s %-42s %-9s %s\n" "$M" "$N" "${P:0:42}" "$V" "$D"
  done <<< "$ANOMALIES"
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo " Ctrl+C sair | Refresh 10s"
  sleep 10
done
