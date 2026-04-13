package server

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"filegate/config"
	"filegate/internal/backend"
	"filegate/internal/engine"
	"filegate/internal/middleware"
)

// Server 封装了 FileGate 的 HTTP 服务。
type Server struct {
	cfg          *config.Config
	engine       *gin.Engine
	router       *engine.Router
	policyEngine *engine.PolicyEngine
	processor    *engine.Processor
	limiter      *middleware.RateLimiter
	backends     map[string]backend.Backend
	backendCfgs  map[string]config.BackendConfig
	imgproxy     *imgproxyClient

	pathFilterMu sync.RWMutex
	pathFilters  map[string]*middleware.PathFilter
}

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

		key := normalizeKey(backendCfg.Name)
		if _, exists := backendMap[key]; exists {
			return nil, fmt.Errorf("duplicated backend %q", backendCfg.Name)
		}
		backendMap[key] = instance
		backendCfgMap[key] = backendCfg
		policyEngine.RegisterBackend(backendCfg.Name, backendCfg.CircuitBreaker)
	}

	imgproxyClient, err := newImgproxyClient(cfg.Service.Imgproxy)
	if err != nil {
		return nil, err
	}

	limiter, err := middleware.NewRateLimiterWithRedis(cfg.Service.Redis)
	if err != nil {
		return nil, err
	}

	pathFilters, err := buildPathFilterIndex(cfg)
	if err != nil {
		return nil, err
	}

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
		processor:    engine.NewProcessor(cfg),
		limiter:      limiter,
		backends:     backendMap,
		backendCfgs:  backendCfgMap,
		imgproxy:     imgproxyClient,
		pathFilters:  pathFilters,
	}

	httpEngine.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})
	httpEngine.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	if cfg.System.Metrics.Prometheus {
		httpEngine.GET("/metrics", gin.WrapH(promhttp.Handler()))
	}

	httpEngine.GET("/fs/:namespace/:class/*objectPath", s.handleFetch)
	httpEngine.HEAD("/fs/:namespace/:class/*objectPath", s.handleFetch)
	httpEngine.GET("/origin/:namespace/:class/*objectPath", s.handleOriginFetch)
	httpEngine.HEAD("/origin/:namespace/:class/*objectPath", s.handleOriginFetch)

	return s, nil
}

func buildPathFilterIndex(cfg *config.Config) (map[string]*middleware.PathFilter, error) {
	filters := make(map[string]*middleware.PathFilter)
	if cfg == nil {
		return filters, nil
	}

	for _, ns := range cfg.Namespaces {
		for _, cls := range ns.Class {
			key := normalizeKey(ns.Name) + ":" + normalizeKey(cls.Name)

			filter, err := middleware.NewPathFilter(cls.Security.PathFilter)
			if err != nil {
				return nil, fmt.Errorf("build path filter for %q/%q: %w", ns.Name, cls.Name, err)
			}

			filters[key] = filter
		}
	}

	return filters, nil
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
	rawObjectPath := strings.TrimPrefix(c.Param("objectPath"), "/")
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
		statusCode := http.StatusUnauthorized
		if errors.Is(err, middleware.ErrSignatureExpired) {
			statusCode = http.StatusUnauthorized
		}
		abortWithError(c, statusCode, err)
		return
	}

	limitKey := c.ClientIP() + ":" + normalizeKey(namespace) + ":" + normalizeKey(className)
	limitResult := s.limiter.Check(limitKey, route.Class.Security.Limit)
	setRateLimitHeaders(c, limitResult)
	if !limitResult.Allowed {
		abortWithError(c, http.StatusTooManyRequests, fmt.Errorf("rate limit exceeded"))
		return
	}

	if err = s.validatePathFilter(namespace, className, route.Class.Security.PathFilter, rawObjectPath); err != nil {
		abortWithError(c, http.StatusForbidden, err)
		return
	}

	sourcePath, transformOptions, err := s.processor.ParseRequest(route.Class, rawObjectPath, c.Request.URL.Query())
	if err != nil {
		abortWithError(c, http.StatusBadRequest, err)
		return
	}

	if transformOptions.Enabled && s.imgproxy != nil {
		rule, exists := s.router.FileConversionRule(route.Class.FileConversion.Rule)
		if !exists {
			abortWithError(c, http.StatusInternalServerError, fmt.Errorf("file conversion rule %q not found", route.Class.FileConversion.Rule))
			return
		}

		imgResp, processErr := s.processWithImgproxy(c.Request, namespace, className, sourcePath, transformOptions, rule)
		if processErr != nil {
			abortWithError(c, http.StatusBadGateway, processErr)
			return
		}
		defer imgResp.Body.Close()

		for key, value := range route.Class.ResponseHeaders {
			c.Header(key, value)
		}

		for _, headerName := range []string{"Etag", "Last-Modified", "Cache-Control", "Content-Disposition", "Accept-Ranges", "Content-Type", "Content-Length"} {
			for _, value := range imgResp.Header.Values(headerName) {
				c.Header(headerName, value)
			}
		}

		if transformOptions.Enabled {
			c.Header("X-FileGate-Transform", engine.FormatTransformOptions(transformOptions))
		}
		c.Header("X-FileGate-Policy", route.Policy.Name)

		if c.Request.Method == http.MethodHead {
			c.Status(imgResp.StatusCode)
			return
		}

		c.Status(imgResp.StatusCode)
		if _, err = io.Copy(c.Writer, imgResp.Body); err != nil {
			_ = c.Error(fmt.Errorf("stream imgproxy response body: %w", err))
		}
		return
	}

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

	if transformOptions.Enabled {
		c.Header("X-FileGate-Transform", engine.FormatTransformOptions(transformOptions))
	}
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

