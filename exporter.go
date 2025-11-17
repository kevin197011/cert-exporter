package main

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// CertExporter Prometheus exporter结构
type CertExporter struct {
	config      *Config
	mutex       sync.RWMutex
	nacosManager *NacosConfigManager
	stopChan    chan struct{}
	triggerChan chan struct{} // 用于触发立即检查
	initialCheckDone bool      // 标记是否已完成初始检查

	// Prometheus指标
	certExpiryDays *prometheus.GaugeVec
	certExpiryTime *prometheus.GaugeVec
	certCheckTime  *prometheus.GaugeVec
	certStatus     *prometheus.GaugeVec
}

// NewCertExporter 创建新的exporter
// 参数:
//   - localConfig: 本地配置对象，包含Nacos连接信息和业务配置
// 返回值:
//   - *CertExporter: 创建的exporter实例
//   - error: 如果创建失败则返回错误
// 功能: 根据配置创建CertExporter实例，如果启用了Nacos则优先从Nacos获取配置
func NewCertExporter(localConfig *Config) (*CertExporter, error) {
	var finalConfig *Config
	var nacosManager *NacosConfigManager

	// 如果启用了Nacos，优先尝试从Nacos获取配置
	if localConfig.IsNacosEnabled() {
		var err error
		nacosManager, err = NewNacosConfigManager(localConfig)
		if err != nil {
			slog.Warn("创建Nacos配置管理器失败，使用本地配置", "error", err)
			finalConfig = localConfig
		} else {
			// 尝试从Nacos获取配置
			if nacosConfig := nacosManager.GetConfig(); nacosConfig != nil {
				finalConfig = nacosConfig
				slog.Info("使用Nacos配置", "domain_count", len(nacosConfig.Domains))
			} else {
				slog.Info("Nacos配置为空，使用本地配置")
				finalConfig = localConfig
			}
		}
	} else {
		slog.Info("Nacos未启用，使用本地配置")
		finalConfig = localConfig
	}

	exporter := &CertExporter{
		config:       finalConfig,
		nacosManager: nacosManager,
		stopChan:     make(chan struct{}),
		triggerChan:  make(chan struct{}, 1), // 缓冲通道，避免阻塞
		certExpiryDays: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "cert_expiry_days",
				Help: "SSL证书距离过期的天数 (-999表示检测失败)",
			},
			[]string{"domain"},
		),
		certExpiryTime: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "cert_expiry_timestamp",
				Help: "SSL证书过期时间戳",
			},
			[]string{"domain"},
		),
		certCheckTime: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "cert_check_timestamp",
				Help: "SSL证书最后检查时间戳",
			},
			[]string{"domain"},
		),
		certStatus: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "cert_check_status",
				Help: "SSL证书检查状态 (1=成功, 0=失败)",
			},
			[]string{"domain"},
		),
	}

	// 启动配置监听
	if nacosManager != nil {
		go exporter.watchConfigUpdates()
	}

	return exporter, nil
}

// Describe 实现Prometheus Collector接口
// 参数:
//   - ch: Prometheus描述符通道
// 功能: 向Prometheus注册所有指标的描述符
func (e *CertExporter) Describe(ch chan<- *prometheus.Desc) {
	e.certExpiryDays.Describe(ch)
	e.certExpiryTime.Describe(ch)
	e.certCheckTime.Describe(ch)
	e.certStatus.Describe(ch)
}

// Collect 实现Prometheus Collector接口
// 参数:
//   - ch: Prometheus指标通道
// 功能: 收集并发送所有Prometheus指标
func (e *CertExporter) Collect(ch chan<- prometheus.Metric) {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	e.certExpiryDays.Collect(ch)
	e.certExpiryTime.Collect(ch)
	e.certCheckTime.Collect(ch)
	e.certStatus.Collect(ch)
}

