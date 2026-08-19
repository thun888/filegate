package config

import "time"

// Config 是 FileGate 的根配置。
type Config struct {
	Backends            []BackendConfig      `yaml:"backends"`              // 后端存储服务配置列表
	BackendPolicies     []BackendPolicy      `yaml:"backend_policy"`        // 后端选择策略配置列表
	Namespaces          []NamespaceConfig    `yaml:"namespaces"`            // 命名空间配置列表
	FileConversionRules []FileConversionRule `yaml:"file_conversion_rules"` // 文件转换规则配置列表
	Service             ServiceConfig        `yaml:"service"`               // 外部服务集成配置
	System              SystemConfig         `yaml:"system"`                // 系统级配置
}

// BackendConfig 定义后端存储服务的配置。
type BackendConfig struct {
	Name           string               `yaml:"name"`            // 后端名称，用于标识和引用
	Type           string               `yaml:"type"`            // 后端类型：fs(本地文件系统), http(HTTP服务), s3(对象存储)
	Config         BackendDetailConfig  `yaml:"config"`          // 后端详细连接配置
	Timeout        time.Duration        `yaml:"timeout"`         // 请求超时时间
	Retries        int                  `yaml:"retries"`         // 请求失败重试次数
	RetryDelay     time.Duration        `yaml:"retry_delay"`     // 重试间隔时间
	CircuitBreaker CircuitBreakerConfig `yaml:"circuit_breaker"` // 熔断器配置
}

// BackendDetailConfig 包含后端服务的详细连接配置。
type BackendDetailConfig struct {
	URLPrefix    string            `yaml:"url_prefix"`    // HTTP后端的URL前缀
	ExtraHeaders map[string]string `yaml:"extra_headers"` // HTTP后端的额外请求头

	Endpoint  string `yaml:"endpoint"`   // S3兼容服务的端点地址
	Region    string `yaml:"region"`     // S3区域
	Bucket    string `yaml:"bucket"`     // S3存储桶名称
	AccessKey string `yaml:"access_key"` // S3访问密钥ID
	SecretKey string `yaml:"secret_key"` // S3访问密钥Secret

	RootPath string `yaml:"root_path"` // 本地文件系统的根路径
}

// CircuitBreakerConfig 定义熔断器的配置参数。
type CircuitBreakerConfig struct {
	FailureThreshold int           `yaml:"failure_threshold"` // 触发熔断的连续失败次数阈值
	RecoveryTimeout  time.Duration `yaml:"recovery_timeout"`  // 熔断后进入半开状态的等待时间
	HalfOpenTimeout  time.Duration `yaml:"half_open_timeout"` // 半开状态的超时时间
}

// BackendPolicy 定义后端选择策略。
type BackendPolicy struct {
	Name     string   `yaml:"name"`     // 策略名称，用于在命名空间中引用
	Strategy string   `yaml:"strategy"` // 选择策略：round_robin(轮询), random(随机), failover(故障转移)
	Backends []string `yaml:"backends"` // 参与此策略的后端名称列表
}

// NamespaceConfig 定义命名空间配置，包含多个类别。
type NamespaceConfig struct {
	Name          string        `yaml:"name"`           // 命名空间名称，用于URL路径路由
	BackendPolicy string        `yaml:"backend_policy"` // 使用的后端选择策略名称
	Class         []ClassConfig `yaml:"class"`          // 命名空间下的类别配置列表
}

// ClassConfig 定义命名空间下的类别配置。
type ClassConfig struct {
	Name            string                    `yaml:"name"`             // 类别名称，用于URL路径路由
	Security        SecurityConfig            `yaml:"security"`         // 安全相关配置
	FileConversion  ClassFileConversionConfig `yaml:"file_conversion"`  // 文件转换配置
	ResponseHeaders map[string]string         `yaml:"response_headers"` // 自定义响应头
}

// SecurityConfig 包含安全相关的配置项。
type SecurityConfig struct {
	ReferCheck ReferCheckConfig `yaml:"refer_check"` // Referer检查配置
	Signature  SignatureConfig  `yaml:"signature"`   // 请求签名验证配置
	PathFilter PathFilterConfig `yaml:"path_filter"` // 路径过滤规则配置
}

// ReferCheckConfig 定义 Referer 检查配置。
type ReferCheckConfig struct {
	Enabled         bool     `yaml:"enabled"`          // 是否启用Referer检查
	AllowedReferers []string `yaml:"allowed_referers"` // 允许的Referer域名列表：精确域名（example.com）或通配符域名（*.example.com），单独的 * 放行所有域名
}

// SignatureConfig 定义请求签名验证配置。
type SignatureConfig struct {
	Enabled bool   `yaml:"enabled"` // 是否启用签名验证
	Secret  string `yaml:"secret"`  // 签名密钥
	Expire  int64  `yaml:"expire"`  // 签名过期时间（秒）（设置为0表示不过期）
}

// PathFilterConfig 定义路径过滤规则。
type PathFilterConfig struct {
	DenyPatterns    []string `yaml:"deny_patterns"`    // 拒绝的路径列表：按字面量子串匹配（非正则），路径包含任一子串即拒绝
	AllowPaths      []string `yaml:"allow_paths"`      // 允许的路径前缀列表
	AllowExtensions []string `yaml:"allow_extensions"` // 允许的文件扩展名列表
}

