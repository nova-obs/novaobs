# NovaAPM Kubernetes 部署

该清单部署 NovaAPM 前后端。后端仅通过集群内 `ClusterIP` 提供服务；前端 Nginx 将 `/api/` 同源代理到后端，并通过固定 NodePort `30080` 暴露。

除镜像构建命令外，以下 `kubectl` 命令均在本目录执行。

## 1. 构建并推送镜像

在工作区根目录执行。两个 `Makefile` 默认使用 `linux/amd64`，可直接构建并推送当前部署清单使用的 `0.1.1` 镜像：

```bash
make -C novaapm docker-build-push
make -C novaapm-fe docker-build-push
```

只构建到本地 Docker 时使用 `docker-build`，构建后可用 `docker-inspect` 确认结果为 `linux/amd64`：

```bash
make -C novaapm docker-build docker-inspect
make -C novaapm-fe docker-build docker-inspect
```

镜像仓库、版本和目标平台都可以覆盖；例如：

```bash
make -C novaapm docker-build-push REGISTRY=<registry> TAG=0.1.2
make -C novaapm-fe docker-build-push REGISTRY=<registry> TAG=0.1.2
```

若覆盖镜像地址或版本，需要同步修改 `20-backend-deployment.yaml` 和 `30-frontend-deployment.yaml`。生产环境应使用不可变版本或镜像摘要，不要使用 `latest`。`PLATFORM` 默认不要修改；只有目标集群不是 AMD64 时才显式覆盖。

构建基础镜像的 tag 也会随上游更新；正式发布时应在验证和扫描后将 Dockerfile 中的基础镜像锁定到 digest。

## 2. 创建后端 Secret

MongoDB 必须已经可被集群访问。优先使用集群已有的外部 Secret 管理方案。若暂时手工创建，请把真实值写入权限为 `0600` 的临时文件，避免进入 shell history，并且不要提交该文件：

```bash
kubectl apply -f 00-namespace.yaml
kubectl -n novaapm-system create secret generic novaapm-backend-secrets \
  --from-env-file=/secure/path/novaapm-secrets.env
```

临时文件需要包含 `mongodb-uri`、严格 32 字节的 `encryption-key`、独立的高强度 `alert-ingest-token`、`bootstrap-admin-username`、`bootstrap-admin-password` 与可选的 `bootstrap-admin-display-name`。

若数据库中已经存在管理员，也必须保留一组 `bootstrap-admin-*` 配置作为 release 模式的运行时平台超级管理员。当前版本尚未把该超级主体固化为可脱离启动配置的角色绑定；删除用户名会使该账号失去超级主体身份，只删除密码则会导致后端启动失败。需要换管理员或密码时，应原子更新对应 Secret 并滚动重启。Secret 创建成功后立即安全清理临时文件。

编辑 `10-backend-config.yaml` 的 `logs-agent-opamp-endpoint`，填写 Collector 可访问的地址。当前 NodePort 入口示例为 `ws://<节点 IP>:30080/v1/opamp`；若 Collector 暂不接入 OpAMP，可保持为空。生产 TLS 入口应使用 `wss://`。

## 3. 部署与验证

```bash
kubectl apply --dry-run=client -k .
kubectl apply -k .
kubectl -n novaapm-system rollout status deployment/novaapm-backend
kubectl -n novaapm-system rollout status deployment/novaapm-frontend
kubectl -n novaapm-system get service novaapm-frontend
```

访问地址：`http://<任一节点 IP>:30080`。健康检查可访问：

```bash
curl --fail http://<节点 IP>:30080/healthz
curl --fail http://<节点 IP>:30080/api/v1/health
```

NodePort 会在各节点地址上开放端口，只适合受控内网的临时入口。请通过节点防火墙或安全组限制来源；正式生产入口应迁移到启用 TLS 的 Ingress 或 Gateway。迁移时还必须配置可信代理链和原始 HTTPS 协议识别，确保后端会话 Cookie 能够设置 `Secure`，不能只在入口终止 TLS。

后端当前 `/api/v1/health` 是进程级浅探针，不包含 MongoDB 连通性检查，因此清单中的 readiness 只能表达 HTTP 服务是否可响应。
