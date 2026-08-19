package config

import (
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/thun888/filegate/internal/utils"
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
		cfg.Backends[i].Type = NormalizeKey(cfg.Backends[i].Type)

		// 如果没有指定或范围错误，则使用重置为默认值
		if cfg.Backends[i].Timeout <= 0 {
			log.Printf("[config] backend %q has non-positive timeout %d, using default 5s", cfg.Backends[i].Name, cfg.Backends[i].Timeout)
			cfg.Backends[i].Timeout = 5 * time.Second
		}
		if cfg.Backends[i].Retries < 0 {
			log.Printf("[config] backend %q has negative retries %d, using default 0", cfg.Backends[i].Name, cfg.Backends[i].Retries)
			cfg.Backends[i].Retries = 0
		}
		if cfg.Backends[i].RetryDelay < 0 {
			log.Printf("[config] backend %q has negative retry delay %d, using default 0", cfg.Backends[i].Name, cfg.Backends[i].RetryDelay)
			cfg.Backends[i].RetryDelay = 0
		}

	}

	// for i := range cfg.BackendPolicies {
	// 	cfg.BackendPolicies[i].Strategy = NormalizeKey(cfg.BackendPolicies[i].Strategy)
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
	cfg.System.Server.BaseURL = strings.TrimSpace(cfg.System.Server.BaseURL)
	if cfg.System.Server.BaseURL == "" {
		cfg.System.Server.BaseURL = "http://" + net.JoinHostPort(cfg.System.Server.Host, fmt.Sprintf("%d", cfg.System.Server.Port))
	}
	if cfg.System.Logging.Level == "" {
		cfg.System.Logging.Level = "info"
	}

	cfg.Service.Imgproxy.URL = strings.TrimSpace(cfg.Service.Imgproxy.URL)
	if cfg.Service.Imgproxy.Timeout <= 0 {
		cfg.Service.Imgproxy.Timeout = 20 * time.Second
	}
	cfg.Service.Imgproxy.Signature.Key = strings.TrimSpace(cfg.Service.Imgproxy.Signature.Key)
	cfg.Service.Imgproxy.Signature.Salt = strings.TrimSpace(cfg.Service.Imgproxy.Signature.Salt)

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

		key := NormalizeKey(b.Name)
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

		pKey := NormalizeKey(p.Name)
		if _, exists := policyNames[pKey]; exists {
			return fmt.Errorf("duplicated backend policy name %q", p.Name)
		}
		policyNames[pKey] = struct{}{}

		resolvableCount := 0
		for _, backendName := range p.Backends {
			if _, exists := backendNames[NormalizeKey(backendName)]; exists {
				resolvableCount++
			}
		}
		// 如果策略中没有任何后端可解析，则视为配置错误
		if resolvableCount == 0 {
			return fmt.Errorf("backend policy %q has no resolvable backend", p.Name)
		}
	}

	ruleNames := make(map[string]struct{}, len(cfg.FileConversionRules))
	for _, r := range cfg.FileConversionRules {
		if strings.TrimSpace(r.Name) == "" {
			return fmt.Errorf("file conversion rule name is required")
		}
		key := NormalizeKey(r.Name)
		if _, exists := ruleNames[key]; exists {
			return fmt.Errorf("duplicated file conversion rule name %q", r.Name)
		}
		ruleNames[key] = struct{}{}

		// 启动时校验 max_file_size，避免运行期每个请求都解析失败
		if raw := strings.TrimSpace(r.MaxFileSize); raw != "" {
			if _, err := utils.ParseByteSize(raw); err != nil {
				return fmt.Errorf("file conversion rule %q has invalid max_file_size %q: %w", r.Name, raw, err)
			}
		}

		// 水印参数校验：position 必须是 imgproxy 支持的取值，opacity 必须在 [0,1]
		if r.Watermark.Enabled {
			switch NormalizeKey(r.Watermark.Position) {
			case "ce", "no", "so", "ea", "we", "noea", "nowe", "soea", "sowe", "re", "ch":
			default:
				return fmt.Errorf("file conversion rule %q has invalid watermark position %q", r.Name, r.Watermark.Position)
			}
			if r.Watermark.Opacity < 0 || r.Watermark.Opacity > 1 {
				return fmt.Errorf("file conversion rule %q has watermark opacity %v out of range [0,1]", r.Name, r.Watermark.Opacity)
			}
		}
	}

	namespaceNames := make(map[string]struct{}, len(cfg.Namespaces))
	for _, ns := range cfg.Namespaces {
		if strings.TrimSpace(ns.Name) == "" {
			return fmt.Errorf("namespace name is required")
		}

		nsKey := NormalizeKey(ns.Name)
		if _, exists := namespaceNames[nsKey]; exists {
			return fmt.Errorf("duplicated namespace name %q", ns.Name)
		}
		namespaceNames[nsKey] = struct{}{}

		if ns.BackendPolicy == "" {
			return fmt.Errorf("namespace %q backend_policy is required", ns.Name)
		}
		if _, exists := policyNames[NormalizeKey(ns.BackendPolicy)]; !exists {
			return fmt.Errorf("namespace %q references unknown backend policy %q", ns.Name, ns.BackendPolicy)
		}

		classNames := make(map[string]struct{}, len(ns.Class))
		for _, cls := range ns.Class {
			if strings.TrimSpace(cls.Name) == "" {
				return fmt.Errorf("class name is required in namespace %q", ns.Name)
			}

			clsKey := NormalizeKey(cls.Name)
			if _, exists := classNames[clsKey]; exists {
				return fmt.Errorf("duplicated class name %q in namespace %q", cls.Name, ns.Name)
			}
			classNames[clsKey] = struct{}{}

			seenRules := make(map[string]struct{}, len(cls.FileConversion.Rules))
			for _, rule := range cls.FileConversion.Rules {
				if strings.TrimSpace(rule) == "" {
					return fmt.Errorf("class %q in namespace %q has file_conversion entry with empty rule", cls.Name, ns.Name)
				}
				ruleKey := NormalizeKey(rule)
				if _, exists := ruleNames[ruleKey]; !exists {
					return fmt.Errorf("class %q in namespace %q references unknown conversion rule %q", cls.Name, ns.Name, rule)
				}
				if _, dup := seenRules[ruleKey]; dup {
					return fmt.Errorf("class %q in namespace %q references conversion rule %q more than once", cls.Name, ns.Name, rule)
				}
				seenRules[ruleKey] = struct{}{}
			}

			if cls.Security.Signature.Enabled && strings.TrimSpace(cls.Security.Signature.Secret) == "" {
				return fmt.Errorf("class %q in namespace %q enables signature but secret is empty", cls.Name, ns.Name)
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

	if cfg.System.Server.BaseURL != "" {
		raw := cfg.System.Server.BaseURL
		if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
			raw = "http://" + raw
		}

		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" {
			return fmt.Errorf("invalid system.server.base_url %q", cfg.System.Server.BaseURL)
		}
	}

	return nil
}

// NormalizeKey 将字符串转换为小写并去除首尾空白字符，用作 map key 的标准化。
func NormalizeKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