// StartMonitoring 启动后台监控
// 功能: 启动定时监控任务，每天凌晨3点执行证书检查，同时监听配置更新事件
// 说明: 启动时会立即执行一次检查，之后每天凌晨3点定时执行，配置更新时会立即触发检查
func (e *CertExporter) StartMonitoring() {
	// 立即执行一次检查
	e.checkAllCerts()
	e.initialCheckDone = true

	// 计算到下一个凌晨3点的时间
	nextCheckTime := e.calculateNext3AM()
	slog.Info("启动定时监控", "next_check_time", nextCheckTime.Format("2006-01-02 15:04:05"))

	// 创建定时器，等待到下一个凌晨3点
	timer := time.NewTimer(time.Until(nextCheckTime))
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			slog.Info("定时器触发（每天凌晨3点），开始检查SSL证书")
			e.checkAllCerts()

			// 计算下一个凌晨3点的时间
			nextCheckTime = e.calculateNext3AM()
			slog.Info("下次检查时间", "next_check_time", nextCheckTime.Format("2006-01-02 15:04:05"))
			timer.Reset(time.Until(nextCheckTime))

		case <-e.triggerChan:
			slog.Info("收到配置变更触发信号，立即执行SSL证书检查")
			e.checkAllCerts()

			// 配置更新后，重新计算下一个凌晨3点的时间
			nextCheckTime = e.calculateNext3AM()
			timer.Reset(time.Until(nextCheckTime))

		case <-e.stopChan:
			slog.Info("停止定时监控")
			return
		}
	}
}

// calculateNext3AM 计算下一个凌晨3点的时间
// 返回值:
//   - time.Time: 下一个凌晨3点的时间点
// 功能: 根据当前时间计算下一个凌晨3点的执行时间，如果已过今天3点则返回明天3点
func (e *CertExporter) calculateNext3AM() time.Time {
	now := time.Now()
	// 今天的凌晨3点
	today3AM := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, now.Location())

	// 如果现在已经过了今天的3点，则返回明天的3点
	if now.After(today3AM) || now.Equal(today3AM) {
		return today3AM.Add(24 * time.Hour)
	}

	// 如果还没到今天的3点，返回今天的3点
	return today3AM
}

// watchConfigUpdates 监听配置更新
// 功能: 监听Nacos配置更新，当检测到配置变化时更新本地配置并触发证书检查
// 说明: 仅在启用Nacos时运行，避免启动时的重复检查
func (e *CertExporter) watchConfigUpdates() {
	if e.nacosManager == nil {
		return
	}

	updateChan := e.nacosManager.GetUpdateChannel()
	for {
		select {
		case newConfig := <-updateChan:
			if newConfig != nil {
				e.mutex.Lock()
				oldConfig := *e.config // 复制旧配置
				e.config = newConfig
				initialCheckDone := e.initialCheckDone
				e.mutex.Unlock()

				// 详细记录所有配置变化
				e.logConfigChanges(&oldConfig, newConfig)

				// 只有在初始检查完成后才触发配置变更检查，避免启动时重复检查
				if initialCheckDone {
					select {
					case e.triggerChan <- struct{}{}:
						slog.Info("已发送配置变更触发信号")
					default:
						slog.Warn("触发通道已满，跳过此次触发信号")
					}
				} else {
					slog.Debug("跳过启动时的配置变更触发，避免重复检查")
				}
			}
		case <-e.stopChan:
			return
		}
	}
}



// getCurrentConfig 获取当前配置
// 返回值:
//   - *Config: 当前配置对象的副本（线程安全）
// 功能: 以线程安全的方式获取当前配置
func (e *CertExporter) getCurrentConfig() *Config {
	e.mutex.RLock()
	defer e.mutex.RUnlock()
	return e.config
}

// Stop 停止监控
// 功能: 停止所有监控任务，关闭Nacos配置管理器，释放资源
func (e *CertExporter) Stop() {
	close(e.stopChan)
	if e.nacosManager != nil {
		e.nacosManager.Close()
	}
}

