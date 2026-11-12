# kos-notification

KOS 系统应用：把事件与状态变化按订阅投递到外部渠道，并为每次投递尝试保留一条记录。本仓是该产品的独立扩展包——声明渠道、订阅与投递记录三个对象类和一个页面，经 kos-sdk 打包，以系统组件单独装载与卸载。

- 设计：`tsic/kos-design` 的 `kos-notification/`（范围、契约、验收、决策记录）。

## 贡献

| 贡献 | 声明 |
|---|---|
| 对象类 `Channel`、`Subscription`、`DeliveryRecord` | API 组 `notification.kos.tsic.top/v1`，集群作用域 |
| 控制器 `delivery` | 只写 `DeliveryRecord`；读核心组 `events` |
| 页面 `notification` | 渠道、订阅、投递记录三处展示项 |
| 对象角色 | 渠道与订阅：Owner 可写；投递记录：无用户角色可写，由控制器写入 |
| 卸载保留 | 三个对象类均 `retain` |

渠道与订阅的创建、修改与删除走控制面 API 的对象路由（`/api/v1/resources/<Kind>`），页面声明这三类入口；投递记录没有用户入口。

渠道凭据只有一个写入入口——随渠道配置一起提交（`Writers.WriteChannel`），读取只返回是否已设置；凭据不出现在渠道与订阅对象、投递记录、日志、审计、组件状态与对象投影中（`KOSD-C-14`）。

## 产物结构

`packaging.Pack` 产出的归档只有固定条目：

- `manifest.json`：扩展声明（`KOSD-C-18` 的字段集合，SDK 校验；不新增字段、缺项不补默认值）
- `digest`：覆盖清单的摘要；本产品没有 UI 资产，摘要只覆盖清单
- `assets/…`：本产品没有

## 打包

```sh
make pack
# 等价于
go run ./cmd/kos-notification-pack -out dist/kos-notification-1.0.0.tar.gz
```

命令打印归档路径与 `digest: sha256:…`；装载请求把该摘要写进 `SystemComponent.spec.package.digest`。

## 装载与卸载

装载由创建系统组件发起：`spec.package.reference` 指向归档，`spec.package.digest` 是打包打印的摘要。内核按 `KOSD-C-17` 校验摘要、读声明并激活贡献；停用按 `KOSD-C-28` 撤下页面与对象类并保留对象；卸载按 `KOSD-C-29` 的声明保留数据并列入遗留清单。

当前内核装载路径把工件字节直接当声明文档解析（`pkg/extension.Parse`）、对整份工件取 sha256（`DigestOf`），而 SDK 包是含清单与资产的归档、摘要按条目分帧计算。两者不对齐时装载被拒绝，属跨仓接口对齐：本仓不新建安装通道、不加产品专用字段、不改 kosd。可执行的证据在 `tests/acceptance/KOSD-TC-155`。

## 跨仓接口落差（上报，不在本仓修补）

1. **装载路径**：内核把工件当声明文档解析、按整份工件 sha256 校验；SDK 包是归档且摘要分帧覆盖清单，装载被拒绝。
2. **事件读取声明**：控制器读核心组的 `Event`，而 SDK 的读范围要求 `apiGroup` 非空，核心组（空字符串）无法命名。清单因此不带 `permissions.read`，也没有用占位组冒充；内核侧的声明形状（`GroupKind` 空组即核心组）支持这一读取。
3. **记录写权限**：清单以 `objectRoles: {DeliveryRecord: []}` 声明没有用户角色可写，但现内核把空的写者列表当作「未声明」，未声明的扩展类由 Owner 可写（`pkg/extension/registry.go`、`pkg/access/policy.go`）。产品自己的写入路径拒绝任何用户写记录（`AllowWrite`），内核侧的强制需另行补齐。
4. **页面写入口的产品校验**：页面写入口走通用对象路由 `/api/v1/resources/<Kind>`，内核只校验对象身份、不解释扩展 spec（`pkg/controlplane/objects.go` 的 `normalizeExtension`），也没有调用产品写路径的钩子；`config.schema.channels` 同样没有框架消费者（`pkg/pages/form.go` 的 `FormOf` 读的是应用 `ConfigField`）。因此「不支持的渠道类型在创建与修改时被拒绝」与「凭据随渠道配置写入」在产品声明面上没有强制点，校验目前只在 `Writers.WriteChannel` / `Writers.WriteSubscription` 生效。

## 测试

```sh
go build ./... && go vet ./... && go test ./...
```

`tests/acceptance/harness` 是按 `KOSD-C-17`/`C-28`/`C-29` 规则搭的 fixture，验证声明的贡献与启停、保留语义；它不是安装通道。`kosd` 只作为测试依赖，用来核对页面形状、框架版本范围与内核装载路径的行为。

| 用例 | 判据 |
|---|---|
| `tests/acceptance/NOTIFY-TC-3` | 创建入口的类型列表与写入接受的一致；不支持的类型被拒绝并给出原因；订阅目标渠道集合可读；页面三类展示项的来源、修改入口（渠道/订阅各走本类路由）与只读原因；空投影下三项读到空列表；停用后页面与对象类消失、记录保留 |
| `tests/acceptance/NOTIFY-TC-5` | 凭据只有一个写入入口、只回读是否已设置、非 Owner 写入被拒绝；空凭据撤销注册；渠道写入失败不留凭据；各可观察面不含凭据值 |
| `tests/acceptance/KOSD-TC-155` | 打包一次、装载一次、卸载一次；贡献与声明一一对应；未声明保留语义的类按内核默认删除；保留数据进遗留清单；内核 kind 集合与能力基线不变；摘要不匹配拒绝装载 |
