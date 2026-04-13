package engine

import (
	"fmt"

	"filegate/config"
)

// Route 表示 namespace + class 的最终路由映射。
type Route struct {
	Namespace config.NamespaceConfig
	Class     config.ClassConfig
	Policy    config.BackendPolicy
}

type Router struct {
	routes          map[string]map[string]Route
	conversionRules map[string]config.FileConversionRule
}

func NewRouter(cfg *config.Config) (*Router, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	policyMap := make(map[string]config.BackendPolicy, len(cfg.BackendPolicies))
	for _, p := range cfg.BackendPolicies {
		policyMap[normalizeKey(p.Name)] = p
	}

	routes := make(map[string]map[string]Route, len(cfg.Namespaces))
	for _, ns := range cfg.Namespaces {
		policy, exists := policyMap[normalizeKey(ns.BackendPolicy)]
		if !exists {
			return nil, fmt.Errorf("namespace %q references unknown backend policy %q", ns.Name, ns.BackendPolicy)
		}

		classMap := make(map[string]Route, len(ns.Class))
		for _, cls := range ns.Class {
			classMap[normalizeKey(cls.Name)] = Route{
				Namespace: ns,
				Class:     cls,
				Policy:    policy,
			}
		}

		routes[normalizeKey(ns.Name)] = classMap
	}

	conversionRules := make(map[string]config.FileConversionRule, len(cfg.FileConversionRules))
	for _, rule := range cfg.FileConversionRules {
		conversionRules[normalizeKey(rule.Name)] = rule
	}

	return &Router{
		routes:          routes,
		conversionRules: conversionRules,
	}, nil
}

func (r *Router) Resolve(namespace, className string) (Route, error) {
	ns, exists := r.routes[normalizeKey(namespace)]
	if !exists {
		return Route{}, fmt.Errorf("namespace %q not found", namespace)
	}

	route, exists := ns[normalizeKey(className)]
	if !exists {
		return Route{}, fmt.Errorf("class %q not found in namespace %q", className, namespace)
	}

	return route, nil
}

func (r *Router) FileConversionRule(name string) (config.FileConversionRule, bool) {
	rule, exists := r.conversionRules[normalizeKey(name)]
	return rule, exists
}
