# Domain Exporter 部署指南

## 环境支持

Domain Exporter 支持多种部署环境：

- 🐳 **Docker Compose** - 本地开发和测试
- ☸️ **Kubernetes** - 生产环境
- 🔧 **二进制文件** - 直接运行

## 配置说明

### Nacos 配置

应用支持两种 Nacos 连接方式：

#### HTTP 连接（本地开发）
```bash
NACOS_URL=http://192.168.1.11:8848
NACOS_USERNAME=nacos
NACOS_PASSWORD=nacos
NACOS_NAMESPACE_ID=devops
NACOS_DATA_ID=domain-exporter
NACOS_GROUP=DEFAULT_GROUP
NACOS_SKIP_SSL_VERIFY=true
```

#### HTTPS 连接（生产环境）
```bash
NACOS_URL=https://infra-nacos.slileisure.com:443
NACOS_USERNAME=nacos
NACOS_PASSWORD=nacos
NACOS_NAMESPACE_ID=devops
NACOS_DATA_ID=domain-exporter
NACOS_GROUP=DEFAULT_GROUP
NACOS_SKIP_SSL_VERIFY=true  # 仅测试环境
```

### Nacos 配置文件内容

在 Nacos 控制台中创建配置文件，内容如下：

```yaml
# 监控间隔（秒）
check_interval: 3600

# HTTP服务端口
port: 8080

# 日志级别
log_level: info

# 请求超时时间（秒）
timeout: 5

# 域名列表
domains:
  - example.com
  - google.com
  - github.com
  - qq.com
  - baidu.com
```

## 部署方式

### 1. Docker Compose 部署

适用于本地开发和测试：

```bash
# 克隆项目
git clone <repository>
cd domain-exporter

# 配置环境变量
cp .env.example .env
# 编辑 .env 文件

# 启动服务
docker-compose up -d

# 查看日志
docker-compose logs -f

# 访问 metrics
curl http://localhost:8080/metrics
```

### 2. Kubernetes 部署

适用于生产环境：

```bash
# 进入 k8s 目录
cd k8s

# 修改配置（如果需要）
vim deployment.yaml

# 部署到集群
kubectl apply -f deployment.yaml

# 检查状态
kubectl get pods -n monitoring -l app=domain-exporter

# 查看日志
kubectl logs -n monitoring -l app=domain-exporter -f

# 端口转发测试
kubectl port-forward -n monitoring svc/domain-exporter 8080:8080
```

### 3. 二进制文件部署

适用于简单环境：

```bash
# 构建二进制文件
go build -o domain-exporter .

# 设置环境变量
export NACOS_URL=http://localhost:8848
export NACOS_USERNAME=nacos
export NACOS_PASSWORD=nacos
# ... 其他环境变量

# 运行
./domain-exporter
```

## 故障排查

### 1. Nacos 连接问题

使用检查脚本诊断：

```bash
# 设置环境变量
export NACOS_URL=https://your-nacos-server:443
export NACOS_USERNAME=nacos
export NACOS_PASSWORD=nacos
export NACOS_NAMESPACE_ID=devops
export NACOS_DATA_ID=domain-exporter
export NACOS_GROUP=DEFAULT_GROUP

# 运行检查脚本
bash scripts/check-nacos.sh
```

### 2. 常见问题

#### 问题：配置加载失败
```
ERROR msg="Nacos GetConfig 调用失败" error="read config from both server and cache fail"
```

**解决方案：**
1. 检查 Nacos 服务器是否可访问
2. 确认配置文件是否存在于正确的命名空间和组中
3. 验证用户名和密码是否正确
4. 对于 HTTPS 连接，确认 SSL 配置

#### 问题：SSL 证书验证失败

**解决方案：**
```bash
# 临时跳过 SSL 验证（仅测试环境）
export NACOS_SKIP_SSL_VERIFY=true
```

#### 问题：域名检查失败

**解决方案：**
1. 检查网络连接
2. 确认域名拼写正确
3. 检查防火墙设置
4. 增加超时时间

### 3. 监控和告警

#### Prometheus 配置

```yaml
scrape_configs:
  - job_name: 'domain-exporter'
    static_configs:
      - targets: ['domain-exporter:8080']
    scrape_interval: 60s
```

#### Grafana 仪表板

主要指标：
- `domain_check_status` - 域名检查状态
- `domain_expiry_days` - 域名过期天数
- `domain_check_timestamp` - 最后检查时间

#### 告警规则

```yaml
groups:
  - name: domain-expiry
    rules:
      - alert: DomainExpiringSoon
        expr: domain_expiry_days < 30
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "域名即将过期"
          description: "域名 {{ $labels.domain }} 将在 {{ $value }} 天后过期"
      
      - alert: DomainCheckFailed
        expr: domain_check_status == 0
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "域名检查失败"
          description: "域名 {{ $labels.domain }} 检查失败"
```

## 性能优化

### 1. 资源配置

#### K8s 资源限制
```yaml
resources:
  requests:
    memory: "64Mi"
    cpu: "50m"
  limits:
    memory: "128Mi"
    cpu: "100m"
```

### 2. 配置优化

- 合理设置检查间隔（推荐 3600 秒）
- 调整超时时间（推荐 5-30 秒）
- 限制并发域名检查数量

### 3. 网络优化

- 使用 HTTP/2 连接池
- 启用 Keep-Alive
- 配置合适的超时时间

## 安全建议

1. **生产环境启用 SSL 证书验证**
2. **使用 Secret 管理敏感信息**
3. **限制网络访问权限**
4. **定期更新依赖和镜像**
5. **启用只读文件系统**
6. **使用非 root 用户运行**