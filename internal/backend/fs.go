package backend

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thun888/filegate/config"
)

type fsBackend struct {
	name     string
	rootPath string
}

func newFSBackend(cfg config.BackendConfig) (Backend, error) {
	rootPath := strings.TrimSpace(cfg.Config.RootPath)
	if rootPath == "" {
		return nil, fmt.Errorf("fs backend %q requires config.root_path", cfg.Name)
	}

	absRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, fmt.Errorf("resolve fs root path for backend %q: %w", cfg.Name, err)
	}

	return &fsBackend{name: cfg.Name, rootPath: absRoot}, nil
}

func (b *fsBackend) Name() string {
	return b.name
}

func (b *fsBackend) Fetch(_ context.Context, objectPath string) (*Object, error) {
	resolvedPath, err := b.resolvePath(objectPath)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(resolvedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file %q not found in backend %q", objectPath, b.name)
		}
		return nil, fmt.Errorf("open %q from backend %q: %w", objectPath, b.name, err)
	}

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("stat %q from backend %q: %w", objectPath, b.name, err)
	}

	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(resolvedPath)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	headers := make(http.Header)
	headers.Set("Last-Modified", info.ModTime().UTC().Format(time.RFC1123))

	return &Object{
		Body:        f,
		ContentType: contentType,
		Size:        info.Size(),
		Headers:     headers,
	}, nil
}

func (b *fsBackend) resolvePath(objectPath string) (string, error) {
	// 防止路径过长影响处理效率，或者说被利用来耗费资源
	const MaxPathLength = 4096
	if len(objectPath) > MaxPathLength {
		return "", fmt.Errorf("path too long")
	}

	if strings.TrimSpace(objectPath) == "" {
		return "", fmt.Errorf("object path is empty")
	}
	// 清理路径，防止路径穿越攻击
	cleanPath := filepath.Clean(filepath.FromSlash("/" + strings.TrimLeft(objectPath, "/")))
	relPath := strings.TrimPrefix(cleanPath, string(filepath.Separator))
	fullPath := filepath.Join(b.rootPath, relPath)
	// 确保 fullPath 在 b.rootPath 内
	if !isSubPath(b.rootPath, fullPath) {
		return "", fmt.Errorf("path %q escapes backend root", objectPath)
	}

	return fullPath, nil
}

func isSubPath(basePath, targetPath string) bool {
	rel, err := filepath.Rel(basePath, targetPath)
	if err != nil {
		return false
	}

	if rel == "." {
		return true
	}

	parentPrefix := ".." + string(filepath.Separator)
	return rel != ".." && !strings.HasPrefix(rel, parentPrefix)
}
