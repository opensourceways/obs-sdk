// Package env 提供通用字段环境变量解析辅助。
//
// 四语言 SDK 统一读同一套 OBS_* 环境变量（见 spec/common-fields.md），
// 保证部署配置模板不因语言而异。解析优先级：显式参数 > 环境变量 > 内置默认。
package env

import (
	"os"
)

const (
	envService   = "OBS_SERVICE"
	envEnv       = "OBS_ENV"
	envInstance  = "OBS_INSTANCE"
	envCommunity = "OBS_COMMUNITY"
)

// Service 返回服务名：explicit 非空优先，否则读 OBS_SERVICE，再否则 "unknown"。
func Service(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if v := os.Getenv(envService); v != "" {
		return v
	}
	return "unknown"
}

// Env 返回部署环境：explicit 非空优先，否则读 OBS_ENV，再否则 "unknown"。
func Env(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if v := os.Getenv(envEnv); v != "" {
		return v
	}
	return "unknown"
}

// Instance 返回实例标识：explicit 非空优先，否则读 OBS_INSTANCE，再否则 hostname。
func Instance(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if v := os.Getenv(envInstance); v != "" {
		return v
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "unknown"
}

// Community 返回社区：explicit 非空优先，否则读 OBS_COMMUNITY，再否则 "unknown"。
func Community(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if v := os.Getenv(envCommunity); v != "" {
		return v
	}
	return "unknown"
}
