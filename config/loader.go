package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
)

// Load 从指定路径读取 YAML 并解析为配置对象。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	return Parse(data)
}

// Parse 将 YAML 字节流解析为配置对象。
func Parse(data []byte) (*Config, error) {
	cfg := defaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("unmarshal yaml: %w", err)
	}

	normalize(cfg)
	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func defaultConfig() *Config {
	return &Config{
		System: SystemConfig{
			Server: ServerConfig{
				Host: "127.0.0.1",
				Port: 8080,
			},
			Logging: LoggingConfig{
				Level:     "info",
				AccessLog: true,
			},
			Metrics: MetricsConfig{
				Prometheus: true,
				Labels: map[string]string{
					"service": "filegate",
				},
			},
		},
	}
}

func normalize(cfg *Config) {
	for i := range cfg.Backends {
		cfg.Backends[i].Type = canonicalName(cfg.Backends[i].Type)

		// 如果没有指定或范围错误，则使用重置为默认值
		if cfg.Backends[i].Timeout <= 0 {
			fmt.Printf("backend %q has non-positive timeout %d, using default 5s\n", cfg.Backends[i].Name, cfg.Backends[i].Timeout)
			cfg.Backends[i].Timeout = 5 * time.Second
		}
		if cfg.Backends[i].Retries < 0 {
			fmt.Printf("backend %q has negative retries %d, using default 0\n", cfg.Backends[i].Name, cfg.Backends[i].Retries)
			cfg.Backends[i].Retries = 0
		}
		if cfg.Backends[i].RetryDelay < 0 {
			fmt.Printf("backend %q has negative retry delay %d, using default 0\n", cfg.Backends[i].Name, cfg.Backends[i].RetryDelay)
			cfg.Backends[i].RetryDelay = 0
		}

	}

	// for i := range cfg.BackendPolicies {
	// 	cfg.BackendPolicies[i].Strategy = canonicalName(cfg.BackendPolicies[i].Strategy)
	// 	if cfg.BackendPolicies[i].Strategy == "" {
	// 		cfg.BackendPolicies[i].Strategy = "single"
	// 	}
	// }

	if cfg.System.Server.Host == "" {
		cfg.System.Server.Host = "127.0.0.1"
	}
	if cfg.System.Server.Port == 0 {
		cfg.System.Server.Port = 8080
	}
	cfg.System.Server.Domain = strings.TrimSpace(cfg.System.Server.Domain)
	if cfg.System.Server.Domain == "" {
		cfg.System.Server.Domain = "http://" + net.JoinHostPort(cfg.System.Server.Host, fmt.Sprintf("%d", cfg.System.Server.Port))
	}
	if cfg.System.Logging.Level == "" {
		cfg.System.Logging.Level = "info"
	}

	cfg.Service.Imgproxy.URL = strings.TrimSpace(cfg.Service.Imgproxy.URL)
	cfg.Service.Imgproxy.Signature.Key = strings.TrimSpace(cfg.Service.Imgproxy.Signature.Key)
	cfg.Service.Imgproxy.Signature.Salt = strings.TrimSpace(cfg.Service.Imgproxy.Signature.Salt)

	cfg.Service.Redis.Addr = strings.TrimSpace(cfg.Service.Redis.Addr)
	cfg.Service.Redis.Password = strings.TrimSpace(cfg.Service.Redis.Password)
	if cfg.Service.Redis.DB < 0 {
		cfg.Service.Redis.DB = 0
	}
	if strings.TrimSpace(cfg.Service.Redis.KeyPrefix) == "" {
		cfg.Service.Redis.KeyPrefix = "filegate:limit:"
	}

	// if len(cfg.BackendPolicies) > 0 {
	// 	defaultPolicy := cfg.BackendPolicies[0].Name
	// 	for i := range cfg.Namespaces {
	// 		if strings.TrimSpace(cfg.Namespaces[i].BackendPolicy) == "" {
	// 			cfg.Namespaces[i].BackendPolicy = defaultPolicy
	// 		}
	// 	}
	// }
}

