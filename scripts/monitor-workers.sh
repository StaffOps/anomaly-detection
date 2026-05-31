#!/bin/bash
# Workers detail view
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$(dirname "$SCRIPT_DIR")/controller"

prom_val() { echo "$1" | grep -v '^#' | grep "$2" | awk '{print $NF}' | head -1; }

while true; do
  # Collect from all 3 workers
  declare -a WIPS WMETRICS
  for i in 1 2 3; do
    WIPS[$i]=$(docker inspect "06-staffops-worker-$i" 2>/dev/null | grep -oP '"IPAddress": "\K[0-9.]+' | head -1)
    WMETRICS[$i]=$(curl -s "http://${WIPS[$i]}:8081/metrics")
  done

  # Baseline from Redis
  BL_TOTAL=$(docker exec 06-staffops-redis-1 redis-cli KEYS "baseline:*" 2>/dev/null | wc -l | tr -d '\r\n')
  BL_BY_METRIC=$(docker exec 06-staffops-redis-1 redis-cli KEYS "baseline:*" 2>/dev/null | tr -d '\r' | sed 's/baseline://' | cut -d: -f1 | sort | uniq -c | sort -rn)

  # Sample counts per metric type
  CPU_CNT=$(docker exec 06-staffops-redis-1 redis-cli HGET "$(docker exec 06-staffops-redis-1 redis-cli KEYS 'baseline:cpu_by_workload:*' 2>/dev/null | head -1 | tr -d '\r\n')" count 2>/dev/null | tr -d '\r\n')
  ERR_CNT=$(docker exec 06-staffops-redis-1 redis-cli HGET "$(docker exec 06-staffops-redis-1 redis-cli KEYS 'baseline:error_rate_by_service:*' 2>/dev/null | head -1 | tr -d '\r\n')" count 2>/dev/null | tr -d '\r\n')
  REQ_CNT=$(docker exec 06-staffops-redis-1 redis-cli HGET "$(docker exec 06-staffops-redis-1 redis-cli KEYS 'baseline:request_rate_by_service:*' 2>/dev/null | head -1 | tr -d '\r\n')" count 2>/dev/null | tr -d '\r\n')
  LAT_CNT=$(docker exec 06-staffops-redis-1 redis-cli HGET "$(docker exec 06-staffops-redis-1 redis-cli KEYS 'baseline:latency_p99_by_service:*' 2>/dev/null | head -1 | tr -d '\r\n')" count 2>/dev/null | tr -d '\r\n')
  LOG_CNT=$(docker exec 06-staffops-redis-1 redis-cli HGET "$(docker exec 06-staffops-redis-1 redis-cli KEYS 'baseline:log_volume_by_workload:*' 2>/dev/null | head -1 | tr -d '\r\n')" count 2>/dev/null | tr -d '\r\n')

  clear
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo " WORKERS — $(date '+%H:%M:%S')"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

  echo ""
  echo " JOBS PROCESSADOS"
  printf "   %-10s %-10s %-12s %-8s %-10s %s\n" "WORKER" "STATIC" "ADAPTIVE" "LOGS" "DETECCOES" "BASELINE_UPD"
  printf "   %-10s %-10s %-12s %-8s %-10s %s\n" "----------" "----------" "------------" "--------" "----------" "------------"
  for i in 1 2 3; do
    W="${WMETRICS[$i]}"
    ST=$(prom_val "$W" 'jobs_processed_total{status="success",type="JOB_TYPE_METRICS_STATIC"}')
    AD=$(prom_val "$W" 'jobs_processed_total{status="success",type="JOB_TYPE_METRICS_ADAPTIVE"}')
    LG=$(prom_val "$W" 'jobs_processed_total{status="success",type="JOB_TYPE_LOGS"}')
    DET_S=$(prom_val "$W" 'detections_total{detector="static"}')
    DET_A=$(prom_val "$W" 'detections_total{detector="adaptive"}')
    BU=$(prom_val "$W" 'baseline_updates_total')
    printf "   %-10s %-10s %-12s %-8s %-10s %s\n" "worker-$i" "${ST:-0}" "${AD:-0}" "${LG:-0}" "s:${DET_S:-0} a:${DET_A:-0}" "${BU:-0}"
  done

  echo ""
  echo " QUERIES EXECUTADAS (worker-1)"
  W="${WMETRICS[1]}"
  VM_Q=$(prom_val "$W" 'query_duration_seconds_count{datasource="vm"}')
  VM_S=$(prom_val "$W" 'query_duration_seconds_sum{datasource="vm"}')
  LK_Q=$(prom_val "$W" 'query_duration_seconds_count{datasource="loki"}')
  LK_S=$(prom_val "$W" 'query_duration_seconds_sum{datasource="loki"}')
  VM_A=$(awk "BEGIN{printf \"%.0f\", (${VM_S:-0}/(${VM_Q:-1}+0.001))*1000}")
  LK_A=$(awk "BEGIN{printf \"%.0f\", (${LK_S:-0}/(${LK_Q:-1}+0.001))*1000}")

  ST_S=$(prom_val "$W" 'job_duration_seconds_sum{type="JOB_TYPE_METRICS_STATIC"}')
  ST_C=$(prom_val "$W" 'job_duration_seconds_count{type="JOB_TYPE_METRICS_STATIC"}')
  AD_S=$(prom_val "$W" 'job_duration_seconds_sum{type="JOB_TYPE_METRICS_ADAPTIVE"}')
  AD_C=$(prom_val "$W" 'job_duration_seconds_count{type="JOB_TYPE_METRICS_ADAPTIVE"}')
  LG_S=$(prom_val "$W" 'job_duration_seconds_sum{type="JOB_TYPE_LOGS"}')
  LG_C=$(prom_val "$W" 'job_duration_seconds_count{type="JOB_TYPE_LOGS"}')
  ST_A=$(awk "BEGIN{printf \"%.0f\", (${ST_S:-0}/(${ST_C:-1}+0.001))*1000}")
  AD_A=$(awk "BEGIN{printf \"%.0f\", (${AD_S:-0}/(${AD_C:-1}+0.001))*1000}")
  LG_A=$(awk "BEGIN{printf \"%.0f\", (${LG_S:-0}/(${LG_C:-1}+0.001))*1000}")

  printf "   %-20s %-12s %-12s %s\n" "DATASOURCE" "QUERIES" "AVG (ms)" "REGRAS"
  printf "   %-20s %-12s %-12s %s\n" "--------------------" "------------" "------------" "----------------------------"
  printf "   %-20s %-12s %-12s %s\n" "VictoriaMetrics" "${VM_Q:-0}" "$VM_A" "high_cpu_ratio, high_restart_rate,"
  printf "   %-20s %-12s %-12s %s\n" "" "" "" "high_memory_ratio, cpu_by_workload,"
  printf "   %-20s %-12s %-12s %s\n" "" "" "" "error/request/latency_by_service"
  printf "   %-20s %-12s %-12s %s\n" "Loki" "${LK_Q:-0}" "$LK_A" "error_rate_by_ns, log_volume_by_wl"
  echo ""
  printf "   %-25s %-12s %s\n" "TIPO JOB" "AVG (ms)" "O QUE FAZ"
  printf "   %-25s %-12s %s\n" "-------------------------" "------------" "--------------------------------"
  printf "   %-25s %-12s %s\n" "METRICS_STATIC" "$ST_A" "Compara valor vs threshold fixo"
  printf "   %-25s %-12s %s\n" "METRICS_ADAPTIVE" "$AD_A" "Calcula Z-Score vs baseline EWMA"
  printf "   %-25s %-12s %s\n" "LOGS" "$LG_A" "Rate de logs de erro via Loki"

  echo ""
  echo " REDIS"
  RD_OPS=$(prom_val "$W" 'redis_operation_duration_seconds_count')
  RD_SUM=$(prom_val "$W" 'redis_operation_duration_seconds_sum')
  RD_AVG=$(awk "BEGIN{printf \"%.3f\", (${RD_SUM:-0}/(${RD_OPS:-1}+0.001))*1000}")
  RD_ERR=$(prom_val "$W" 'redis_errors_total')
  RD_HGET=$(prom_val "$W" 'redis_operations_total{op="hgetall"}')
  RD_HSET=$(prom_val "$W" 'redis_operations_total{op="hset"}')
  echo "   Ops: ${RD_OPS:-0}  (hgetall=${RD_HGET:-0}  hset=${RD_HSET:-0})"
  echo "   Avg latency: ${RD_AVG}ms  |  Errors: ${RD_ERR:-0}"

  echo ""
  echo " BASELINE — Aprendizado (warm-up = 60 amostras)"
  echo "   Total series rastreadas: ${BL_TOTAL:-0}"
  echo ""
  printf "   %-30s %-8s %-8s %s\n" "METRICA" "SERIES" "AMOSTRAS" "STATUS"
  printf "   %-30s %-8s %-8s %s\n" "------------------------------" "--------" "--------" "----------"
  echo "$BL_BY_METRIC" | while read -r CNT NAME; do
    [ -z "$CNT" ] && continue
    case "$NAME" in
      cpu_by_workload) S="${CPU_CNT:-0}" ;;
      error_rate_by_service) S="${ERR_CNT:-0}" ;;
      request_rate_by_service) S="${REQ_CNT:-0}" ;;
      latency_p99_by_service) S="${LAT_CNT:-0}" ;;
      log_volume_by_workload) S="${LOG_CNT:-0}" ;;
      error_rate_by_namespace) S="${LOG_CNT:-0}" ;;
      *) S="?" ;;
    esac
    if [ "${S:-0}" -ge 60 ] 2>/dev/null; then
      STATUS="ATIVO"
    else
      STATUS="${S}/60"
    fi
    printf "   %-30s %-8s %-8s %s\n" "$NAME" "$CNT" "$S" "$STATUS"
  done

  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo " Ctrl+C sair | Refresh 15s"
  sleep 15
done
