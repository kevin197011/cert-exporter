package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v2"
)

// NacosConfigManager Nacos HTTP API 配置管理器
type NacosConfigManager struct {
	httpClient   *http.Client
	config       *Config
	configMutex  sync.RWMutex
	updateChan   chan *Config
	accessToken  string
	tokenExpiry  time.Time
	stopChan     chan struct{}
}

// NewNacosConfigManager 创建基于 HTTP API 的 Nacos 配置管理器
// 参数:
//   - localConfig: 本地配置对象，包含Nacos连接信息
// 返回值:
//   - *NacosConfigManager: 创建的配置管理器实例
//   - error: 如果创建失败则返回错误
// 功能: 创建Nacos配置管理器，初始化HTTP客户端，加载初始配置，启动配置轮询
func NewNacosConfigManager(localConfig *Config) (*NacosConfigManager, error) {
	if !localConfig.IsNacosEnabled() {
		slog.Info("Nacos未启用，跳过Nacos配置管理器创建")
		return nil, nil
	}

	slog.Info("创建Nacos HTTP配置管理器",
		"nacos_url", localConfig.NacosUrl,
		"namespace_id", localConfig.NamespaceId,
		"username", localConfig.Username,
		"data_id", localConfig.DataId,
		"group", localConfig.Group,
		"poll_interval", "10s")

	// 创建 HTTP 客户端
	httpClient := &http.Client{
		Timeout: 15 * time.Second,
	}

	// 为 HTTPS 连接配置 SSL 设置
	if strings.HasPrefix(localConfig.NacosUrl, "https://") {
		slog.Info("检测到HTTPS连接，配置SSL设置", "skip_ssl_verify", localConfig.SkipSSLVerify)

		tlsConfig := &tls.Config{
			InsecureSkipVerify: localConfig.SkipSSLVerify,
		}

		httpClient.Transport = &http.Transport{
			TLSClientConfig: tlsConfig,
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     30 * time.Second,
		}

		if localConfig.SkipSSLVerify {
			slog.Warn("已禁用SSL证书验证，仅适用于开发/测试环境")
		}
	}

	manager := &NacosConfigManager{
		httpClient: httpClient,
		config:     localConfig,
		updateChan: make(chan *Config, 1),
		stopChan:   make(chan struct{}),
	}

	// 初始加载配置
	if err := manager.loadConfig(); err != nil {
		slog.Warn("初始配置加载失败，将使用本地配置", "error", err)
	}

	// 启动配置轮询
	go manager.startPolling()

	return manager, nil
}

// loadConfig 通过 HTTP API 加载配置
// 返回值:
//   - error: 如果加载失败则返回错误
// 功能: 从Nacos获取配置，解析YAML格式，检查配置变化并通知更新
func (m *NacosConfigManager) loadConfig() error {
	// 确保有有效的访问令牌
	if err := m.ensureValidToken(); err != nil {
		return fmt.Errorf("获取访问令牌失败: %w", err)
	}

	// 获取配置
	content, err := m.getConfig()
	if err != nil {
		return fmt.Errorf("获取配置失败: %w", err)
	}

	// 解析配置
	var nacosConfig Config
	if err := yaml.Unmarshal([]byte(content), &nacosConfig); err != nil {
		return fmt.Errorf("解析配置失败: %w", err)
	}

	// 保留原始的Nacos连接配置
	nacosConfig.NacosUrl = m.config.NacosUrl
	nacosConfig.Username = m.config.Username
	nacosConfig.Password = m.config.Password
	nacosConfig.NamespaceId = m.config.NamespaceId
	nacosConfig.DataId = m.config.DataId
	nacosConfig.Group = m.config.Group

	// 应用默认值
	applyDefaults(&nacosConfig)

	// 更新配置
	m.configMutex.Lock()
	oldConfig := m.config
	m.config = &nacosConfig
	m.configMutex.Unlock()

	// 检查配置是否有变化
	configChanged := oldConfig == nil ||
		!equalStringSlices(oldConfig.Domains, nacosConfig.Domains) ||
		oldConfig.CheckInterval != nacosConfig.CheckInterval ||
		oldConfig.Timeout != nacosConfig.Timeout ||
		oldConfig.LogLevel != nacosConfig.LogLevel

	if configChanged {
		slog.Info("Nacos配置已更新",
			"domain_count", len(nacosConfig.Domains),
			"check_interval", nacosConfig.CheckInterval,
			"timeout", nacosConfig.Timeout,
			"log_level", nacosConfig.LogLevel)

		// 通知配置更新
		select {
		case m.updateChan <- &nacosConfig:
			slog.Debug("已发送配置更新通知")
		default:
			slog.Debug("配置更新通道已满，跳过通知")
		}
	} else {
		slog.Debug("Nacos配置已重新加载，但未检测到变化")
	}

	return nil
}

