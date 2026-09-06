# new-api rc.33 生产发布手册

## 目标与基线

- 目标版本：官方 `v1.0.0-rc.33`，commit `eb99ab1b40343c3317bb47981cccdbb2b159a5fa`。
- 当前代码提交：`df20673ab5acdb6fd0c52c2d24aa90eba51508bd`。
- 当前生产应用回滚基线：`wanchuan/new-api:responses-8eee45fd2-20260906`。
- 本次只替换 `new-api` 应用镜像；PostgreSQL、Redis、Grok2API、Sub2API 不重启。
- 官方 rc.33 仍将插件系统标记为实验性；OpenRouter 旧的任意 `-thinking` 别名行为已移除，发布前必须检查现有模型映射。

## 发布前门禁

1. 使用干净提交构建源码，不从脏工作区打包：

   ```sh
   git archive --format=tar.gz --output=source.tar.gz df20673ab5acdb6fd0c52c2d24aa90eba51508bd
   sha256sum source.tar.gz
   ```

2. 在与生产一致的 Docker builder 中执行：

   ```sh
   GOWORK=off go vet ./...
   GOWORK=off go test ./...
   (cd relaykit && GOWORK=off go vet ./... && GOWORK=off go test ./... && GOWORK=off go build ./...)
   ```

3. 前端执行：

   ```sh
   cd web
   bun install --frozen-lockfile
   bun run typecheck
   bun run test
   bun run lint:ci
   bun run format:check
   VITE_REACT_APP_VERSION=v1.0.0-rc.33+main.eb99ab1.chuanapi.20260906 bun run build
   ```

4. 构建并签名 immutable 镜像。将实际 digest 填入生产 `.env`，禁止使用 `latest`：

   ```dotenv
   NEW_API_IMAGE=wanchuan/new-api@sha256:REPLACE_NEW_API_DIGEST
   POSTGRES_IMAGE=postgres@sha256:REPLACE_POSTGRES_DIGEST
   REDIS_IMAGE=redis@sha256:REPLACE_REDIS_DIGEST
   SQL_DSN=postgresql://root:REPLACE_POSTGRES_PASSWORD@postgres:5432/new-api
   REDIS_CONN_STRING=redis://:REPLACE_REDIS_PASSWORD@redis:6379
   POSTGRES_PASSWORD=REPLACE_POSTGRES_PASSWORD
   REDIS_PASSWORD=REPLACE_REDIS_PASSWORD
   ```

   使用 `cosign verify` 校验签名，并保存 SBOM、镜像 digest、源码 SHA256 和构建日志。

5. 在隔离数据库执行恢复、双次迁移和回滚演练：

   ```sh
   bash ops/server/new-api-rc30-migration-drill.sh /path/to/rc33-drill
   bash ops/server/new-api-rc30-db-matrix.sh /path/to/rc33-matrix
   ```

   两个脚本文件名为历史兼容名称，默认目标已切换为 rc.33。必须核对资金四列、账本、任务、渠道、价格选项、索引和插件表前后不变。

6. 更新 `/etc/wanchuan/new-api-production-gates.env`。以下项目必须全部为 `PASS`：

   `GO_TEST`、`GO_RACE`、`RELAYKIT_BUILD`、`WEB_TYPECHECK`、`WEB_TEST`、`WEB_BUILD`、`WEB_LINT`、`WEB_FORMAT`、`PG_RESTORE_TWICE`、`MYSQL_MIGRATION_TWICE`、`SQLITE_MIGRATION_TWICE`、`RC26_APP_ROLLBACK`、`FUNDS_INVARIANTS`、`PLUGIN_REGISTRY`、`TASK_PRICING`、`CONTRACTS`、`UPSTREAM_REGRESSION_CHECK`。

## Canary 与切换

1. 使用 `ops/server/new-api-rc30-canary-start.sh` 启动 rc.33 canary，并确认 `/api/healthz` 与 `/api/readyz` 均返回 `{"success":true}`。
2. 启用现有 5 分钟 monitor，连续观察至少 72 小时和 800 次记录；每次记录必须为 running、healthy、restart=0、OOM=false、critical log=0，资金快照只能有一个版本。
3. 发布前检查：OpenAI Chat、Responses、Claude、Gemini、图片、音频、任务插件、登录/2FA/OAuth、Token、支付 webhook 重放和 Caddy 路由。
4. 运行生产门禁：

   ```sh
   bash ops/server/new-api-rc30-production-gate.sh /etc/wanchuan/new-api-production-gates.env
   ```

5. 仅在门禁通过后，用生产发布脚本更新 `/opt/middle/new-api/docker-compose.yml` 的 `new-api` 镜像字段。更新前保存 PostgreSQL custom dump、globals、Redis RDB、Compose、环境文件、依赖容器指纹和价格指纹。

## 发布后验收

```sh
curl -fsS https://chuanapi.com/api/healthz
curl -fsS https://chuanapi.com/api/readyz
curl -fsS https://chuanapi.com/api/status
curl -sS -o /dev/null -w '%{http_code}\n' https://chuanapi.com/public-api/status
curl -sS -o /dev/null -w '%{http_code}\n' https://chuanapi.com/dashboard/overview
curl -sS -o /dev/null -w '%{http_code}\n' https://chuanapi.com/v1/models
```

期望：healthz/readyz/status 为成功 JSON，public status 和 dashboard 为 200，未认证 `/v1/models` 为应用层 401 而不是边缘 HTML 错误页。记录版本、request id、响应类型、容器状态、重启次数和错误日志摘要。

## 回滚

只回滚应用镜像，不恢复数据库：

```sh
cp -a /opt/middle/state/new-api-deploy/rc33-eb99ab1/backup-*/docker-compose.yml /opt/middle/new-api/docker-compose.yml
docker compose --project-directory /opt/middle/new-api config -q
docker compose --project-directory /opt/middle/new-api up -d --no-deps --pull never --timeout 150 new-api
curl -fsS http://127.0.0.1:3000/api/status
```

若迁移已改变 schema 或资金对账不一致，停止切换并使用对应恢复演练产物；不得直接在生产执行破坏性回滚 SQL。

## 当前本地验证限制

当前 Windows 工作区已通过 Go/relaykit、前端 typecheck/test/lint/format/build、Compose YAML 和 Shell 静态检查；未在本机执行 Docker、真实 MySQL/PostgreSQL、cosign 或 72 小时 canary。上述门禁必须在生产构建主机或隔离 CI 完成后，才允许切换公网流量。