// TriggerCheck 手动触发检查（用于外部调用）
// 功能: 通过HTTP接口手动触发一次证书检查，如果检查正在进行中则跳过
func (e *CertExporter) TriggerCheck() {
	select {
	case e.triggerChan <- struct{}{}:
		slog.Info("手动触发SSL证书检查")
	default:
		slog.Info("检查已在进行中，跳过手动触发")
	}
}

// checkAllCerts 检查所有SSL证书（并发执行）
// 功能: 并发检查所有配置的域名SSL证书，使用信号量限制最大并发数为100
// 说明: 统计成功和失败数量，记录总耗时
func (e *CertExporter) checkAllCerts() {
	currentConfig := e.getCurrentConfig()
	domainCount := len(currentConfig.Domains)

	if domainCount == 0 {
		slog.Warn("域名列表为空，跳过检查")
		return
	}

	startTime := time.Now()

	// 使用 WaitGroup 等待所有检查完成
	var wg sync.WaitGroup

	// 使用带缓冲的通道限制并发数，避免同时发起过多连接
	// 最大并发数设置为 100，可以根据需要调整
	maxConcurrent := 100
	if domainCount < maxConcurrent {
		maxConcurrent = domainCount
	}

	slog.Info("开始并发检查SSL证书",
		"domain_count", domainCount,
		"max_concurrent", maxConcurrent,
		"timeout", currentConfig.Timeout)

	semaphore := make(chan struct{}, maxConcurrent)

	// 统计成功和失败数量
	var successCount, failCount int
	var mu sync.Mutex

	// 并发检查每个域名的SSL证书
	for i, domain := range currentConfig.Domains {
		wg.Add(1)

		go func(index int, d string) {
			defer wg.Done()

			// 获取信号量
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			slog.Debug("开始检查", "progress", fmt.Sprintf("%d/%d", index+1, domainCount), "domain", d)
			success := e.checkCert(d)

			mu.Lock()
			if success {
				successCount++
			} else {
				failCount++
			}
			mu.Unlock()
		}(i, domain)
	}

	// 等待所有检查完成
	wg.Wait()

	duration := time.Since(startTime)
	slog.Info("所有SSL证书检查完成",
		"domain_count", domainCount,
		"success", successCount,
		"failed", failCount,
		"duration_seconds", duration.Seconds())
}

