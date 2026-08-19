package server

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/thun888/filegate/config"
	"github.com/thun888/filegate/internal/backend"
	"github.com/thun888/filegate/internal/engine"
	"github.com/thun888/filegate/internal/middleware"
	"github.com/thun888/filegate/internal/utils"
)

// 内嵌的默认图片
//
//go:embed res_code/*
var resCodeImages embed.FS
var Version = "dev"

// Server 封装了 FileGate 的 HTTP 服务。
type Server struct {
	cfg          *config.Config
	engine       *gin.Engine
	router       *engine.Router
	policyEngine *engine.PolicyEngine
	processor    *engine.Processor
	backends     map[string]backend.Backend
	backendCfgs  map[string]config.BackendConfig
	imgproxy     *imgproxyClient
}

// errorImages 预加载的错误图片缓存，key 为图片文件名（如 "404.png"）。
// 在启动时一次性加载所有错误图片到内存，优先从外部文件系统读取，外部没有则回退到内嵌图片。
var errorImages map[string][]byte

func New(cfg *config.Config) (*Server, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	routeIndex, err := engine.NewRouter(cfg)
	if err != nil {
		return nil, err
	}

	policyEngine := engine.NewPolicyEngine()

	backendMap := make(map[string]backend.Backend, len(cfg.Backends))
	backendCfgMap := make(map[string]config.BackendConfig, len(cfg.Backends))
	for _, backendCfg := range cfg.Backends {
		instance, buildErr := backend.NewFromConfig(backendCfg)
		if buildErr != nil {
			return nil, buildErr
		}

		key := config.NormalizeKey(backendCfg.Name)
		// if _, exists := backendMap[key]; exists {
		// 	return nil, fmt.Errorf("duplicated backend %q", backendCfg.Name)
		// }
		backendMap[key] = instance
		backendCfgMap[key] = backendCfg
		policyEngine.RegisterBackend(backendCfg.Name, backendCfg.CircuitBreaker)
	}

	imgproxyClient, err := newImgproxyClient(cfg.Service.Imgproxy)
	if err != nil {
		return nil, err
	}

	// 手动创建 Gin 引擎以控制Logger的使用
	httpEngine := gin.New()
	if cfg.System.Logging.AccessLog {
		httpEngine.Use(gin.Logger())
	}
	httpEngine.Use(gin.Recovery())

	s := &Server{
		cfg:          cfg,
		engine:       httpEngine,
		router:       routeIndex,
		policyEngine: policyEngine,
		processor:    engine.NewProcessor(routeIndex.FileConversionRule),
		backends:     backendMap,
		backendCfgs:  backendCfgMap,
		imgproxy:     imgproxyClient,
	}

	errorImages = preloadErrorImages()

	httpEngine.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong", "version": Version})
	})
	httpEngine.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "version": Version})
	})
	if cfg.System.Metrics.Prometheus {
		httpEngine.GET("/metrics", gin.WrapH(promhttp.Handler()))
	}

	httpEngine.GET("/fs/:namespace/:class/*objectPath", s.handleFetch)
	httpEngine.HEAD("/fs/:namespace/:class/*objectPath", s.handleFetch)
	// /origin/ 这个接口专门给 imgproxy 拉取原始文件，避免任何转换或其他处理导致无法正常拉取到原始文件
	// 需在反向代理中限制访问来源为 imgproxy 实例
	httpEngine.GET("/origin/:namespace/:class/*objectPath", s.handleOriginFetch)
	httpEngine.HEAD("/origin/:namespace/:class/*objectPath", s.handleOriginFetch)

	return s, nil
}

func (s *Server) Run(addr string) error {
	return s.engine.Run(addr)
}

func (s *Server) Handler() http.Handler {
	return s.engine
}

