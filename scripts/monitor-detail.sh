#!/bin/bash
# Detailed view of anomaly-detection internals
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$(dirname "$SCRIPT_DIR")/controller"

prom_val() { echo "$1" | grep -v '^#' | grep "$2" | awk '{print $NF}' | head -1; }
prom_sum() { echo "$1" | grep -v '^#' | grep "$2" | awk '{s+=$NF}END{print s+0}'; }

while true; do
  CTRL=$(curl -s http://localhost:8080/metrics)
  WIP=$(docker inspect 06-staffops-worker-1 2>/dev/null | grep -oP '"IPAddress": "\K[0-9.]+' | head -1)
  W1=$(curl -s "http://$WIP:8081/metrics")
  WIP2=$(docker inspect 06-staffops-worker-2 2>/dev/null | grep -oP '"IPAddress": "\K[0-9.]+' | head -1)
  W2=$(curl -s "http://$WIP2:8081/metrics")
  WIP3=$(docker inspect 06-staffops-worker-3 2>/dev/null | grep -oP '"IPAddress": "\K[0-9.]+' | head -1)
  W3=$(curl -s "http://$WIP3:8081/metrics")

  # Anomalies breakdown by namespace
  ANOM_BY_NS=$(docker compose logs controller 2>&1 | grep "anomaly_detected" | \
    sed -n 's/.*"metric":"\([^"]*\)".*"namespace":"\([^"]*\)".*"severity":"\([^"]*\)".*/\3|\1|\2/p' | \
    sort | uniq -c | sort -rn | head -15)

  # Correlations from logs
  CORR_LOGS=$(docker compose logs controller 2>&1 | grep -i "correlated\|escalat" | tail -10)

  # Dedup info
  DEDUP_KEYS=$(docker exec 06-staffops-redis-1 redis-cli KEYS "alert:dedup:*" 2>/dev/null | wc -l | tr -d '\r\n')
  DEDUP_TTLS=$(docker exec 06-staffops-redis-1 redis-cli KEYS "alert:dedup:*" 2>/dev/null | tr -d '\r' | while read k; do
    TTL=$(docker exec 06-staffops-redis-1 redis-cli TTL "$k" 2>/dev/null | tr -d '\r\n')
    echo "${TTL}s"
  done | paste -sd, -)

  # Baseline stats from Redis
  BL_TOTAL=$(docker exec 06-staffops-redis-1 redis-cli KEYS "baseline:*" 2>/dev/null | wc -l | tr -d '\r\n')
  BL_BY_METRIC=$(docker exec 06-staffops-redis-1 redis-cli KEYS "baseline:*" 2>/dev/null | tr -d '\r' | sed 's/baseline://' | cut -d: -f1 | sort | uniq -c | sort -rn)

  # Sample baselines (one per metric type)
  SAMPLE_CPU_KEY=$(docker exec 06-staffops-redis-1 redis-cli KEYS "baseline:cpu_by_workload:*" 2>/dev/null | head -1 | tr -d '\r\n')
  SAMPLE_CPU=$(docker exec 06-staffops-redis-1 redis-cli HGETALL "$SAMPLE_CPU_KEY" 2>/dev/null | tr -d '\r' | paste - - | tr '\t' '=')
  SAMPLE_ERR_KEY=$(docker exec 06-staffops-redis-1 redis-cli KEYS "baseline:error_rate_by_service:*" 2>/dev/null | head -1 | tr -d '\r\n')
  SAMPLE_ERR=$(docker exec 06-staffops-redis-1 redis-cli HGETALL "$SAMPLE_ERR_KEY" 2>/dev/null | tr -d '\r' | paste - - | tr '\t' '=')

  # Worker job stats
  W1_STATIC=$(prom_val "$W1" 'jobs_processed_total{status="success",type="JOB_TYPE_METRICS_STATIC"}')
  W1_ADAPT=$(prom_val "$W1" 'jobs_processed_total{status="success",type="JOB_TYPE_METRICS_ADAPTIVE"}')
  W1_LOGS=$(prom_val "$W1" 'jobs_processed_total{status="success",type="JOB_TYPE_LOGS"}')
  W2_STATIC=$(prom_val "$W2" 'jobs_processed_total{status="success",type="JOB_TYPE_METRICS_STATIC"}')
  W2_ADAPT=$(prom_val "$W2" 'jobs_processed_total{status="success",type="JOB_TYPE_METRICS_ADAPTIVE"}')
  W2_LOGS=$(prom_val "$W2" 'jobs_processed_total{status="success",type="JOB_TYPE_LOGS"}')
  W3_STATIC=$(prom_val "$W3" 'jobs_processed_total{status="success",type="JOB_TYPE_METRICS_STATIC"}')
  W3_ADAPT=$(prom_val "$W3" 'jobs_processed_total{status="success",type="JOB_TYPE_METRICS_ADAPTIVE"}')
  W3_LOGS=$(prom_val "$W3" 'jobs_processed_total{status="success",type="JOB_TYPE_LOGS"}')

  # Job durations (avg ms)
  ST_SUM=$(prom_val "$W1" 'job_duration_seconds_sum{type="JOB_TYPE_METRICS_STATIC"}')
  ST_CNT=$(prom_val "$W1" 'job_duration_seconds_count{type="JOB_TYPE_METRICS_STATIC"}')
  AD_SUM=$(prom_val "$W1" 'job_duration_seconds_sum{type="JOB_TYPE_METRICS_ADAPTIVE"}')
  AD_CNT=$(prom_val "$W1" 'job_duration_seconds_count{type="JOB_TYPE_METRICS_ADAPTIVE"}')
  LG_SUM=$(prom_val "$W1" 'job_duration_seconds_sum{type="JOB_TYPE_LOGS"}')
  LG_CNT=$(prom_val "$W1" 'job_duration_seconds_count{type="JOB_TYPE_LOGS"}')
  ST_AVG=$(awk "BEGIN{printf \"%.0f\", (${ST_SUM:-0}/(${ST_CNT:-1}+0.001))*1000}")
  AD_AVG=$(awk "BEGIN{printf \"%.0f\", (${AD_SUM:-0}/(${AD_CNT:-1}+0.001))*1000}")
  LG_AVG=$(awk "BEGIN{printf \"%.0f\", (${LG_SUM:-0}/(${LG_CNT:-1}+0.001))*1000}")

  # Redis perf
  REDIS_OPS=$(prom_val "$W1" 'redis_operation_duration_seconds_count')
  REDIS_SUM=$(prom_val "$W1" 'redis_operation_duration_seconds_sum')
  REDIS_AVG=$(awk "BEGIN{printf \"%.2f\", (${REDIS_SUM:-0}/(${REDIS_OPS:-1}+0.001))*1000}")

  # Render
  clear
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo " ANOMALY DETECTION — DETAIL VIEW — $(date '+%H:%M:%S')"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

  echo ""
  echo " CONTROLLER — Anomalias por namespace/regra"
  echo "   COUNT  SEV      REGRA                NAMESPACE"
  echo "   -----  -------  -------------------  -----------------"
  echo "$ANOM_BY_NS" | while read -r CNT SEV_RULE; do
    SEV=$(echo "$SEV_RULE" | cut -d'|' -f1)
    RULE=$(echo "$SEV_RULE" | cut -d'|' -f2)
    NS=$(echo "$SEV_RULE" | cut -d'|' -f3)
    printf "   %-5s  %-7s  %-19s  %s\n" "$CNT" "$SEV" "$RULE" "$NS"
  done

  echo ""
  echo " CONTROLLER — Dedup & Correlacao"
  echo "   Alertas em cooldown: ${DEDUP_KEYS:-0}  (TTLs: ${DEDUP_TTLS:--})"
  CORR_W=$(prom_val "$CTRL" 'correlated_total{severity="warning"}')
  CORR_C=$(prom_val "$CTRL" 'correlated_total{severity="critical"}')
  echo "   Correlacoes:  warning=${CORR_W:-0}  critical=${CORR_C:-0}"
  echo "   Dedup total: $(prom_val "$CTRL" 'alerts_deduplicated_total')"

  echo ""
  echo " WORKERS — Jobs processados (por worker)"
  printf "   %-10s %-8s %-10s %s\n" "WORKER" "STATIC" "ADAPTIVE" "LOGS"
  printf "   %-10s %-8s %-10s %s\n" "----------" "--------" "----------" "----"
  printf "   %-10s %-8s %-10s %s\n" "worker-1" "${W1_STATIC:-0}" "${W1_ADAPT:-0}" "${W1_LOGS:-0}"
  printf "   %-10s %-8s %-10s %s\n" "worker-2" "${W2_STATIC:-0}" "${W2_ADAPT:-0}" "${W2_LOGS:-0}"
  printf "   %-10s %-8s %-10s %s\n" "worker-3" "${W3_STATIC:-0}" "${W3_ADAPT:-0}" "${W3_LOGS:-0}"

  echo ""
  echo " WORKERS — Performance por tipo de job"
  printf "   %-25s %-10s %s\n" "TIPO" "AVG (ms)" "EXECUCOES"
  printf "   %-25s %-10s %s\n" "-------------------------" "----------" "---------"
  printf "   %-25s %-10s %s\n" "METRICS_STATIC" "$ST_AVG" "${ST_CNT:-0}"
  printf "   %-25s %-10s %s\n" "METRICS_ADAPTIVE" "$AD_AVG" "${AD_CNT:-0}"
  printf "   %-25s %-10s %s\n" "LOGS" "$LG_AVG" "${LG_CNT:-0}"
  echo "   Redis: ${REDIS_OPS:-0} ops (avg ${REDIS_AVG}ms)"

  echo ""
  echo " BASELINE — Aprendizado"
  echo "   Total keys: ${BL_TOTAL:-0}"
  echo "$BL_BY_METRIC" | while read -r CNT NAME; do
    printf "     %-30s %s series\n" "$NAME" "$CNT"
  done
  echo ""
  echo "   Amostra cpu_by_workload:  $SAMPLE_CPU"
  echo "   Amostra error_rate:       $SAMPLE_ERR"

  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo " Ctrl+C sair | Refresh 15s"
  sleep 15
done
