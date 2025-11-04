# KK Domain Exporter Helm Chart

这是一个用于部署域名过期监控 Prometheus Exporter 的 Helm Chart。

## 功能特性

- 支持多副本部署
- 自动扩缩容 (HPA)
- Prometheus ServiceMonitor 集成
- Ingress 支持
- Pod 中断预算 (PDB)
- 健康检查和就绪检查
- 灵活的配置管理

## 安装

### 基本安装

```bash
# 添加 Helm 仓库（如果有的话）
helm repo add domain-exporter https://your-helm-repo.com

# 或者直接从本地安装
helm install domain-exporter ./domain-exporter
```

### 自定义配置安装

```bash
# 使用自定义 values 文件
helm install domain-exporter ./domain-exporter -f ./domain-exporter/values-prod.yaml

# 或者通过命令行设置参数
helm install domain-exporter ./domain-exporter \
  --set config.domains="yourdomain.com,anotherdomain.com" \
  --set config.checkInterval=1800 \
  --set replicaCount=2
```

### 开发环境安装

```bash
helm install domain-exporter-dev ./domain-exporter -f ./domain-exporter/values-dev.yaml
```

## 配置参数

### 基本配置

| 参数 | 描述 | 默认值 |
|------|------|--------|
| `replicaCount` | 副本数量 | `1` |
| `image.repository` | 镜像仓库 | `ghcr.io/kevin197011/domain-exporter` |
| `image.tag` | 镜像标签 | `latest` |
| `image.pullPolicy` | 镜像拉取策略 | `IfNotPresent` |

### 应用配置

| 参数 | 描述 | 默认值 |
|------|------|--------|
| `config.domains` | 监控的域名列表（逗号分隔） | `"example.com,test.com"` |
| `config.checkInterval` | 检查间隔（秒） | `3600` |
| `config.port` | HTTP 服务端口 | `8080` |
| `config.logLevel` | 日志级别 | `"info"` |
| `config.timeout` | WHOIS 查询超时时间（秒） | `30` |

### Nacos 配置

| 参数 | 描述 | 默认值 |
|------|------|--------|
| `nacos.enabled` | 启用 Nacos 配置管理 | `false` |
| `nacos.url` | Nacos 服务器地址 | `"http://nacos:8848"` |
| `nacos.username` | Nacos 用户名 | `"nacos"` |
| `nacos.password` | Nacos 密码 | `"nacos"` |

### 服务配置

| 参数 | 描述 | 默认值 |
|------|------|--------|
| `service.type` | 服务类型 | `ClusterIP` |
| `service.port` | 服务端口 | `8080` |

### Ingress 配置

| 参数 | 描述 | 默认值 |
|------|------|--------|
| `ingress.enabled` | 启用 Ingress | `false` |
| `ingress.className` | Ingress 类名 | `""` |
| `ingress.hosts` | Ingress 主机配置 | `[]` |

### 资源配置

| 参数 | 描述 | 默认值 |
|------|------|--------|
| `resources.limits.cpu` | CPU 限制 | `200m` |
| `resources.limits.memory` | 内存限制 | `256Mi` |
| `resources.requests.cpu` | CPU 请求 | `100m` |
| `resources.requests.memory` | 内存请求 | `128Mi` |

### 自动扩缩容

| 参数 | 描述 | 默认值 |
|------|------|--------|
| `autoscaling.enabled` | 启用 HPA | `false` |
| `autoscaling.minReplicas` | 最小副本数 | `1` |
| `autoscaling.maxReplicas` | 最大副本数 | `3` |
| `autoscaling.targetCPUUtilizationPercentage` | CPU 使用率目标 | `80` |

### Prometheus 监控

| 参数 | 描述 | 默认值 |
|------|------|--------|
| `serviceMonitor.enabled` | 启用 ServiceMonitor | `false` |
| `serviceMonitor.interval` | 抓取间隔 | `30s` |
| `serviceMonitor.scrapeTimeout` | 抓取超时 | `10s` |

## 使用示例

### 1. 基本部署

```bash
helm install domain-exporter ./domain-exporter \
  --set config.domains="yourdomain.com,example.com"
```

### 2. 生产环境部署

```bash
helm install domain-exporter ./domain-exporter \
  --values ./domain-exporter/values-prod.yaml \
  --set config.domains="prod1.com,prod2.com,prod3.com"
```

### 3. 启用 Prometheus 监控

```bash
helm install domain-exporter ./domain-exporter \
  --set serviceMonitor.enabled=true \
  --set serviceMonitor.labels.release=prometheus
```

### 4. 启用 Ingress

```bash
helm install domain-exporter ./domain-exporter \
  --set ingress.enabled=true \
  --set ingress.hosts[0].host=domain-exporter.yourdomain.com \
  --set ingress.hosts[0].paths[0].path=/ \
  --set ingress.hosts[0].paths[0].pathType=Prefix
```

## 升级

```bash
# 升级到新版本
helm upgrade domain-exporter ./domain-exporter

# 升级并修改配置
helm upgrade domain-exporter ./domain-exporter \
  --set config.checkInterval=1800
```

## 卸载

```bash
helm uninstall domain-exporter
```

## 故障排除

### 查看 Pod 状态

```bash
kubectl get pods -l app.kubernetes.io/name=domain-exporter
```

### 查看日志

```bash
kubectl logs -l app.kubernetes.io/name=domain-exporter
```

### 查看服务

```bash
kubectl get svc -l app.kubernetes.io/name=domain-exporter
```

### 端口转发测试

```bash
kubectl port-forward svc/domain-exporter-domain-exporter 8080:8080
curl http://localhost:8080/metrics
```

## 监控集成

### Prometheus 配置

如果没有使用 Prometheus Operator，可以在 Prometheus 配置中添加：

```yaml
scrape_configs:
  - job_name: 'domain-exporter'
    kubernetes_sd_configs:
      - role: endpoints
        namespaces:
          names:
            - default  # 替换为实际的命名空间
    relabel_configs:
      - source_labels: [__meta_kubernetes_service_name]
        action: keep
        regex: domain-exporter-domain-exporter
```

### Grafana 仪表板

可以创建 Grafana 仪表板来可视化域名过期信息：

- 域名过期天数趋势图
- 即将过期的域名列表
- 域名检查状态

### 告警规则

```yaml
groups:
- name: domain_expiry
  rules:
  - alert: DomainExpiringSoon
    expr: domain_expiry_days < 30 and domain_expiry_days > 0
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: "域名即将过期"
      description: "域名 {{ $labels.domain }} 将在 {{ $value }} 天后过期"
```