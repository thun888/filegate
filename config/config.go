package config

import "time"

// Config 是 FileGate 的根配置。
type Config struct {
	Backends            []BackendConfig      `yaml:"backends"`
	BackendPolicies     []BackendPolicy      `yaml:"backend_policy"`
	Namespaces          []NamespaceConfig    `yaml:"namespaces"`
	FileConversionRules []FileConversionRule `yaml:"file_conversion_rules"`
	Service             ServiceConfig        `yaml:"service"`
	System              SystemConfig         `yaml:"system"`
}

type BackendConfig struct {
	Name           string               `yaml:"name"`
	Type           string               `yaml:"type"`
	Config         BackendDetailConfig  `yaml:"config"`
	Timeout        time.Duration        `yaml:"timeout"`
	Retries        int                  `yaml:"retries"`
	RetryDelay     time.Duration        `yaml:"retry_delay"`
	CircuitBreaker CircuitBreakerConfig `yaml:"circuit_breaker"`
}

type BackendDetailConfig struct {
	URLPrefix    string            `yaml:"url_prefix"`
	ExtraHeaders map[string]string `yaml:"extra_headers"`

	Endpoint  string `yaml:"endpoint"`
	Region    string `yaml:"region"`
	Bucket    string `yaml:"bucket"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`

	RootPath string `yaml:"root_path"`
}

type CircuitBreakerConfig struct {
	FailureThreshold int           `yaml:"failure_threshold"`
	RecoveryTimeout  time.Duration `yaml:"recovery_timeout"`
	HalfOpenTimeout  time.Duration `yaml:"half_open_timeout"`
}

type BackendPolicy struct {
	Name     string   `yaml:"name"`
	Strategy string   `yaml:"strategy"`
	Backends []string `yaml:"backends"`
}

type NamespaceConfig struct {
	Name          string        `yaml:"name"`
	BackendPolicy string        `yaml:"backend_policy"`
	Class         []ClassConfig `yaml:"class"`
}

type ClassConfig struct {
	Name            string                    `yaml:"name"`
	Security        SecurityConfig            `yaml:"security"`
	FileConversion  ClassFileConversionConfig `yaml:"file_conversion"`
	ResponseHeaders map[string]string         `yaml:"response_headers"`
}

type SecurityConfig struct {
	ReferCheck ReferCheckConfig `yaml:"refer_check"`
	Signature  SignatureConfig  `yaml:"signature"`
	Limit      LimitConfig      `yaml:"limit"`
	PathFilter PathFilterConfig `yaml:"path_filter"`
}

type ReferCheckConfig struct {
	Enabled         bool     `yaml:"enabled"`
	AllowedReferers []string `yaml:"allowed_referers"`
}

type SignatureConfig struct {
	Enabled bool   `yaml:"enabled"`
	Secret  string `yaml:"secret"`
	Expire  int64  `yaml:"expire"`
}

type LimitConfig struct {
	Enabled     bool          `yaml:"enabled"`
	MaxRequests int           `yaml:"max_requests"`
	Window      time.Duration `yaml:"window"`
}

type PathFilterConfig struct {
	DenyPatterns    []string `yaml:"deny_patterns"`
	AllowPaths      []string `yaml:"allow_paths"`
	AllowExtensions []string `yaml:"allow_extensions"`
}

type ClassFileConversionConfig struct {
	Enabled bool   `yaml:"enabled"`
	Rule    string `yaml:"rule"`
}

type FileConversionRule struct {
	Name                string                  `yaml:"name"`
	MaxFileSize         string                  `yaml:"max_file_size"`
	SupportedFormats    []string                `yaml:"supported_formats"`
	EnableRequestParams RequestParamsConfig     `yaml:"enable_request_params"`
	DefaultParams       ConversionDefaultParams `yaml:"default_params"`
	Watermark           WatermarkConfig         `yaml:"watermark"`
}

type RequestParamsConfig struct {
	Width   ParamRange `yaml:"width"`
	Height  ParamRange `yaml:"height"`
	Quality ParamRange `yaml:"quality"`
	Blur    bool       `yaml:"blur"`
	Format  bool       `yaml:"format"`
	Zip     bool       `yaml:"zip"`
}

type ParamRange struct {
	Min int `yaml:"min"`
	Max int `yaml:"max"`
}

type ConversionDefaultParams struct {
	Width   int    `yaml:"width"`
	Height  int    `yaml:"height"`
	Blur    int    `yaml:"blur"`
	Quality int    `yaml:"quality"`
	Format  string `yaml:"format"`
	Zip     bool   `yaml:"zip"`
}

type WatermarkConfig struct {
	Enabled  bool    `yaml:"enabled"`
	Position string  `yaml:"position"`
	Opacity  float64 `yaml:"opacity"`
}

type ServiceConfig struct {
	Imgproxy ImgproxyConfig `yaml:"imgproxy"`
	Redis    RedisConfig    `yaml:"redis"`
}

type ImgproxyConfig struct {
	URL       string                  `yaml:"url"`
	Signature ImgproxySignatureConfig `yaml:"signature"`
}

type ImgproxySignatureConfig struct {
	Enabled bool   `yaml:"enabled"`
	Key     string `yaml:"key"`
	Salt    string `yaml:"salt"`
}

type SystemConfig struct {
	Server  ServerConfig  `yaml:"server"`
	Logging LoggingConfig `yaml:"logging"`
	Metrics MetricsConfig `yaml:"metrics"`
}

type RedisConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Addr      string `yaml:"addr"`
	Password  string `yaml:"password"`
	DB        int    `yaml:"db"`
	KeyPrefix string `yaml:"key_prefix"`
}

type ServerConfig struct {
	Domain string `yaml:"domain"`
	Host   string `yaml:"host"`
	Port   int    `yaml:"port"`
}

type LoggingConfig struct {
	Level     string `yaml:"level"`
	AccessLog bool   `yaml:"access_log"`
}

type MetricsConfig struct {
	Prometheus bool              `yaml:"prometheus"`
	Labels     map[string]string `yaml:"labels"`
}
