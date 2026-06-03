# NovaObs Backend

## 本地启动

后端启动会校验 Secret 加密密钥。启动前需要设置 32 字节的 `NOVAOBS_SECRET_KEY`，用于 Kubeconfig、证书私钥、Token 等敏感数据的本地 AES-GCM 加密。

```bash
export NOVAOBS_SECRET_KEY="12345678901234567890123456789012"
go run ./cmd/server
```

不要把生产密钥写入 `configs/config.yaml` 或提交到代码仓库；生产环境应通过环境变量或密钥管理系统注入。
