package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/thun888/filegate/config"
)

var (
	ErrRefererDenied    = errors.New("referer denied")
	ErrSignatureInvalid = errors.New("signature invalid")
	ErrSignatureExpired = errors.New("signature expired")
)

// VerifyReferer 基于 Referer 白名单进行校验。
func VerifyReferer(r *http.Request, cfg config.ReferCheckConfig) error {
	if !cfg.Enabled {
		return nil
	}

	referer := strings.TrimSpace(r.Referer())
	if referer == "" {
		return ErrRefererDenied
	}

	for _, allowed := range cfg.AllowedReferers {
		allowed = strings.TrimSpace(allowed)
		if allowed == "" {
			continue
		}
		if strings.HasPrefix(referer, allowed) {
			return nil
		}
	}

	return ErrRefererDenied
}

// VerifySignature 校验 exp/sign 查询参数。
func VerifySignature(r *http.Request, cfg config.SignatureConfig) error {
	if !cfg.Enabled {
		return nil
	}

	query := r.URL.Query()
	expRaw := strings.TrimSpace(query.Get("exp"))
	signRaw := strings.TrimSpace(query.Get("sign"))
	if expRaw == "" || signRaw == "" {
		return ErrSignatureInvalid
	}

	exp, err := strconv.ParseInt(expRaw, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid exp value %q", expRaw)
	}

	// 过期检测
	now := time.Now().Unix()
	if now > exp {
		return ErrSignatureExpired
	}
	// 如果配置了过期窗口，检查 exp 是否在合理范围内
	if cfg.Expire > 0 && exp-now > cfg.Expire {
		return fmt.Errorf("signature expiration exceeds configured window")
	}

	canonical := buildCanonicalString(r.Method, r.URL.Path, query, expRaw)
	mac := hmac.New(sha256.New, []byte(cfg.Secret))
	_, _ = mac.Write([]byte(canonical))
	expected := hex.EncodeToString(mac.Sum(nil))

	received := strings.ToLower(signRaw)

	// 使用ConstantTimeCompare防止时序攻击
	// 但是对于公开的图片资源来讲有没有必要呢？
	if subtle.ConstantTimeCompare([]byte(received), []byte(expected)) != 1 {
		return ErrSignatureInvalid
	}

	return nil
}

// 构建规范字符串：
// GET
// /api/v1/query
// a=1&b=2
// 1703520000
func buildCanonicalString(method, requestPath string, query url.Values, exp string) string {
	canonicalQuery := url.Values{}
	for key, values := range query {
		// 筛除 sign 和 exp 参数，其他参数按原样保留
		if strings.EqualFold(key, "sign") || strings.EqualFold(key, "exp") {
			continue
		}
		for _, value := range values {
			canonicalQuery.Add(key, value)
		}
	}

	return strings.ToUpper(method) + "\n" + requestPath + "\n" + canonicalQuery.Encode() + "\n" + exp
}