// startPolling 启动配置轮询（每10秒检查一次）
// 功能: 每10秒轮询一次Nacos配置，检测配置变化并通知更新
func (m *NacosConfigManager) startPolling() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	slog.Info("启动Nacos配置轮询", "interval", "10s")

	for {
		select {
		case <-ticker.C:
			if err := m.loadConfig(); err != nil {
				slog.Debug("配置轮询失败", "error", err)
			}
		case <-m.stopChan:
			slog.Info("停止Nacos配置轮询")
			return
		}
	}
}

// ensureValidToken 确保有有效的访问令牌
// 返回值:
//   - error: 如果获取令牌失败则返回错误
// 功能: 检查访问令牌是否过期，如果过期或即将过期（30秒内）则刷新令牌
func (m *NacosConfigManager) ensureValidToken() error {
	// 检查令牌是否过期（提前30秒刷新）
	if time.Now().Add(30*time.Second).After(m.tokenExpiry) {
		return m.refreshToken()
	}
	return nil
}

// refreshToken 刷新访问令牌
// 返回值:
//   - error: 如果刷新失败则返回错误
// 功能: 通过Nacos登录API获取新的访问令牌，更新令牌和过期时间
func (m *NacosConfigManager) refreshToken() error {
	loginURL := fmt.Sprintf("%s/nacos/v1/auth/login", m.config.NacosUrl)
	// 使用 url.Values 正确编码表单数据，避免特殊字符导致的问题
	formData := url.Values{}
	formData.Set("username", m.config.Username)
	formData.Set("password", m.config.Password)

	resp, err := m.httpClient.Post(loginURL, "application/x-www-form-urlencoded", strings.NewReader(formData.Encode()))
	if err != nil {
		return fmt.Errorf("登录请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取登录响应失败: %w", err)
	}

	var loginResp map[string]interface{}
	if err := json.Unmarshal(body, &loginResp); err != nil {
		return fmt.Errorf("解析登录响应失败: %w", err)
	}

	accessToken, ok := loginResp["accessToken"].(string)
	if !ok {
		return fmt.Errorf("获取访问令牌失败: %s", string(body))
	}

	// 获取令牌过期时间（默认18000秒）
	tokenTtl := 18000.0
	if ttl, ok := loginResp["tokenTtl"].(float64); ok {
		tokenTtl = ttl
	}

	m.accessToken = accessToken
	m.tokenExpiry = time.Now().Add(time.Duration(tokenTtl) * time.Second)

	slog.Debug("访问令牌已刷新", "expires_in", tokenTtl)
	return nil
}

// getConfig 获取配置内容
// 返回值:
//   - string: 配置内容的YAML字符串
//   - error: 如果获取失败则返回错误
// 功能: 通过Nacos HTTP API获取配置内容
func (m *NacosConfigManager) getConfig() (string, error) {
	configURL := fmt.Sprintf("%s/nacos/v1/cs/configs?dataId=%s&group=%s&tenant=%s&accessToken=%s",
		m.config.NacosUrl, m.config.DataId, m.config.Group, m.config.NamespaceId, m.accessToken)

	resp, err := m.httpClient.Get(configURL)
	if err != nil {
		return "", fmt.Errorf("获取配置请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取配置响应失败: %w", err)
	}

	content := string(body)
	if content == "" || content == "config data not exist" {
		return "", fmt.Errorf("配置不存在或为空")
	}

	return content, nil
}

// GetConfig 获取当前配置
// 返回值:
//   - *Config: 当前配置对象（线程安全）
// 功能: 以线程安全的方式获取当前缓存的配置
func (m *NacosConfigManager) GetConfig() *Config {
	if m == nil {
		return nil
	}

	m.configMutex.RLock()
	defer m.configMutex.RUnlock()
	return m.config
}

// GetUpdateChannel 获取配置更新通道
// 返回值:
//   - <-chan *Config: 配置更新通知通道
// 功能: 返回配置更新通知通道，当配置变化时会发送新的配置对象
func (m *NacosConfigManager) GetUpdateChannel() <-chan *Config {
	if m == nil {
		return nil
	}
	return m.updateChan
}

// Close 关闭Nacos配置管理器
// 功能: 停止配置轮询，关闭所有通道，释放资源
func (m *NacosConfigManager) Close() {
	if m != nil {
		close(m.stopChan)
		close(m.updateChan)
		slog.Info("Nacos配置管理器已关闭")
	}
}

