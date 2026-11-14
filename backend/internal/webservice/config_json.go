package webservice

import "encoding/json"

// siteConfig 是存库 config 列的引擎配置部分（Site 中除「列字段」外的全部）。
// 列字段（ID/Name/Domains/Type/Enabled/Default/TLSMode/Challenge/Cert/Remark）
// 在 ws_sites 表有独立列，不进 config。
type siteConfig struct {
	Targets            []string         `json:"targets"`
	LBPolicy           string           `json:"lb_policy"`
	Health             HealthCheck      `json:"health"`
	Locations          []Location       `json:"locations"`
	UseTargetHost      bool             `json:"use_target_host"`
	ForwardedHeaders   bool             `json:"forwarded_headers"`
	InsecureSkipVerify bool             `json:"insecure_skip_verify"`
	Timeouts           BackendTimeouts  `json:"timeouts"`
	RedirectTo         string           `json:"redirect_to"`
	RedirectCode       int              `json:"redirect_code"`
	KeepPath           bool             `json:"keep_path"`
	Root               string           `json:"root"`
	ListDir            bool             `json:"list_dir"`
	Text               string           `json:"text"`
	ContentType        string           `json:"content_type"`
	StatusCode         int              `json:"status_code"`
	IPMode             string           `json:"ip_mode"`
	IPList             []string         `json:"ip_list"`
	Compress           bool             `json:"compress"`
	RateLimit          RateLimit        `json:"rate_limit"`
	RequestHeaders     []string         `json:"request_headers"`
	ResponseHeaders    []string         `json:"response_headers"`
}

// MarshalConfig 序列化站点的引擎配置部分（写库用）。
func MarshalConfig(s *Site) json.RawMessage {
	b, _ := json.Marshal(siteConfig{
		Targets: s.Targets, LBPolicy: s.LBPolicy, Health: s.Health, Locations: s.Locations,
		UseTargetHost: s.UseTargetHost, ForwardedHeaders: s.ForwardedHeaders,
		InsecureSkipVerify: s.InsecureSkipVerify, Timeouts: s.Timeouts,
		RedirectTo: s.RedirectTo, RedirectCode: s.RedirectCode, KeepPath: s.KeepPath,
		Root: s.Root, ListDir: s.ListDir, Text: s.Text, ContentType: s.ContentType, StatusCode: s.StatusCode,
		IPMode: s.IPMode, IPList: s.IPList, Compress: s.Compress, RateLimit: s.RateLimit,
		RequestHeaders: s.RequestHeaders, ResponseHeaders: s.ResponseHeaders,
	})
	return json.RawMessage(b)
}

// UnmarshalConfig 从存库 JSON 恢复引擎配置字段（不影响调用方已填的列字段）。
func UnmarshalConfig(cfg []byte, s *Site) error {
	if len(cfg) == 0 {
		return nil
	}
	var c siteConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return err
	}
	s.Targets, s.LBPolicy, s.Health, s.Locations = c.Targets, c.LBPolicy, c.Health, c.Locations
	s.UseTargetHost, s.ForwardedHeaders, s.InsecureSkipVerify, s.Timeouts =
		c.UseTargetHost, c.ForwardedHeaders, c.InsecureSkipVerify, c.Timeouts
	s.RedirectTo, s.RedirectCode, s.KeepPath = c.RedirectTo, c.RedirectCode, c.KeepPath
	s.Root, s.ListDir, s.Text, s.ContentType, s.StatusCode = c.Root, c.ListDir, c.Text, c.ContentType, c.StatusCode
	s.IPMode, s.IPList, s.Compress, s.RateLimit = c.IPMode, c.IPList, c.Compress, c.RateLimit
	s.RequestHeaders, s.ResponseHeaders = c.RequestHeaders, c.ResponseHeaders
	return nil
}
