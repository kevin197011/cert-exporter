package main

import (
	"log/slog"
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
func (e *CertExporter) Describe(ch chan<- *prometheus.Desc) {
	e.certExpiryDays.Describe(ch)
	e.certExpiryTime.Describe(ch)
	e.certCheckTime.Describe(ch)
	e.certStatus.Describe(ch)
}

// Collect 实现Prometheus Collector接口
func (e *CertExporter) Collect(ch chan<- prometheus.Metric) {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	e.certExpiryDays.Collect(ch)
	e.certExpiryTime.Collect(ch)
	e.certCheckTime.Collect(ch)
	e.certStatus.Collect(ch)
}

// StartMonitoring 启动后台监控
func (e *CertExporter) StartMonitoring() {
	// 立即执行一次检查
	e.checkAllCerts()
	e.initialCheckDone = true

	// 获取初始检查间隔
	currentInterval := time.Duration(e.getCurrentConfig().CheckInterval) * time.Second
	ticker := time.NewTicker(currentInterval)
	defer ticker.Stop()

	slog.Info("启动定时监控", "check_interval_seconds", e.getCurrentConfig().CheckInterval)

	for {
		select {
		case <-ticker.C:
			slog.Debug("定时器触发，开始检查SSL证书")
			e.checkAllCerts()
			
			// 检查配置是否变化，如果变化则重置定时器
			newInterval := time.Duration(e.getCurrentConfig().CheckInterval) * time.Second
			if newInterval != currentInterval {
				slog.Info("检查间隔已更新", 
					"old_interval_seconds", int(currentInterval.Seconds()),
					"new_interval_seconds", int(newInterval.Seconds()))
				currentInterval = newInterval
				ticker.Reset(currentInterval)
			}
			
		case <-e.triggerChan:
			slog.Info("收到配置变更触发信号，立即执行SSL证书检查")
			e.checkAllCerts()
			
			// 重置定时器，使用最新的检查间隔
			newInterval := time.Duration(e.getCurrentConfig().CheckInterval) * time.Second
			if newInterval != currentInterval {
				slog.Info("配置变更后更新检查间隔", 
					"old_interval_seconds", int(currentInterval.Seconds()),
					"new_interval_seconds", int(newInterval.Seconds()))
				currentInterval = newInterval
			}
			ticker.Reset(currentInterval)
			
		case <-e.stopChan:
			slog.Info("停止定时监控")
			return
		}
	}
}

// watchConfigUpdates 监听配置更新
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
func (e *CertExporter) getCurrentConfig() *Config {
	e.mutex.RLock()
	defer e.mutex.RUnlock()
	return e.config
}

// Stop 停止监控
func (e *CertExporter) Stop() {
	close(e.stopChan)
	if e.nacosManager != nil {
		e.nacosManager.Close()
	}
}

// TriggerCheck 手动触发检查（用于外部调用）
func (e *CertExporter) TriggerCheck() {
	select {
	case e.triggerChan <- struct{}{}:
		slog.Info("手动触发SSL证书检查")
	default:
		slog.Info("检查已在进行中，跳过手动触发")
	}
}

// checkAllCerts 检查所有SSL证书（并发执行）
func (e *CertExporter) checkAllCerts() {
	currentConfig := e.getCurrentConfig()
	domainCount := len(currentConfig.Domains)
	
	if domainCount == 0 {
		slog.Warn("域名列表为空，跳过检查")
		return
	}
	
	slog.Info("开始并发检查SSL证书", "domain_count", domainCount)
	
	// 使用 WaitGroup 等待所有检查完成
	var wg sync.WaitGroup
	
	// 使用带缓冲的通道限制并发数，避免同时发起过多连接
	// 最大并发数设置为 10，可以根据需要调整
	maxConcurrent := 10
	if domainCount < maxConcurrent {
		maxConcurrent = domainCount
	}
	semaphore := make(chan struct{}, maxConcurrent)
	
	// 并发检查每个域名的SSL证书
	for i, domain := range currentConfig.Domains {
		wg.Add(1)
		
		go func(index int, d string) {
			defer wg.Done()
			
			// 获取信号量
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			
			slog.Debug("检查进度", "current", index+1, "total", domainCount, "domain", d)
			e.checkCert(d)
		}(i, domain)
	}
	
	// 等待所有检查完成
	wg.Wait()
	
	slog.Info("所有SSL证书检查完成", "domain_count", domainCount)
}

// checkCert 检查单个域名的SSL证书
func (e *CertExporter) checkCert(domain string) {
	slog.Debug("检查SSL证书", "domain", domain)

	// 记录检查时间
	now := time.Now()
	e.certCheckTime.WithLabelValues(domain).Set(float64(now.Unix()))

	// 获取当前配置
	currentConfig := e.getCurrentConfig()

	// 获取SSL证书信息（带超时和重试）
	timeout := time.Duration(currentConfig.Timeout) * time.Second
	certInfo, err := GetCertInfoWithFallback(domain, timeout, currentConfig)
	if err != nil {
		slog.Error("获取SSL证书信息失败", "domain", domain, "error", err)
		e.certStatus.WithLabelValues(domain).Set(0)
		// 设置失败标记：-999天表示检测失败
		e.certExpiryDays.WithLabelValues(domain).Set(-999)
		// 设置过期时间戳为0表示未知
		e.certExpiryTime.WithLabelValues(domain).Set(0)
		return
	}

	// 设置成功状态
	e.certStatus.WithLabelValues(domain).Set(1)

	// 计算剩余天数（取整数）
	daysUntilExpiry := time.Until(certInfo.ExpiryDate).Hours() / 24
	daysUntilExpiryInt := float64(int(daysUntilExpiry))
	e.certExpiryDays.WithLabelValues(domain).Set(daysUntilExpiryInt)

	// 设置过期时间戳
	e.certExpiryTime.WithLabelValues(domain).Set(float64(certInfo.ExpiryDate.Unix()))

	slog.Info("SSL证书检查完成", 
		"domain", domain,
		"days_until_expiry", int(daysUntilExpiryInt),
		"expiry_date", certInfo.ExpiryDate.Format("2006-01-02"),
		"issuer", certInfo.Issuer,
		"subject", certInfo.Subject,
		"method", certInfo.Method)
}

// logConfigChanges 记录配置变化的详细信息
func (e *CertExporter) logConfigChanges(oldConfig, newConfig *Config) {
	changes := make(map[string]interface{})
	
	// 检查域名列表变化
	if !equalStringSlices(oldConfig.Domains, newConfig.Domains) {
		changes["domains"] = map[string]interface{}{
			"old": oldConfig.Domains,
			"new": newConfig.Domains,
		}
	}
	
	// 检查检查间隔变化
	if oldConfig.CheckInterval != newConfig.CheckInterval {
		changes["check_interval"] = map[string]interface{}{
			"old": oldConfig.CheckInterval,
			"new": newConfig.CheckInterval,
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
		if _, exists := changes["check_interval"]; exists {
			slog.Info("检查间隔已更新，将在下次定时器触发时生效")
		}
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