// handleOriginFetch 用于给 imgproxy 拉取原始文件，不做转换处理。
func (s *Server) handleOriginFetch(c *gin.Context) {
	namespace := c.Param("namespace")
	className := c.Param("class")
	rawObjectPath := strings.TrimPrefix(c.Param("objectPath"), "/")
	if rawObjectPath == "" {
		abortWithError(c, http.StatusBadRequest, fmt.Errorf("object path is empty"))
		return
	}

	route, err := s.router.Resolve(namespace, className)
	if err != nil {
		abortWithError(c, http.StatusNotFound, err)
		return
	}

	if err = s.validatePathFilter(namespace, className, route.Class.Security.PathFilter, rawObjectPath); err != nil {
		abortWithError(c, http.StatusForbidden, err)
		return
	}

	classCfg := route.Class
	classCfg.FileConversion.Enabled = false
	sourcePath, _, err := s.processor.ParseRequest(classCfg, rawObjectPath, c.Request.URL.Query())
	if err != nil {
		abortWithError(c, http.StatusBadRequest, err)
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

func (s *Server) validatePathFilter(namespace, className string, cfg config.PathFilterConfig, objectPath string) error {
	key := normalizeKey(namespace) + ":" + normalizeKey(className)

	s.pathFilterMu.RLock()
	pathFilter := s.pathFilters[key]
	s.pathFilterMu.RUnlock()

	if pathFilter == nil {
		created, err := middleware.NewPathFilter(cfg)
		if err != nil {
			return err
		}

		s.pathFilterMu.Lock()
		if existing := s.pathFilters[key]; existing != nil {
			pathFilter = existing
		} else {
			s.pathFilters[key] = created
			pathFilter = created
		}
		s.pathFilterMu.Unlock()
	}

	return pathFilter.Validate(objectPath)
}

func abortWithError(c *gin.Context, statusCode int, err error) {
	setNoCacheHeaders(c)

	if imageName := errorImageName(statusCode); imageName != "" {
		if imageData, readErr := readErrorImage(imageName); readErr == nil {
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

	c.AbortWithStatusJSON(statusCode, gin.H{
		"error": err.Error(),
	})
}

func setNoCacheHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
}

func errorImageName(statusCode int) string {
	if statusCode == http.StatusNotFound {
		return "404.png"
	}

	if statusCode >= 400 && statusCode < 500 {
		return "400.png"
	}

	if statusCode >= 500 && statusCode < 600 {
		return "500.png"
	}

	return ""
}

func readErrorImage(imageName string) ([]byte, error) {
	var readErr error
	for _, imagePath := range candidateErrorImagePaths(imageName) {
		data, err := os.ReadFile(imagePath)
		if err == nil {
			return data, nil
		}
		readErr = err
	}

	if readErr == nil {
		readErr = fmt.Errorf("error image %q not found", imageName)
	}

	return nil, readErr
}

func candidateErrorImagePaths(imageName string) []string {
	candidates := []string{
		filepath.Join("assets", "res_code", imageName),
		filepath.Join("..", "assets", "res_code", imageName),
		filepath.Join("..", "..", "assets", "res_code", imageName),
	}

	if executablePath, err := os.Executable(); err == nil {
		executableDir := filepath.Dir(executablePath)
		candidates = append(candidates,
			filepath.Join(executableDir, "assets", "res_code", imageName),
			filepath.Join(executableDir, "..", "assets", "res_code", imageName),
		)
	}

	return dedupePaths(candidates)
}

func dedupePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, exists := seen[p]; exists {
			continue
		}
		seen[p] = struct{}{}
		result = append(result, p)
	}

	return result
}

func setRateLimitHeaders(c *gin.Context, result middleware.RateLimitResult) {
	if result.Limit > 0 {
		c.Header("X-RateLimit-Limit", strconv.Itoa(result.Limit))
	}

	if result.Limit > 0 || result.Remaining > 0 {
		c.Header("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))
	}

	if result.ResetAfter > 0 {
		resetSeconds := int(math.Ceil(result.ResetAfter.Seconds()))
		if resetSeconds < 0 {
			resetSeconds = 0
		}
		c.Header("X-RateLimit-Reset", strconv.Itoa(resetSeconds))
	}

	if result.Source != "" {
		c.Header("X-RateLimit-Source", result.Source)
	}
}

func normalizeKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