func (s *Server) handleFetch(c *gin.Context) {
	namespace := c.Param("namespace")
	className := c.Param("class")
	rawObjectPath, err := utils.SanitizePath(strings.TrimPrefix(c.Param("objectPath"), "/"))
	if err != nil {
		abortWithError(c, http.StatusBadRequest, err)
		return
	}
	if rawObjectPath == "" {
		abortWithError(c, http.StatusBadRequest, fmt.Errorf("object path is empty"))
		return
	}

	route, err := s.router.Resolve(namespace, className)
	if err != nil {
		abortWithError(c, http.StatusNotFound, err)
		return
	}

	if err = middleware.VerifyReferer(c.Request, route.Class.Security.ReferCheck); err != nil {
		abortWithError(c, http.StatusForbidden, err)
		return
	}

	if err = middleware.VerifySignature(c.Request, route.Class.Security.Signature); err != nil {
		// statusCode := http.StatusUnauthorized
		// if errors.Is(err, middleware.ErrSignatureExpired) {
		// 	statusCode = http.StatusForbidden
		// }
		abortWithError(c, http.StatusUnauthorized, err)
		return
	}
	sourcePath, transformOptions, rule, err := s.processor.ParseRequest(route.Class, rawObjectPath, c.Request.URL.Query())
	if err != nil {
		abortWithError(c, http.StatusBadRequest, err)
		return
	}

	// PathFilter 必须校验后端实际拉取的 sourcePath（此时转换后缀已被剥离），
	// 否则 deny_patterns / allow_extensions 可被 @100w.jpg 这类转换后缀绕过。
	if err = route.PathFilter.Validate(sourcePath); err != nil {
		abortWithError(c, http.StatusForbidden, err)
		return
	}

	// 处理转换请求
	if transformOptions.Enabled && s.imgproxy != nil {
		imgResp, processErr := s.processWithImgproxy(c.Request, namespace, className, sourcePath, transformOptions, rule)
		if processErr != nil {
			// 没有任何可下发的处理选项属于客户端请求/配置问题，返回 400
			if errors.Is(processErr, ErrImgproxyNoProcessingOptions) {
				abortWithError(c, http.StatusBadRequest, processErr)
				return
			}
			// imgproxy 返回 404 说明源文件不存在，向客户端透传 404
			var statusErr *imgproxyStatusError
			if errors.As(processErr, &statusErr) && statusErr.code == http.StatusNotFound {
				abortWithError(c, http.StatusNotFound, fmt.Errorf("source file not found"))
				return
			}
			abortWithError(c, http.StatusBadGateway, processErr)
			return
		}
		defer imgResp.Body.Close()

		// 将 imgproxy 响应头复制到客户端响应中，保留原始的缓存控制、内容类型等信息
		for _, headerName := range []string{"Etag", "Last-Modified", "Cache-Control", "Content-Disposition", "Accept-Ranges", "Content-Type", "Content-Length"} {
			for _, value := range imgResp.Header.Values(headerName) {
				c.Header(headerName, value)
			}
		}
		// 将自定义响应头添加到客户端响应中
		for key, value := range route.Class.ResponseHeaders {
			c.Header(key, value)
		}

		c.Header("X-FileGate-Transform", engine.FormatTransformOptions(transformOptions))
		c.Header("X-FileGate-Policy", route.Policy.Name)

		// 对于 HEAD 请求，只返回状态码和响应头，不返回响应体
		if c.Request.Method == http.MethodHead {
			c.Status(imgResp.StatusCode)
			return
		}

		c.Status(imgResp.StatusCode)
		// 将 imgproxy 响应体直接流式传输到客户端，避免在服务器端缓存整个文件
		if _, err = io.Copy(c.Writer, imgResp.Body); err != nil {
			_ = c.Error(fmt.Errorf("stream imgproxy response body: %w", err))
		}
		return
	}

	// 处理非转换请求，直接从后端拉取原始文件
	obj, err := s.policyEngine.FetchWithPolicy(c.Request.Context(), route.Policy, s.backends, sourcePath)
	if err != nil {
		abortWithError(c, http.StatusBadGateway, err)
		return
	}
	defer obj.Body.Close()

	for key, value := range route.Class.ResponseHeaders {
		c.Header(key, value)
	}

	for _, headerName := range []string{"Etag", "Last-Modified", "Cache-Control", "Content-Disposition", "Accept-Ranges"} {
		for _, value := range obj.Headers.Values(headerName) {
			c.Header(headerName, value)
		}
	}

	contentType := s.processor.ResolveContentType(obj.ContentType, transformOptions)
	c.Header("Content-Type", contentType)

	if obj.Size >= 0 {
		c.Header("Content-Length", strconv.FormatInt(obj.Size, 10))
	}

	// c.Header("X-FileGate-Transform", engine.FormatTransformOptions(transformOptions)) // 仅在转换请求时返回该响应头
	c.Header("X-FileGate-Policy", route.Policy.Name)

	if c.Request.Method == http.MethodHead {
		c.Status(http.StatusOK)
		return
	}

	c.Status(http.StatusOK)
	if _, err = io.Copy(c.Writer, obj.Body); err != nil {
		_ = c.Error(fmt.Errorf("stream response body: %w", err))
	}
}

// handleOriginFetch 用于给 imgproxy 拉取原始文件，不做转换处理和签名校验。
func (s *Server) handleOriginFetch(c *gin.Context) {
	namespace := c.Param("namespace")
	className := c.Param("class")
	rawObjectPath, err := utils.SanitizePath(strings.TrimPrefix(c.Param("objectPath"), "/"))
	if err != nil {
		abortWithError(c, http.StatusBadRequest, err)
		return
	}
	if rawObjectPath == "" {
		abortWithError(c, http.StatusBadRequest, fmt.Errorf("object path is empty"))
		return
	}

	route, err := s.router.Resolve(namespace, className)
	if err != nil {
		abortWithError(c, http.StatusNotFound, err)
		return
	}

	classCfg := route.Class
	classCfg.FileConversion.Rules = nil
	sourcePath, _, _, err := s.processor.ParseRequest(classCfg, rawObjectPath, c.Request.URL.Query())
	if err != nil {
		abortWithError(c, http.StatusBadRequest, err)
		return
	}

	if err = route.PathFilter.Validate(sourcePath); err != nil {
		abortWithError(c, http.StatusForbidden, err)
		return
	}

	obj, err := s.policyEngine.FetchWithPolicy(c.Request.Context(), route.Policy, s.backends, sourcePath)
	if err != nil {
		abortWithError(c, http.StatusBadGateway, err)
		return
	}
	defer obj.Body.Close()

	for _, headerName := range []string{"Etag", "Last-Modified", "Cache-Control", "Content-Disposition", "Accept-Ranges"} {
		for _, value := range obj.Headers.Values(headerName) {
			c.Header(headerName, value)
		}
	}

	contentType := obj.ContentType
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/octet-stream"
	}
	c.Header("Content-Type", contentType)

	if obj.Size >= 0 {
		c.Header("Content-Length", strconv.FormatInt(obj.Size, 10))
	}

	if c.Request.Method == http.MethodHead {
		c.Status(http.StatusOK)
		return
	}

	c.Status(http.StatusOK)
	if _, err = io.Copy(c.Writer, obj.Body); err != nil {
		_ = c.Error(fmt.Errorf("stream origin response body: %w", err))
	}
}