// checkCert 检查单个域名的SSL证书，返回是否成功
// 参数:
//   - domain: 要检查的域名
// 返回值:
//   - bool: 检查是否成功，true表示成功，false表示失败
// 功能: 检查指定域名的SSL证书过期时间，更新Prometheus指标，根据剩余天数记录不同级别的日志
func (e *CertExporter) checkCert(domain string) bool {
	slog.Debug("开始检查SSL证书", "domain", domain)

	// 记录检查时间
	now := time.Now()
	e.certCheckTime.WithLabelValues(domain).Set(float64(now.Unix()))

	// 获取当前配置
	currentConfig := e.getCurrentConfig()

	// 获取SSL证书信息（带超时和重试）
	timeout := time.Duration(currentConfig.Timeout) * time.Second
	certInfo, err := GetCertInfoWithFallback(domain, timeout, currentConfig)
	if err != nil {
		// 根据错误类型决定日志级别
		errMsg := err.Error()
		if strings.Contains(errMsg, "timeout") ||
		   strings.Contains(errMsg, "i/o timeout") ||
		   strings.Contains(errMsg, "connection refused") ||
		   strings.Contains(errMsg, "no such host") ||
		   strings.Contains(errMsg, "server misbehaving") {
			// 网络问题使用 WARN 级别
			slog.Warn("SSL证书检查失败（网络问题）", "domain", domain, "error", err)
		} else {
			// 其他错误使用 ERROR 级别
			slog.Error("SSL证书检查失败", "domain", domain, "error", err)
		}
		e.certStatus.WithLabelValues(domain).Set(0)
		// 设置失败标记：-999天表示检测失败
		e.certExpiryDays.WithLabelValues(domain).Set(-999)
		// 设置过期时间戳为0表示未知
		e.certExpiryTime.WithLabelValues(domain).Set(0)
		return false
	}

	// 设置成功状态
	e.certStatus.WithLabelValues(domain).Set(1)

	// 计算剩余天数（取整数）
	daysUntilExpiry := time.Until(certInfo.ExpiryDate).Hours() / 24
	daysUntilExpiryInt := float64(int(daysUntilExpiry))
	e.certExpiryDays.WithLabelValues(domain).Set(daysUntilExpiryInt)

	// 设置过期时间戳
	e.certExpiryTime.WithLabelValues(domain).Set(float64(certInfo.ExpiryDate.Unix()))

	// 根据剩余天数决定日志级别
	days := int(daysUntilExpiryInt)
	if days < 0 {
		slog.Error("SSL证书已过期",
			"domain", domain,
			"expired_days", -days,
			"expiry_date", certInfo.ExpiryDate.Format("2006-01-02"))
	} else if days < 7 {
		slog.Warn("SSL证书即将过期",
			"domain", domain,
			"days_until_expiry", days,
			"expiry_date", certInfo.ExpiryDate.Format("2006-01-02"))
	} else if days < 30 {
		slog.Info("SSL证书检查完成（即将过期）",
			"domain", domain,
			"days_until_expiry", days,
			"expiry_date", certInfo.ExpiryDate.Format("2006-01-02"))
	} else {
		slog.Debug("SSL证书检查完成",
			"domain", domain,
			"days_until_expiry", days,
			"expiry_date", certInfo.ExpiryDate.Format("2006-01-02"),
			"issuer", certInfo.Issuer)
	}

	return true
}

// logConfigChanges 记录配置变化的详细信息
// 参数:
//   - oldConfig: 旧配置对象
//   - newConfig: 新配置对象
// 功能: 比较新旧配置的差异，记录所有变化的配置项，特别提醒重要配置的变化
func (e *CertExporter) logConfigChanges(oldConfig, newConfig *Config) {
	changes := make(map[string]interface{})

	// 检查域名列表变化
	if !equalStringSlices(oldConfig.Domains, newConfig.Domains) {
		changes["domains"] = map[string]interface{}{
			"old": oldConfig.Domains,
			"new": newConfig.Domains,
		}
	}

	// 检查端口变化
	if oldConfig.Port != newConfig.Port {
		changes["port"] = map[string]interface{}{
			"old": oldConfig.Port,
			"new": newConfig.Port,
		}
	}

	// 检查日志级别变化
	if oldConfig.LogLevel != newConfig.LogLevel {
		changes["log_level"] = map[string]interface{}{
			"old": oldConfig.LogLevel,
			"new": newConfig.LogLevel,
		}
	}

	// 检查超时时间变化
	if oldConfig.Timeout != newConfig.Timeout {
		changes["timeout"] = map[string]interface{}{
			"old": oldConfig.Timeout,
			"new": newConfig.Timeout,
		}
	}


	// 记录变化
	if len(changes) > 0 {
		slog.Info("检测到配置参数变化", "changes", changes)

		// 特别提醒重要变化
		if _, exists := changes["domains"]; exists {
			slog.Info("域名列表已更新，立即触发检查")
		}

		if _, exists := changes["timeout"]; exists {
			slog.Info("超时时间已更新，将在下次检查时生效")
		}
	} else {
		slog.Debug("配置已重新加载，但未检测到参数变化")
	}
}

// equalStringSlices 比较两个字符串切片是否相等
// 参数:
//   - a: 第一个字符串切片
//   - b: 第二个字符串切片
// 返回值:
//   - bool: 如果两个切片长度和内容完全相同则返回true，否则返回false
// 功能: 深度比较两个字符串切片是否相等
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}