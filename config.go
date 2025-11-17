package main

import (
	"os"
	"strconv"
	"strings"
	"gopkg.in/yaml.v2"
)

// Config 配置结构
type Config struct {
	// 业务配置（从Nacos获取）
	Domains           []string `yaml:"domains"`
	Port              int      `yaml:"port"`
	LogLevel          string   `yaml:"log_level"`
	Timeout           int      `yaml:"timeout"`

	// Nacos连接配置（从本地配置文件获取）
	NacosUrl          string `yaml:"nacos_url"`
	Username          string `yaml:"username"`
	Password          string `yaml:"password"`
	NamespaceId       string `yaml:"namespace_id"`
	DataId            string `yaml:"data_id"`
	Group             string `yaml:"group"`
	SkipSSLVerify     bool   `yaml:"skip_ssl_verify"`  // 跳过SSL证书验证
}

// LoadConfig 加载配置（优先使用环境变量，然后是配置文件）
// 参数:
//   - filename: 配置文件路径，如果为空则只从环境变量加载
// 返回值:
//   - *Config: 加载的配置对象
//   - error: 如果加载失败则返回错误
// 功能: 按优先级加载配置：环境变量 > 配置文件 > 默认值
func LoadConfig(filename string) (*Config, error) {
	var config Config

	// 首先尝试从环境变量加载
	loadFromEnv(&config)

	// 如果配置文件存在，则加载并合并（环境变量优先）
	if filename != "" {
		if data, err := os.ReadFile(filename); err == nil {
			var fileConfig Config
			if err := yaml.Unmarshal(data, &fileConfig); err == nil {
				// 合并配置，环境变量优先
				mergeConfig(&config, &fileConfig)
			}
		}
	}

	// 应用默认值
	applyDefaults(&config)

	return &config, nil
}

// IsNacosEnabled 检查是否启用了Nacos
// 返回值:
//   - bool: 如果配置了NacosUrl则返回true，否则返回false
// 功能: 判断是否启用了Nacos配置中心
func (c *Config) IsNacosEnabled() bool {
	return c.NacosUrl != ""
}



// loadFromEnv 从环境变量加载配置
// 参数:
//   - config: 配置对象指针，将环境变量值填充到此对象
// 功能: 从环境变量读取所有配置项，包括Nacos连接配置和业务配置
func loadFromEnv(config *Config) {
	// Nacos配置
	if val := os.Getenv("NACOS_URL"); val != "" {
		config.NacosUrl = val
	}
	if val := os.Getenv("NACOS_USERNAME"); val != "" {
		config.Username = val
	}
	if val := os.Getenv("NACOS_PASSWORD"); val != "" {
		config.Password = val
	}
	if val := os.Getenv("NACOS_NAMESPACE_ID"); val != "" {
		config.NamespaceId = val
	}
	if val := os.Getenv("NACOS_DATA_ID"); val != "" {
		config.DataId = val
	}
	if val := os.Getenv("NACOS_GROUP"); val != "" {
		config.Group = val
	}
	if val := os.Getenv("NACOS_SKIP_SSL_VERIFY"); val != "" {
		config.SkipSSLVerify = val == "true" || val == "1"
	}

	// 业务配置
	if val := os.Getenv("DOMAINS"); val != "" {
		config.Domains = strings.Split(val, ",")
		// 清理空白字符
		for i, domain := range config.Domains {
			config.Domains[i] = strings.TrimSpace(domain)
		}
	}
	if val := os.Getenv("PORT"); val != "" {
		if port, err := strconv.Atoi(val); err == nil {
			config.Port = port
		}
	}
	if val := os.Getenv("LOG_LEVEL"); val != "" {
		config.LogLevel = val
	}

	if val := os.Getenv("TIMEOUT"); val != "" {
		if timeout, err := strconv.Atoi(val); err == nil {
			config.Timeout = timeout
		}
	}
}

// mergeConfig 合并配置，env配置优先
// 参数:
//   - envConfig: 环境变量配置（优先）
//   - fileConfig: 配置文件配置（作为补充）
// 功能: 将文件配置合并到环境变量配置中，只有当环境变量未设置时才使用文件配置
func mergeConfig(envConfig, fileConfig *Config) {
	// 只有当环境变量未设置时，才使用文件配置
	if envConfig.NacosUrl == "" {
		envConfig.NacosUrl = fileConfig.NacosUrl
	}
	if envConfig.Username == "" {
		envConfig.Username = fileConfig.Username
	}
	if envConfig.Password == "" {
		envConfig.Password = fileConfig.Password
	}
	if envConfig.NamespaceId == "" {
		envConfig.NamespaceId = fileConfig.NamespaceId
	}
	if envConfig.DataId == "" {
		envConfig.DataId = fileConfig.DataId
	}
	if envConfig.Group == "" {
		envConfig.Group = fileConfig.Group
	}

	// 业务配置
	if len(envConfig.Domains) == 0 {
		envConfig.Domains = fileConfig.Domains
	}
	if envConfig.Port == 0 {
		envConfig.Port = fileConfig.Port
	}
	if envConfig.LogLevel == "" {
		envConfig.LogLevel = fileConfig.LogLevel
	}

	if envConfig.Timeout == 0 {
		envConfig.Timeout = fileConfig.Timeout
	}

}

// applyDefaults 应用默认值配置
// 参数:
//   - config: 配置对象指针，将应用默认值到此对象
// 功能: 为未设置的配置项应用默认值，并规范化域名列表
func applyDefaults(config *Config) {
	// 业务配置默认值
	if config.Port == 0 {
		config.Port = 8080
	}
	if config.LogLevel == "" {
		config.LogLevel = "info"
	}

	if config.Timeout == 0 {
		config.Timeout = 30 // 默认超时30秒
	}

	// Nacos连接配置默认值
	if config.DataId == "" {
		config.DataId = "cert-exporter"
	}
	if config.Group == "" {
		config.Group = "DEFAULT_GROUP"
	}
	if config.NamespaceId == "" {
		config.NamespaceId = "public"
	}

	// 规范化域名列表，移除 http:// 或 https:// 前缀
	normalizeDomains(config)
}

// normalizeDomains 规范化域名列表，移除协议前缀
// 参数:
//   - config: 配置对象指针，将规范化其中的域名列表
// 功能: 移除域名中的http://和https://前缀，移除尾部斜杠，清理空白字符
func normalizeDomains(config *Config) {
	for i, domain := range config.Domains {
		// 移除空白字符
		domain = strings.TrimSpace(domain)

		// 移除 https:// 前缀
		if strings.HasPrefix(domain, "https://") {
			domain = strings.TrimPrefix(domain, "https://")
		}

		// 移除 http:// 前缀
		if strings.HasPrefix(domain, "http://") {
			domain = strings.TrimPrefix(domain, "http://")
		}

		// 移除尾部的斜杠
		domain = strings.TrimSuffix(domain, "/")

		config.Domains[i] = domain
	}
}