// abortWithError 统一处理 HTTP 错误响应，根据状态码和请求类型返回最合适的错误格式。
// 函数会按以下优先级处理错误：
//  1. 设置禁用缓存的响应头，确保错误响应不会被缓存
//  2. 如果状态码对应有错误图片（eg: 404→404.png），且图片已加载，则返回图片响应
//  3. 对于 HEAD 请求，只返回状态码和响应头，不返回响应体
//  4. 如果没有可用的错误图片，则返回 JSON 格式的错误信息
//
// 参数：
//   - c: Gin 上下文对象，用于写入响应
//   - statusCode: HTTP 状态码
//   - err: 错误对象，其 Error() 方法返回值将作为 JSON 错误信息
//
// 该函数会终止后续处理链（调用 Abort 系列方法），调用后不应再继续执行其他逻辑。
func abortWithError(c *gin.Context, statusCode int, err error) {
	setNoCacheHeaders(c)

	if imageName := errorImageName(statusCode); imageName != "" {
		if imageData, ok := errorImages[imageName]; ok {
			c.Header("Content-Type", "image/png")
			c.Header("Content-Length", strconv.Itoa(len(imageData)))

			if c.Request != nil && c.Request.Method == http.MethodHead {
				c.AbortWithStatus(statusCode)
				return
			}

			c.Data(statusCode, "image/png", imageData)
			c.Abort()
			return
		}
	}

	// HEAD 响应一律不携带响应体（RFC 9110 §9.3.2），
	// 即使没有对应的错误图片，也不能回退为 JSON 响应体。
	if c.Request != nil && c.Request.Method == http.MethodHead {
		c.AbortWithStatus(statusCode)
		return
	}

	c.AbortWithStatusJSON(statusCode, gin.H{
		"error": err.Error(),
	})
}

// setNoCacheHeaders 设置 HTTP 响应头，禁止客户端及中间代理缓存响应内容。
func setNoCacheHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
}

// errorImageName 根据 HTTP 状态码返回对应的错误图片文件名。
// 返回值：
//   - 对于 404 状态码，返回 "404.png"
//   - 对于 403 状态码，返回 "403.png"
//   - 对于 5xx 状态码，返回 "5xx.png"
//   - 对于其他状态码，返回空字符串
func errorImageName(statusCode int) string {
	if statusCode == http.StatusNotFound {
		return "404.png"
	}
	if statusCode == http.StatusForbidden {
		return "403.png"
	}
	if statusCode >= 500 && statusCode < 600 {
		return "5xx.png"
	}
	return ""
}

// preloadErrorImages 在启动时一次性加载所有错误图片到内存。
// 优先从外部文件系统读取，外部没有则回退到内嵌图片。
func preloadErrorImages() map[string][]byte {
	cache := make(map[string][]byte)

	for _, name := range []string{"404.png", "403.png", "5xx.png"} {
		// 先尝试外部文件
		if data, ok := tryReadExternal(name); ok {
			cache[name] = data
			continue
		}
		// 回退到内嵌图片
		if data, err := resCodeImages.ReadFile("res_code/" + name); err == nil {
			cache[name] = data
		}
	}

	return cache
}

// tryReadExternal 尝试从多个可能的位置读取错误图片文件。
// 依次检查以下路径：
//  1. 当前工作目录下的 res_code 文件夹
//  2. 可执行文件所在目录下的 res_code 文件夹（如果可执行文件路径可获取）
//
// 参数：
//   - imageName: 图片文件名（如 "404.png"）
//
// 返回值：
//   - []byte: 图片文件内容（成功读取时）
//   - bool: 是否成功读取到文件
//
// 任何一个路径读取成功即返回，
// 所有路径都失败时返回 nil 和 false
func tryReadExternal(imageName string) ([]byte, bool) {
	paths := []string{filepath.Join("res_code", imageName)}
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), "res_code", imageName))
	}
	for _, p := range paths {
		if data, err := os.ReadFile(p); err == nil {
			return data, true
		}
	}
	return nil, false
}
