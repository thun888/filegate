package engine

import (
	"fmt"

	"github.com/thun888/filegate/config"
	"github.com/thun888/filegate/internal/middleware"
)

// Route 表示 namespace + class 的最终路由映射。
type Route struct {
	Namespace config.NamespaceConfig
	Class     config.ClassConfig
	Policy    config.BackendPolicy

	// PathFilter 是启动时按类预编译好的路径过滤器，请求处理时直接复用，
	PathFilter *middleware.PathFilter
}

type Router struct {
	routes          map[string]map[string]Route
	conversionRules map[string]config.FileConversionRule
}

// NewRouter 根据配置创建路由器实例。
// 它解析配置中的 BackendPolicies、Namespaces 和 FileConversionRules，
// 构建路由映射表用于快速查找。
func NewRouter(cfg *config.Config) (*Router, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	policyMap := make(map[string]config.BackendPolicy, len(cfg.BackendPolicies))
	for _, p := range cfg.BackendPolicies {
		policyMap[config.NormalizeKey(p.Name)] = p
	}

	routes := make(map[string]map[string]Route, len(cfg.Namespaces))
	for _, ns := range cfg.Namespaces {
		policy := policyMap[config.NormalizeKey(ns.BackendPolicy)]

		classMap := make(map[string]Route, len(ns.Class))
		for _, cls := range ns.Class {
			pathFilter, err := middleware.NewPathFilter(cls.Security.PathFilter)
			if err != nil {
				return nil, fmt.Errorf("build path filter for %q/%q: %w", ns.Name, cls.Name, err)
			}

			classMap[config.NormalizeKey(cls.Name)] = Route{
				Namespace:  ns,
				Class:      cls,
				Policy:     policy,
				PathFilter: pathFilter,
			}
		}

		routes[config.NormalizeKey(ns.Name)] = classMap
	}

	conversionRules := make(map[string]config.FileConversionRule, len(cfg.FileConversionRules))
	for _, rule := range cfg.FileConversionRules {
		conversionRules[config.NormalizeKey(rule.Name)] = rule
	}

	return &Router{
		routes:          routes,
		conversionRules: conversionRules,
	}, nil
}

// Resolve 根据 namespace 和 className 查找对应的路由配置。
// 返回匹配的 Route，如果 namespace 或 class 不存在则返回错误。
func (r *Router) Resolve(namespace, className string) (Route, error) {
	ns, exists := r.routes[config.NormalizeKey(namespace)]
	if !exists {
		return Route{}, fmt.Errorf("namespace %q not found", namespace)
	}

	route, exists := ns[config.NormalizeKey(className)]
	if !exists {
		return Route{}, fmt.Errorf("class %q not found in namespace %q", className, namespace)
	}

	return route, nil
}

// FileConversionRule 根据名称查找文件转换规则。
func (r *Router) FileConversionRule(name string) config.FileConversionRule {
	return r.conversionRules[config.NormalizeKey(name)]
}

// Routes 返回内部路由表（namespace -> class -> Route），仅供启动时的调试输出使用。
func (r *Router) Routes() map[string]map[string]Route {
	return r.routes
}
