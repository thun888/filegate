package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"filegate/config"
	"filegate/internal/engine"
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
	Blur              int
	Format            string
	MaxSourceFileSize int64
}

func newImgproxyClient(cfg config.ImgproxyConfig) (*imgproxyClient, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, nil
	}

	parsed, err := url.Parse(strings.TrimSpace(cfg.URL))
	if err != nil {
		return nil, fmt.Errorf("parse imgproxy url: %w", err)
	}

	return &imgproxyClient{
		baseURL:    parsed,
		signature:  cfg.Signature,
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}, nil
}

func (s *Server) processWithImgproxy(req *http.Request, namespace, className, sourcePath string, opts engine.TransformOptions, rule config.FileConversionRule) (*http.Response, error) {
	sourceURL, err := s.buildServerOriginURL(namespace, className, sourcePath)
	if err != nil {
		return nil, err
	}

	maxSourceFileSize, err := parseByteSize(rule.MaxFileSize)
	if err != nil {
		return nil, fmt.Errorf("invalid max_file_size %q: %w", rule.MaxFileSize, err)
	}

	width := 0
	if opts.WidthSet {
		width = opts.Width
	}

	height := 0
	if opts.HeightSet {
		height = opts.Height
	}

	format := ""
	if opts.FormatSet {
		format = opts.Format
	}

	return s.imgproxy.Do(req.Context(), imgproxyRequest{
		Method:            req.Method,
		SourceURL:         sourceURL,
		Width:             width,
		Height:            height,
		Blur:              opts.Blur,
		Format:            format,
		MaxSourceFileSize: maxSourceFileSize,
	})
}

func (s *Server) buildServerOriginURL(namespace, className, sourcePath string) (string, error) {
	domain := strings.TrimSpace(s.cfg.System.Server.Domain)
	if domain == "" {
		return "", fmt.Errorf("system.server.domain is empty")
	}

	if !strings.HasPrefix(domain, "http://") && !strings.HasPrefix(domain, "https://") {
		domain = "http://" + domain
	}

	base, err := url.Parse(domain)
	if err != nil {
		return "", fmt.Errorf("parse system.server.domain: %w", err)
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

	processing := []string{
		fmt.Sprintf("w:%d", req.Width),
		fmt.Sprintf("h:%d", req.Height),
	}
	if req.MaxSourceFileSize > 0 {
		processing = append(processing, fmt.Sprintf("msfs:%d", req.MaxSourceFileSize))
	}
	if req.Blur > 0 {
		processing = append(processing, fmt.Sprintf("bl:%d", req.Blur))
	}
	if strings.TrimSpace(req.Format) != "" {
		processing = append(processing, "f:"+strings.ToLower(strings.TrimPrefix(strings.TrimSpace(req.Format), ".")))
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
		return nil, fmt.Errorf("imgproxy returned status %d", resp.StatusCode)
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

var byteSizePattern = regexp.MustCompile(`(?i)^([0-9]+)\s*([kmgt]?i?b?)?$`)

func parseByteSize(raw string) (int64, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, nil
	}

	match := byteSizePattern.FindStringSubmatch(v)
	if len(match) != 3 {
		return 0, fmt.Errorf("invalid size string")
	}

	baseValue, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0, err
	}

	unit := strings.ToLower(match[2])
	multiplier := int64(1)
	switch unit {
	case "", "b":
		multiplier = 1
	case "k", "kb", "kib":
		multiplier = 1024
	case "m", "mb", "mib":
		multiplier = 1024 * 1024
	case "g", "gb", "gib":
		multiplier = 1024 * 1024 * 1024
	case "t", "tb", "tib":
		multiplier = 1024 * 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("unsupported size unit %q", unit)
	}

	return baseValue * multiplier, nil
}
