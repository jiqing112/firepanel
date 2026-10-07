package proxy

import (
	"fmt"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/alidns"
	"github.com/libdns/cloudflare"
	"github.com/libdns/tencentcloud"
)

// DNSProviderConfig DNS-01 挑战的 DNS 服务商凭据（全局一份，存 settings）。
type DNSProviderConfig struct {
	Provider string `json:"provider"` // cloudflare | alidns | dnspod | ""（未配置）
	APIToken string `json:"api_token"`
	SecretKey string `json:"secret_key"` // alidns 的 AccessKeySecret
}

// Validate 配置合法性。
func (c DNSProviderConfig) Validate() error {
	switch c.Provider {
	case "":
		return fmt.Errorf("未选择 DNS 服务商")
	case "cloudflare":
		if c.APIToken == "" {
			return fmt.Errorf("cloudflare 需要 API Token（Zone.DNS:Edit 权限）")
		}
	case "alidns":
		if c.APIToken == "" || c.SecretKey == "" {
			return fmt.Errorf("阿里云 DNS 需要 AccessKey ID 与 AccessKey Secret")
		}
	case "dnspod":
		if c.APIToken == "" || c.SecretKey == "" {
			return fmt.Errorf("DNSPod/腾讯云 需要 SecretId 与 SecretKey")
		}
	default:
		return fmt.Errorf("不支持的 DNS 服务商: %s", c.Provider)
	}
	return nil
}

// Mask 返回脱敏副本（API 展示用）。
func (c DNSProviderConfig) Mask() DNSProviderConfig {
	out := c
	out.APIToken = maskSecret(c.APIToken)
	out.SecretKey = maskSecret(c.SecretKey)
	return out
}

func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}

// buildDNSProvider 按凭据构造 libdns 提供商（实现 certmagic.DNSProvider，
// 由 certmagic.DNSManager 负责 zone 发现与 TXT 记录读写）。
func buildDNSProvider(c DNSProviderConfig) certmagic.DNSProvider {
	switch c.Provider {
	case "cloudflare":
		return &cloudflare.Provider{APIToken: c.APIToken}
	case "alidns":
		return &alidns.Provider{CredentialInfo: alidns.CredentialInfo{
			AccessKeyID:     c.APIToken,
			AccessKeySecret: c.SecretKey,
		}}
	case "dnspod":
		return &tencentcloud.Provider{SecretId: c.APIToken, SecretKey: c.SecretKey}
	default:
		return nil
	}
}
