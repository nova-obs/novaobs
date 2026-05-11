package servicecatalog

// 服务目录 HTTP handler 由 internal/httpapi 组合 Repository 后注册。
// 这里保留领域侧 handler 边界，避免后续把服务目录能力散落到其他模块。