// ClassFileConversionConfig 定义类别级别的文件转换配置。
type ClassFileConversionConfig struct {
	Rules               []string            `yaml:"rules"`                 // 可用的转换规则名称列表
	EnableRequestParams RequestParamsConfig `yaml:"enable_request_params"` // 允许通过请求参数覆盖的转换选项（对 rules 内所有规则生效）
}

// FileConversionRule 定义文件转换规则的完整配置。
type FileConversionRule struct {
	Name          string                  `yaml:"name"`           // 规则名称，用于在类别中引用
	MaxFileSize   string                  `yaml:"max_file_size"`  // 最大文件大小限制，如"10MB"
	DefaultParams ConversionDefaultParams `yaml:"default_params"` // 转换的默认参数值
	Watermark     WatermarkConfig         `yaml:"watermark"`      // 水印配置
}

// RequestParamsConfig 定义可通过请求参数覆盖的转换选项。
type RequestParamsConfig struct {
	Width   ParamRange `yaml:"width"`   // 宽度参数的允许范围
	Height  ParamRange `yaml:"height"`  // 高度参数的允许范围
	Quality ParamRange `yaml:"quality"` // 质量参数的允许范围
	Blur    bool       `yaml:"blur"`    // 是否允许通过请求参数设置模糊
	Format  bool       `yaml:"format"`  // 是否允许通过请求参数设置输出格式
}

// ParamRange 定义参数的允许范围。
type ParamRange struct {
	Enabled bool `yaml:"enabled"` // 是否允许通过请求参数覆盖
	Min     int  `yaml:"min"`     // 最小值
	Max     int  `yaml:"max"`     // 最大值
}

// ConversionDefaultParams 定义转换的默认参数值。
type ConversionDefaultParams struct {
	Width   int     `yaml:"width"`   // 默认宽度（像素）
	Height  int     `yaml:"height"`  // 默认高度（像素）
	Blur    float64 `yaml:"blur"`    // 默认模糊强度（高斯模糊 sigma，浮点，0 表示不模糊）
	Quality int     `yaml:"quality"` // 默认质量（1-100）
	Format  string  `yaml:"format"`  // 默认输出格式，如"webp", "jpeg", "png"
}

// WatermarkConfig 定义水印配置。
type WatermarkConfig struct {
	Enabled  bool    `yaml:"enabled"`  // 是否启用水印
	Opacity  float64 `yaml:"opacity"`  // 水印透明度修饰符，最终透明度 = base_opacity * opacity
	Position string  `yaml:"position"` // 水印位置：ce(居中), no(顶部), so(底部), ea(右边), we(左边), noea(右上), nowe(左上), soea(右下), sowe(左下), re(平铺), ch(棋盘格平铺，Pro功能)
	XOffset  float64 `yaml:"x_offset"` // X轴偏移量，>=1或<=-1为绝对值，(-1,1)为相对值；re/ch位置时定义瓦片间距
	YOffset  float64 `yaml:"y_offset"` // Y轴偏移量，>=1或<=-1为绝对值，(-1,1)为相对值；re/ch位置时定义瓦片间距
	Scale    float64 `yaml:"scale"`    // 水印大小相对于结果图片的比例，0表示不改变水印大小
}

// ServiceConfig 定义外部服务集成配置。
type ServiceConfig struct {
	Imgproxy ImgproxyConfig `yaml:"imgproxy"` // imgproxy图片处理服务配置
}

// ImgproxyConfig 定义 imgproxy 服务的配置。
type ImgproxyConfig struct {
	URL       string                  `yaml:"url"`       // imgproxy服务地址
	Timeout   time.Duration           `yaml:"timeout"`   // 请求超时时间（<=0 时使用默认 20s）
	Signature ImgproxySignatureConfig `yaml:"signature"` // imgproxy签名配置
}

// ImgproxySignatureConfig 定义 imgproxy 签名配置。
type ImgproxySignatureConfig struct {
	Enabled bool   `yaml:"enabled"` // 是否启用签名验证
	Key     string `yaml:"key"`     // 签名密钥
	Salt    string `yaml:"salt"`    // 签名盐值
}

// SystemConfig 定义系统级配置。
type SystemConfig struct {
	Server  ServerConfig  `yaml:"server"`  // HTTP服务器配置
	Logging LoggingConfig `yaml:"logging"` // 日志配置
	Metrics MetricsConfig `yaml:"metrics"` // 监控指标配置
}

// ServerConfig 定义 HTTP 服务器配置。
type ServerConfig struct {
	BaseURL string `yaml:"base_url"` // 服务器基础URL，用于生成完整URL
	Host    string `yaml:"host"`     // 监听主机地址
	Port    int    `yaml:"port"`     // 监听端口
	Debug   bool   `yaml:"debug"`    // 调试模式：true 使用 gin.DebugMode（输出路由注册等调试信息），false 使用 gin.ReleaseMode
}

// LoggingConfig 定义日志配置。
type LoggingConfig struct {
	Level     string `yaml:"level"`      // 日志级别：debug, info, warn, error
	AccessLog bool   `yaml:"access_log"` // 是否记录访问日志
}

// MetricsConfig 定义监控指标配置。
type MetricsConfig struct {
	Prometheus bool              `yaml:"prometheus"` // 是否启用Prometheus指标导出
	Labels     map[string]string `yaml:"labels"`     // 自定义指标标签
}
