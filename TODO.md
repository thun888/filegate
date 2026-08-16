##  几个关键细节优化

- **Duration 解析**：YAML 解析器默认支持 `5s`。如果报错，请确保你的结构体字段是 `time.Duration`，或者在解析前先通过 Viper 处理。
- **默认值处理**：在 `LoadConfig` 函数开始时，可以先给结构体赋默认值，YAML 中没有定义的字段就会保留默认值。
- **Path Filter 安全性**：在读取 `deny_patterns` 后，建议在代码中预编译为 `regexp.Regexp` 以提高性能。
- **内存映射**：对于 `namespaces` -> `class` 这种结构，在程序启动后，建议将其转换为 `map[string]map[string]Class`，这样在路由请求时可以实现 $O(1)$ 的查找速度，而不是每次去遍历切片。

添加预设组
max_file_size换算成字节