func validate(cfg *Config) error {
	if len(cfg.Backends) == 0 {
		return fmt.Errorf("at least one backend is required")
	}

	backendNames := make(map[string]struct{}, len(cfg.Backends))
	for _, b := range cfg.Backends {
		if strings.TrimSpace(b.Name) == "" {
			return fmt.Errorf("backend name is required")
		}
		if b.Type == "" {
			return fmt.Errorf("backend %q type is required", b.Name)
		}

		key := canonicalName(b.Name)
		if _, exists := backendNames[key]; exists {
			return fmt.Errorf("duplicated backend name %q", b.Name)
		}
		backendNames[key] = struct{}{}
	}

	policyNames := make(map[string]struct{}, len(cfg.BackendPolicies))
	for _, p := range cfg.BackendPolicies {
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("backend policy name is required")
		}
		if len(p.Backends) == 0 {
			return fmt.Errorf("backend policy %q must have backends", p.Name)
		}

		pKey := canonicalName(p.Name)
		if _, exists := policyNames[pKey]; exists {
			return fmt.Errorf("duplicated backend policy name %q", p.Name)
		}
		policyNames[pKey] = struct{}{}

		resolvableCount := 0
		for _, backendName := range p.Backends {
			if _, exists := backendNames[canonicalName(backendName)]; exists {
				resolvableCount++
			}
		}
		if resolvableCount == 0 {
			return fmt.Errorf("backend policy %q has no resolvable backend", p.Name)
		}
	}

	ruleNames := make(map[string]struct{}, len(cfg.FileConversionRules))
	for _, r := range cfg.FileConversionRules {
		if strings.TrimSpace(r.Name) == "" {
			return fmt.Errorf("file conversion rule name is required")
		}
		key := canonicalName(r.Name)
		if _, exists := ruleNames[key]; exists {
			return fmt.Errorf("duplicated file conversion rule name %q", r.Name)
		}
		ruleNames[key] = struct{}{}
	}

	namespaceNames := make(map[string]struct{}, len(cfg.Namespaces))
	for _, ns := range cfg.Namespaces {
		if strings.TrimSpace(ns.Name) == "" {
			return fmt.Errorf("namespace name is required")
		}

		nsKey := canonicalName(ns.Name)
		if _, exists := namespaceNames[nsKey]; exists {
			return fmt.Errorf("duplicated namespace name %q", ns.Name)
		}
		namespaceNames[nsKey] = struct{}{}

		if ns.BackendPolicy == "" {
			return fmt.Errorf("namespace %q backend_policy is required", ns.Name)
		}
		if _, exists := policyNames[canonicalName(ns.BackendPolicy)]; !exists {
			return fmt.Errorf("namespace %q references unknown backend policy %q", ns.Name, ns.BackendPolicy)
		}

		classNames := make(map[string]struct{}, len(ns.Class))
		for _, cls := range ns.Class {
			if strings.TrimSpace(cls.Name) == "" {
				return fmt.Errorf("class name is required in namespace %q", ns.Name)
			}

			clsKey := canonicalName(cls.Name)
			if _, exists := classNames[clsKey]; exists {
				return fmt.Errorf("duplicated class name %q in namespace %q", cls.Name, ns.Name)
			}
			classNames[clsKey] = struct{}{}

			if cls.FileConversion.Enabled {
				if strings.TrimSpace(cls.FileConversion.Rule) == "" {
					return fmt.Errorf("class %q in namespace %q enables file_conversion but has empty rule", cls.Name, ns.Name)
				}
				if _, exists := ruleNames[canonicalName(cls.FileConversion.Rule)]; !exists {
					return fmt.Errorf("class %q in namespace %q references unknown conversion rule %q", cls.Name, ns.Name, cls.FileConversion.Rule)
				}
			}
		}
	}

	if cfg.Service.Imgproxy.Signature.Enabled {
		if cfg.Service.Imgproxy.URL == "" {
			return fmt.Errorf("imgproxy signature enabled but service.imgproxy.url is empty")
		}
		if cfg.Service.Imgproxy.Signature.Key == "" || cfg.Service.Imgproxy.Signature.Salt == "" {
			return fmt.Errorf("imgproxy signature enabled but key/salt is empty")
		}
	}

	if cfg.System.Server.Domain != "" {
		raw := cfg.System.Server.Domain
		if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
			raw = "http://" + raw
		}

		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" {
			return fmt.Errorf("invalid system.server.domain %q", cfg.System.Server.Domain)
		}
	}

	if cfg.Service.Redis.Enabled && cfg.Service.Redis.Addr == "" {
		return fmt.Errorf("redis limiter enabled but service.redis.addr is empty")
	}

	return nil
}

func canonicalName(s string) string {
	// 移除字符串首尾的空白字符
	// 将字符串中的所有字母转换为小写
	return strings.ToLower(strings.TrimSpace(s))
}
