package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/thun888/filegate/config"
	"github.com/thun888/filegate/internal/engine"
	"github.com/thun888/filegate/internal/utils"
)

type imgproxyClient struct {
	baseURL    *url.URL
	signature  config.ImgproxySignatureConfig
	httpClient *http.Client
}

type imgproxyRequest struct {
	Method            string
	SourceURL         string
	Width             int
	Height            int
	Blur              float64
	Quality           int
	Format            string
	MaxSourceFileSize int64
	Watermark         config.WatermarkConfig
}

// imgproxyStatusError 携带 imgproxy 返回的 HTTP 状态码，供调用方区分处理（如 404 透传）。
type imgproxyStatusError struct {
	code int
}

func (e *imgproxyStatusError) Error() string {
	return fmt.Sprintf("imgproxy returned status %d", e.code)
}

// ErrImgproxyNoProcessingOptions 表示请求没有任何可下发的处理选项
// （宽高、质量、模糊、格式、水印均为空或 0）。
var ErrImgproxyNoProcessingOptions = errors.New("no imgproxy processing options")

func newImgproxyClient(cfg config.ImgproxyConfig) (*imgproxyClient, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, nil
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}

	parsed, err := url.Parse(strings.TrimSpace(cfg.URL))
	if err != nil {
		return nil, fmt.Errorf("parse imgproxy url: %w", err)
	}

	return &imgproxyClient{
		baseURL:    parsed,
		signature:  cfg.Signature,
		httpClient: &http.Client{Timeout: timeout},
	}, nil
}

func (s *Server) processWithImgproxy(req *http.Request, namespace, className, sourcePath string, opts engine.TransformOptions, rule config.FileConversionRule) (*http.Response, error) {
	sourceURL, err := s.buildServerOriginURL(namespace, className, sourcePath)
	if err != nil {
		return nil, err
	}

	maxSourceFileSize, err := utils.ParseByteSize(rule.MaxFileSize)
	if err != nil {
		return nil, fmt.Errorf("invalid max_file_size %q: %w", rule.MaxFileSize, err)
	}

	width := opts.Width
	height := opts.Height

	return s.imgproxy.Do(req.Context(), imgproxyRequest{
		Method:            req.Method,
		SourceURL:         sourceURL,
		Width:             width,
		Height:            height,
		Blur:              opts.Blur,
		Quality:           opts.Quality,
		Format:            opts.Format,
		MaxSourceFileSize: maxSourceFileSize,
		Watermark:         rule.Watermark,
	})
}

func (s *Server) buildServerOriginURL(namespace, className, sourcePath string) (string, error) {
	baseURL := strings.TrimSpace(s.cfg.System.Server.BaseURL)
	if baseURL == "" {
		return "", fmt.Errorf("system.server.base_url is empty")
	}

	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "http://" + baseURL
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse system.server.base_url: %w", err)
	}

	cleanPath := path.Clean("/" + strings.TrimLeft(sourcePath, "/"))
	if cleanPath == "." || cleanPath == "/" {
		return "", fmt.Errorf("invalid source path %q", sourcePath)
	}

	u := *base
	basePath := strings.TrimRight(u.Path, "/")
	u.Path = basePath + "/origin/" + namespace + "/" + className + cleanPath
	u.RawQuery = ""

	return u.String(), nil
}

func (c *imgproxyClient) Do(ctx context.Context, req imgproxyRequest) (*http.Response, error) {
	if c == nil {
		return nil, fmt.Errorf("imgproxy is not configured")
	}

	// 只下发显式设置过的选项：
	// - w/h 为 0 表示不调整尺寸，不发送 resize 选项，由 imgproxy 保持原尺寸；
	// - quality 为 0 表示不调整质量；
	// - blur 为 0 表示不模糊。
	processing := make([]string, 0, 6)
	if req.Width > 0 {
		processing = append(processing, fmt.Sprintf("w:%d", req.Width))
	}
	if req.Height > 0 {
		processing = append(processing, fmt.Sprintf("h:%d", req.Height))
	}
	if req.MaxSourceFileSize > 0 {
		processing = append(processing, fmt.Sprintf("msfs:%d", req.MaxSourceFileSize))
	}
	if req.Blur > 0 {
		processing = append(processing, "bl:"+strconv.FormatFloat(req.Blur, 'f', -1, 64))
	}
	if req.Quality > 0 {
		processing = append(processing, fmt.Sprintf("q:%d", req.Quality))
	}
	if strings.TrimSpace(req.Format) != "" {
		processing = append(processing, "f:"+strings.ToLower(strings.TrimPrefix(strings.TrimSpace(req.Format), ".")))
	}
	if req.Watermark.Enabled {
		// wm:opacity:position:x_offset:y_offset:scale（需要在 imgproxy 端配置水印图）
		processing = append(processing, strings.Join([]string{
			"wm",
			strconv.FormatFloat(req.Watermark.Opacity, 'f', -1, 64),
			strings.ToLower(strings.TrimSpace(req.Watermark.Position)),
			strconv.FormatFloat(req.Watermark.XOffset, 'f', -1, 64),
			strconv.FormatFloat(req.Watermark.YOffset, 'f', -1, 64),
			strconv.FormatFloat(req.Watermark.Scale, 'f', -1, 64),
		}, ":"))
	}

	// 没有任何可下发的处理选项（宽高、质量、模糊、格式、水印全为空/0）时直接报错，
	// 避免向 imgproxy 发出无意义的直通请求
	if len(processing) == 0 {
		return nil, ErrImgproxyNoProcessingOptions
	}

	sourceEncoded := base64.RawURLEncoding.EncodeToString([]byte(req.SourceURL))
	unsignedPath := "/" + strings.Join(processing, "/") + "/" + sourceEncoded

	prefix := "unsafe"
	if c.signature.Enabled {
		sig, err := signImgproxyPath(unsignedPath, c.signature.Key, c.signature.Salt)
		if err != nil {
			return nil, err
		}
		prefix = sig
	}

	u := *c.baseURL
	u.Path = strings.TrimRight(c.baseURL.Path, "/") + "/" + prefix + unsignedPath

	method := http.MethodGet
	if req.Method == http.MethodHead {
		method = http.MethodHead
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build imgproxy request: %w", err)
	}
	httpReq.Header.Set("User-Agent", "FileGate/1.0")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request imgproxy: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		defer resp.Body.Close()
		return nil, &imgproxyStatusError{code: resp.StatusCode}
	}

	return resp, nil
}

func signImgproxyPath(unsignedPath, keyRaw, saltRaw string) (string, error) {
	key := decodeSecret(keyRaw)
	salt := decodeSecret(saltRaw)
	if len(key) == 0 || len(salt) == 0 {
		return "", fmt.Errorf("imgproxy signature enabled but key/salt is empty")
	}

	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(salt)
	_, _ = mac.Write([]byte(unsignedPath))

	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func decodeSecret(raw string) []byte {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil
	}

	if decoded, err := hex.DecodeString(v); err == nil {
		return decoded
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(v); err == nil {
		return decoded
	}
	if decoded, err := base64.StdEncoding.DecodeString(v); err == nil {
		return decoded
	}

	return []byte(v)
}
