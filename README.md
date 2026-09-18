# dataworks-cli

阿里云 DataWorks Data Studio 自动化查数 CLI（Go，无第三方依赖）。

## 安装

```bash
go build -o dataworks-cli .
```

## 准备登录态

配置 Cookie 后，`x-csrf-token` 会自动从 Cookie 里的 `csrf_token=` 提取。

```bash
./dataworks-cli config --set-cookie 'currentRegionId=cn-hangzhou; csrf_token=xxxx; ...'

# 或从文件读取（避免进 shell history）
./dataworks-cli config --set-cookie --file cookie.txt
```

工作区参数（按需覆盖）：

```bash
./dataworks-cli config set \
  --project-id 672230 \
  --data-source-id 518134 \
  --resource-group Serverless_res_group_212862176241921_808082136900256 \
  --cu 0.25
```

配置保存在 `~/.dataworks-cli/config.json`（权限 600）。
环境变量 `DATAWORKS_CLI_CONFIG` 可覆盖路径。

## 查数

```bash
# SQL 文件
./dataworks-cli query -f query.sql

# 行内 SQL
./dataworks-cli query -q 'SELECT * FROM bdprd.ods_order_db.t_trade_order_item LIMIT 10;'

# 带参数（paramMap 的 key 对应 SQL 里的 ${变量名}）
./dataworks-cli query -f query.sql --param bizdate=2026-09-17

# 只看结果，不打印进度
./dataworks-cli query -f query.sql --quiet

# 输出格式：auto(默认，能识别表格就打表格) | table | json | raw
./dataworks-cli query -f query.sql --format json
```

`query.sql`：

```sql
select * from bdprd.ods_member_db.t_member_apple where dt = '${bizdate}'
```

> 参数化 SQL 建议放文件里。用 `-q '...'` 时 `${bizdate}` 会被 shell 展开，务必用单引号。


## 接口与状态语义（实测）

| 接口 | 用途 |
| --- | --- |
| `POST /ide/createExecutorJobV3` | 创建任务，`data.jobCode` |
| `GET /v1/getExecutorJobResult` | 拿结果集，**无 status 字段** |
| `GET /ide/getExecutorJobLog` | 任务日志，**唯一可靠的状态源** |

`getExecutorJobResult` 的返回值（实测）：

| 返回 | 含义 |
| --- | --- |
| `code=208` / `data=null` | 执行中 |
| `headerList=[{name:"Error"}]`, `bodyList=[["Result Not Exist"]]` | 还没出结果（哨兵，非真错误） |
| `headerList` 非空 | 成功（`bodyList` 为空即 0 行） |
| `headerList` 为空 | 失败或 DDL 空结果 → 查 log 区分 |

`getExecutorJobLog` 的 `data.content` 关键词：

| 关键词 | 含义 |
| --- | --- |
| `Current task status:RUNNING` | 执行中 |
| `Run sql Succeed` / `SUCCEED: task cost time` | 成功 |
| `FAILED:` / `Shell run failed` / `Current task status:ERROR` | 失败 |

所以 `query` 的流程是：轮询 result，遇到「空 header」再查 log 判定，失败时把日志尾打到 stderr。

## 调试

```bash
# 只打印将发送的 createExecutorJobV3 请求体，不真正提交
./dataworks-cli query -f query.sql --dry-run

# 手动分步
./dataworks-cli create -f query.sql
./dataworks-cli result unified-xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx --raw
./dataworks-cli log    unified-xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx --raw

# 查完顺带打日志
./dataworks-cli query -f query.sql --log
```

## 结构

| 文件 | 职责 |
| --- | --- |
| `main.go` | 装配 CLI |
| `cli.go` | 命令解析、轮询与输出 |
| `client.go` | 三个 HTTP 接口 + 请求体构造 |
| `config.go` | 配置读写 |
| `sqlutil.go` | 自动补 `SET odps.namespace.schema=TRUE ;` |
| `jsonutil.go` | 结果/状态解析与表格渲染 |
| `helpers.go` | 通用工具 |
