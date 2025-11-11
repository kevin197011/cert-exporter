package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"
)

// CertInfo SSL证书信息结构
type CertInfo struct {
	Domain     string
	ExpiryDate time.Time
	Issuer     string
	Subject    string
	Method     string // 检测方法: ssl
}

// GetCertInfo 获取SSL证书信息
func GetCertInfo(domain string, timeout time.Duration) (*CertInfo, error) {
	slog.Debug("开始SSL证书查询", "domain", domain, "timeout", timeout)
	
	// 创建带超时的context
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// 解析域名，确保格式正确
	host, port := parseDomainAndPort(domain)
	
	// 创建TLS连接配置
	config := &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: false, // 验证证书链
	}

	// 使用channel来处理超时
	type result struct {
		cert *CertInfo
		err  error
	}

	resultChan := make(chan result, 1)
	
	// 在goroutine中执行SSL连接
	go func() {
		slog.Debug("执行SSL连接", "host", host, "port", port)
		
		dialer := &net.Dialer{
			Timeout: timeout,
		}
		
		conn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(host, port), config)
		if err != nil {
			slog.Debug("SSL连接失败", "domain", domain, "error", err)
			resultChan <- result{cert: nil, err: fmt.Errorf("SSL连接失败: %v", err)}
			return
		}
		defer conn.Close()

		// 获取证书链
		certs := conn.ConnectionState().PeerCertificates
		if len(certs) == 0 {
			resultChan <- result{cert: nil, err: fmt.Errorf("未找到SSL证书")}
			return
		}

		// 使用第一个证书（服务器证书）
		cert := certs[0]
		
		certInfo := &CertInfo{
			Domain:     domain,
			ExpiryDate: cert.NotAfter,
			Issuer:     cert.Issuer.CommonName,
			Subject:    cert.Subject.CommonName,
			Method:     "ssl",
		}
		
		slog.Debug("SSL证书查询成功", "domain", domain, 
			"expiry_date", cert.NotAfter,
			"issuer", cert.Issuer.CommonName,
			"subject", cert.Subject.CommonName)
		
		resultChan <- result{cert: certInfo, err: nil}
	}()

	// 等待结果或超时
	select {
	case res := <-resultChan:
		return res.cert, res.err
	case <-ctx.Done():
		slog.Debug("SSL证书查询超时", "domain", domain, "timeout", timeout)
		return nil, fmt.Errorf("SSL证书查询超时: %v", ctx.Err())
	}
}

// GetCertInfoWithFallback 使用SSL获取证书信息（带重试）
func GetCertInfoWithFallback(domain string, timeout time.Duration, config *Config) (*CertInfo, error) {
	maxRetries := 2
	var lastErr error
	
	for attempt := 1; attempt <= maxRetries; attempt++ {
		slog.Debug("SSL证书查询尝试", "domain", domain, "attempt", attempt, "max_retries", maxRetries)
		
		info, err := GetCertInfo(domain, timeout)
		if err == nil {
			if attempt > 1 {
				slog.Info("SSL证书查询重试成功", "domain", domain, "attempt", attempt)
			}
			return info, nil
		}
		
		lastErr = err
		slog.Debug("SSL证书查询失败", "domain", domain, "attempt", attempt, "error", err)
		
		// 如果不是最后一次尝试，等待一下再重试
		if attempt < maxRetries {
			waitTime := time.Duration(attempt) * time.Second
			slog.Debug("等待重试", "domain", domain, "wait_seconds", waitTime.Seconds())
			time.Sleep(waitTime)
		}
	}
	
	// 根据错误类型决定日志级别
	errMsg := lastErr.Error()
	if strings.Contains(errMsg, "timeout") || 
	   strings.Contains(errMsg, "i/o timeout") ||
	   strings.Contains(errMsg, "connection refused") ||
	   strings.Contains(errMsg, "no such host") ||
	   strings.Contains(errMsg, "server misbehaving") {
		// 网络问题使用 WARN 级别，避免刷屏
		slog.Warn("SSL证书查询失败（网络问题）", "domain", domain, "attempts", maxRetries, "error", lastErr)
	} else {
		// 其他错误使用 ERROR 级别
		slog.Error("SSL证书查询失败", "domain", domain, "attempts", maxRetries, "error", lastErr)
	}
	return nil, fmt.Errorf("SSL证书查询失败: %v", lastErr)
}

// parseDomainAndPort 解析域名和端口
func parseDomainAndPort(domain string) (string, string) {
	// 移除协议前缀
	domain = strings.TrimPrefix(domain, "https://")
	domain = strings.TrimPrefix(domain, "http://")
	
	// 移除路径
	if idx := strings.Index(domain, "/"); idx != -1 {
		domain = domain[:idx]
	}
	
	// 检查是否包含端口
	if strings.Contains(domain, ":") {
		host, port, err := net.SplitHostPort(domain)
		if err == nil {
			return host, port
		}
	}
	
	// 默认使用443端口
	return domain, "443"
}

// validateDomain 验证域名格式
func validateDomain(domain string) error {
	if domain == "" {
		return fmt.Errorf("域名不能为空")
	}
	
	// 解析URL以验证格式
	if !strings.HasPrefix(domain, "http://") && !strings.HasPrefix(domain, "https://") {
		domain = "https://" + domain
	}
	
	_, err := url.Parse(domain)
	if err != nil {
		return fmt.Errorf("无效的域名格式: %v", err)
	}
	
	return nil
}