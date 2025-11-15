# Portal Gateway Monitoring Guide

This guide explains how to set up and use monitoring for the Portal Gateway.

## Table of Contents

- [Overview](#overview)
- [Metrics](#metrics)
- [Grafana Dashboard](#grafana-dashboard)
- [Alerts](#alerts)
- [Setup Instructions](#setup-instructions)
- [Runbooks](#runbooks)

## Overview

Portal Gateway provides comprehensive monitoring through:
- **Prometheus metrics** for time-series data collection
- **Grafana dashboards** for visualization
- **Alert rules** for proactive issue detection

## Metrics

### Core Metrics

#### `portal_requests_total`
- **Type**: Counter
- **Labels**: `method`, `endpoint`, `status`
- **Description**: Total number of HTTP requests processed
- **Use Case**: Track request volume and status codes

#### `portal_request_duration_seconds`
- **Type**: Histogram
- **Labels**: `method`, `endpoint`
- **Buckets**: 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10
- **Description**: Request duration in seconds
- **Use Case**: Monitor latency (P50, P95, P99)

#### `portal_active_connections`
- **Type**: Gauge
- **Description**: Current number of active connections
- **Use Case**: Monitor connection pool usage

#### `portal_active_leases`
- **Type**: Gauge
- **Description**: Current number of active leases
- **Use Case**: Track active AI agent sessions

#### `portal_bytes_transferred_total`
- **Type**: Counter
- **Labels**: `direction` (sent/received)
- **Description**: Total bytes transferred
- **Use Case**: Monitor bandwidth usage

### AI Agent Metrics

#### `portal_ai_agent_requests_total`
- **Type**: Counter
- **Labels**: `agent_type`, `status`
- **Description**: Total AI agent requests
- **Use Case**: Track AI agent usage by type

#### `portal_ai_agent_latency_seconds`
- **Type**: Histogram
- **Labels**: `agent_type`
- **Buckets**: 0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30, 60
- **Description**: AI agent request latency
- **Use Case**: Monitor AI agent performance

#### `portal_ai_agent_errors_total`
- **Type**: Counter
- **Labels**: `agent_type`, `error_type`
- **Description**: Total AI agent errors
- **Use Case**: Track AI agent failures

### Rate Limiting Metrics

#### `portal_rate_limit_hits_total`
- **Type**: Counter
- **Labels**: `limit_type`
- **Description**: Total rate limit hits
- **Use Case**: Monitor rate limiting effectiveness

### Quota Metrics

#### `portal_quota_exceeded_total`
- **Type**: Counter
- **Labels**: `quota_type`, `key_id`
- **Description**: Total quota exceeded events
- **Use Case**: Track quota violations

## Grafana Dashboard

### Importing the Dashboard

1. Open Grafana UI
2. Navigate to **Dashboards** → **Import**
3. Upload `monitoring/grafana-dashboard.json`
4. Select your Prometheus datasource
5. Click **Import**

### Dashboard Panels

#### Overview Section
- **Request Rate**: Shows requests per second by method and endpoint
- **Error Rate**: Displays 5xx error percentage
- **Request Latency**: Shows P50, P95, P99 latency percentiles

#### AI Agent Metrics Section
- **AI Agent Requests by Type**: Stacked requests by agent type
- **AI Agent Latency (P95)**: P95 latency by agent type
- **AI Agent Error Rate**: Overall error rate gauge

#### Resource Usage Section
- **Active Connections**: Current connection count
- **Active Leases**: Current lease count
- **Bandwidth Usage**: Bytes transferred per second

#### Rate Limiting & Quotas Section
- **Rate Limit Hits**: Rate limit violations by type
- **Quota Exceeded Events**: Quota violations by API key

### Dashboard Variables

- **DS_PROMETHEUS**: Prometheus datasource selector
- Auto-refresh: 10 seconds
- Time range: Last 1 hour (default)

## Alerts

### Alert Configuration

Load alert rules into Prometheus:

```yaml
# prometheus.yml
rule_files:
  - "alert-rules.yaml"
```

### Alert Severities

- **Critical**: Immediate action required (pages on-call)
- **Warning**: Attention needed within hours
- **Info**: Informational, no immediate action

### Active Alerts

#### Critical Alerts
- **HighErrorRate**: Error rate > 5% for 5 minutes
- **VeryHighLatency**: P95 latency > 10s for 5 minutes
- **CriticalConnectionSpike**: > 5000 connections for 2 minutes
- **NoActiveConnections**: No connections for 10 minutes
- **GatewayDown**: Gateway unreachable for 1 minute

#### Warning Alerts
- **HighLatency**: P95 latency > 5s for 10 minutes
- **ConnectionSpike**: > 1000 connections for 5 minutes
- **QuotaExceeded**: Quota violations > 1/s for 5 minutes
- **AIAgentHighErrorRate**: AI agent error rate > 10% for 5 minutes
- **AIAgentHighLatency**: AI agent P95 latency > 10s for 10 minutes
- **HighMemoryUsage**: Memory > 2GB for 10 minutes

#### Info Alerts
- **RateLimitHitSpike**: Rate limit hits > 10/s for 5 minutes
- **HighRequestRate**: Request rate > 10,000/s for 5 minutes

## Setup Instructions

### Prerequisites

- Docker and Docker Compose
- Portal Gateway running on port 8080

### Quick Start with Docker Compose

Create `docker-compose.monitoring.yml`:

```yaml
version: '3.8'

services:
  prometheus:
    image: prom/prometheus:latest
    ports:
      - "9090:9090"
    volumes:
      - ./monitoring/alert-rules.yaml:/etc/prometheus/alert-rules.yaml
      - ./monitoring/prometheus.yml:/etc/prometheus/prometheus.yml
      - prometheus-data:/prometheus
    command:
      - '--config.file=/etc/prometheus/prometheus.yml'
      - '--storage.tsdb.path=/prometheus'
    networks:
      - monitoring

  grafana:
    image: grafana/grafana:latest
    ports:
      - "3000:3000"
    environment:
      - GF_SECURITY_ADMIN_PASSWORD=admin
      - GF_USERS_ALLOW_SIGN_UP=false
    volumes:
      - grafana-data:/var/lib/grafana
      - ./monitoring/grafana-dashboard.json:/etc/grafana/provisioning/dashboards/portal-gateway.json
    networks:
      - monitoring

  alertmanager:
    image: prom/alertmanager:latest
    ports:
      - "9093:9093"
    volumes:
      - ./monitoring/alertmanager.yml:/etc/alertmanager/alertmanager.yml
    networks:
      - monitoring

volumes:
  prometheus-data:
  grafana-data:

networks:
  monitoring:
```

Create `monitoring/prometheus.yml`:

```yaml
global:
  scrape_interval: 15s
  evaluation_interval: 15s

rule_files:
  - /etc/prometheus/alert-rules.yaml

alerting:
  alertmanagers:
    - static_configs:
        - targets: ['alertmanager:9093']

scrape_configs:
  - job_name: 'portal-gateway'
    static_configs:
      - targets: ['host.docker.internal:8080']
    metrics_path: '/metrics'
```

Start monitoring stack:

```bash
docker-compose -f docker-compose.monitoring.yml up -d
```

Access:
- Prometheus: http://localhost:9090
- Grafana: http://localhost:3000 (admin/admin)
- Alertmanager: http://localhost:9093

### Kubernetes Setup

Create `monitoring-namespace.yaml`:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: monitoring
```

Create `prometheus-config.yaml`:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: prometheus-config
  namespace: monitoring
data:
  prometheus.yml: |
    global:
      scrape_interval: 15s
    scrape_configs:
      - job_name: 'portal-gateway'
        kubernetes_sd_configs:
          - role: pod
            namespaces:
              names:
                - default
        relabel_configs:
          - source_labels: [__meta_kubernetes_pod_label_app]
            action: keep
            regex: portal-gateway
```

Deploy Prometheus and Grafana using Helm:

```bash
# Add Helm repositories
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo add grafana https://grafana.github.io/helm-charts
helm repo update

# Install Prometheus
helm install prometheus prometheus-community/prometheus \
  --namespace monitoring \
  --create-namespace \
  --set alertmanager.enabled=true \
  --set server.service.type=LoadBalancer

# Install Grafana
helm install grafana grafana/grafana \
  --namespace monitoring \
  --set service.type=LoadBalancer \
  --set adminPassword=admin
```

## Runbooks

### High Error Rate

**Alert**: `HighErrorRate`

**Symptoms**:
- Error rate > 5%
- 5xx responses in logs

**Investigation**:
1. Check recent deployments: `git log --since="1 hour ago"`
2. Review error logs: `kubectl logs -l app=portal-gateway --tail=100 | grep ERROR`
3. Check downstream service health
4. Review recent configuration changes

**Resolution**:
1. If deployment related: rollback to previous version
2. If resource exhaustion: scale up pods/instances
3. If downstream issue: enable circuit breaker or use fallback
4. If configuration issue: revert config changes

**Prevention**:
- Implement gradual rollouts (canary/blue-green)
- Add integration tests for critical paths
- Set up dependency health checks

### High Latency

**Alert**: `HighLatency` / `VeryHighLatency`

**Symptoms**:
- P95 latency > 5s (warning) or > 10s (critical)
- Slow response times

**Investigation**:
1. Check database query performance
2. Review AI agent latency: check `portal_ai_agent_latency_seconds`
3. Check resource utilization (CPU, memory)
4. Review rate limiting metrics

**Resolution**:
1. If database slow: optimize queries, add indexes
2. If AI agent slow: check downstream AI service
3. If resource constrained: scale up resources
4. If rate limited: adjust rate limits

**Prevention**:
- Add database query optimization
- Implement caching for frequent requests
- Set up auto-scaling policies

### Connection Spike

**Alert**: `ConnectionSpike` / `CriticalConnectionSpike`

**Symptoms**:
- Active connections > 1000 (warning) or > 5000 (critical)

**Investigation**:
1. Check for DDoS attack indicators
2. Review request patterns in logs
3. Check if legitimate traffic spike
4. Review rate limiting effectiveness

**Resolution**:
1. If DDoS: enable rate limiting, block malicious IPs
2. If legitimate: scale up instances
3. If connection leak: restart instances, investigate code

**Prevention**:
- Implement connection pooling
- Set connection timeouts
- Add request throttling

### Quota Exceeded

**Alert**: `QuotaExceeded`

**Symptoms**:
- Quota violations > 1/s

**Investigation**:
1. Identify which API keys are hitting limits
2. Check if usage is legitimate
3. Review quota configuration

**Resolution**:
1. Contact customer about usage
2. Adjust quota if appropriate
3. Investigate potential abuse

**Prevention**:
- Set up quota usage notifications
- Implement quota soft limits with warnings
- Add quota usage dashboard for customers

### AI Agent Errors

**Alert**: `AIAgentHighErrorRate`

**Symptoms**:
- AI agent error rate > 10%

**Investigation**:
1. Check which agent type is failing
2. Review agent error logs
3. Check downstream AI service status
4. Verify API keys and credentials

**Resolution**:
1. If service down: enable circuit breaker
2. If auth issue: refresh credentials
3. If rate limited: implement backoff

**Prevention**:
- Add circuit breaker for AI services
- Implement retry with exponential backoff
- Set up health checks for AI services

### Gateway Down

**Alert**: `GatewayDown`

**Symptoms**:
- Gateway unreachable

**Investigation**:
1. Check instance health: `kubectl get pods -l app=portal-gateway`
2. Review pod events: `kubectl describe pod <pod-name>`
3. Check resource availability
4. Review recent changes

**Resolution**:
1. Restart failed pods: `kubectl delete pod <pod-name>`
2. Scale up if needed: `kubectl scale deployment portal-gateway --replicas=3`
3. Check logs for crash reasons
4. Rollback if deployment related

**Prevention**:
- Set up health check probes
- Implement auto-restart policies
- Add monitoring for pod crashes

## Best Practices

### Metric Collection
- Keep label cardinality low (< 10,000 unique combinations)
- Use histogram buckets appropriate for your SLOs
- Avoid high-cardinality labels (user IDs, request IDs)

### Dashboard Usage
- Set appropriate time ranges for investigation
- Use template variables for filtering
- Create alerts from dashboard queries

### Alert Management
- Set appropriate thresholds based on SLOs
- Use `for` duration to reduce noise
- Include runbook links in annotations
- Test alerts regularly

### Performance
- Keep retention period reasonable (default: 15 days)
- Use recording rules for expensive queries
- Implement metric federation for multi-cluster

## Troubleshooting

### Metrics Not Showing Up

**Check**:
1. Gateway `/metrics` endpoint accessible: `curl http://localhost:8080/metrics`
2. Prometheus scraping: Check Prometheus UI → Status → Targets
3. Firewall rules allow scraping
4. Correct job name in Prometheus config

### Dashboard Not Loading

**Check**:
1. Prometheus datasource configured correctly
2. Time range appropriate (not too far in past)
3. Queries have data for selected time range
4. Panel queries are syntactically correct

### Alerts Not Firing

**Check**:
1. Alert rules loaded: Prometheus UI → Status → Rules
2. Alertmanager connected: Prometheus UI → Status → Runtime & Build
3. Alert conditions actually met
4. `for` duration not preventing firing

## Support

For issues or questions:
- GitHub Issues: https://github.com/portal-project/portal-gateway/issues
- Documentation: https://docs.portal-gateway.io
- Community: Discord/Slack (link)

## References

- [Prometheus Documentation](https://prometheus.io/docs/)
- [Grafana Documentation](https://grafana.com/docs/)
- [Prometheus Best Practices](https://prometheus.io/docs/practices/naming/)
- [Alertmanager Configuration](https://prometheus.io/docs/alerting/latest/configuration/)
