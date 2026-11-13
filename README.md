# kos-notification

KOS 系统应用：把事件与状态变化按订阅投递到外部渠道，并为每次投递尝试保留一条记录。本仓是该产品的独立扩展包——声明渠道、订阅与投递记录三个对象类和一个页面，经 kos-sdk 打包，以系统组件单独装载与卸载。

- 设计：`tsic/kos-design` 的 `kos-notification/`（范围、契约、验收、决策记录）。

## 贡献

| 贡献 | 声明 |
|---|---|
| 对象类 `Channel`、`Subscription`、`DeliveryRecord` | API 组 `notification.kos.tsic.top/v1`，集群作用域 |
| 控制器 `delivery` | 只写 `DeliveryRecord`；读核心组 `events` 与来源对象的状态条件 |
| 页面 `notification` | 渠道、订阅、投递记录三处展示项 |
| 对象角色 | 渠道与订阅：Owner 可写；投递记录：声明面只认会话角色，未声明写者（内核当前按 Owner 可写放行），本产品的写路径拒绝任何用户写记录 |
| 卸载保留 | 三个对象类均声明保留（`retention` 逐类 `keep`） |

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

装载由创建系统组件发起：`spec.package.reference` 指向归档，`spec.package.digest` 是打包打印的摘要。内核按 `KOSD-C-17` 校验摘要、读声明并激活贡献；停用按 `KOSD-C-28` 撤下页面与对象类并保留对象；卸载按 `KOSD-C-29` 的声明保留数据并列入遗留清单。本仓的验收把打包产物交给内核的包读取路径，核对摘要、声明解析与逐类装载（`tests/acceptance/KOSD-TC-155`）。

## 投递

控制器 `delivery` 每轮（`KOSD-C-06` 的单轮）读取已建立订阅、运行内核最近一小时的事件（`KOSD-C-64`）与来源对象的状态条件：

- 事件按来源对象与原因匹配；订阅建立之前的事件与状态变化不补发。
- 状态条件出现或取值变化时触发一次；同一观察状态重复调和不再触发。去重判据是可重放的：投递身份由条件值连同最后跃迁时刻构成，重复读取得到同一身份，跃迁（包括回退）得到新身份。
- 每条订阅的目标渠道各自投递；渠道不可达或未设置凭据都记为失败并给出原因，不影响同一订阅的其他渠道，也不影响其他订阅。
- 每次尝试写一条 `DeliveryRecord`（目标渠道、来源对象、尝试序号、结果与失败原因）；失败按有界重试（默认三次），到上限后保留最后一次原因并停止。记录集合按投递分组可读出尝试次数与终态。
- 投递请求体是 JSON：订阅名、渠道名、来源对象、触发类型（`Event` / `State`）、事件原因与发生时刻。记录由控制器写入；用户不能修改或删除，页面不提供入口。
- 控制器只写投递记录（声明中的 `manages` 与只开本扩展 API 组的 `scope`），不写内核对象，不建事件存储。

## 记录读取与保留

投递记录按来源的可见性读取，来源消失后仍保留（`NOTIFY-C-6`）。读取入口是 `RecordReader.Read`：

- 来源是应用时按该应用的可见性判定：Owner 恒可读；Member 在 `spec.visibility.members` 列名时可读；Guest 在 `spec.visibility.allowGuests` 为真时可读。
- 来源是非应用对象时只对 Owner 与拥有该对象的 Member 可读，Guest 不可读。
- 请求方看不到来源时读取被拒绝并给出原因（`RecordNotVisible`），不以空结果表示无记录。
- 来源对象与订阅被删除后记录保留并可读，返回值里的 `sourceExists` / `subscriptionExists` 标出它们已不存在。记录只保存来源的身份值，不持有阻塞式引用，所以来源删除不被记录挡住。
- 停用后记录保留、对象类不再被服务，因而没有新增记录；重新启用后恢复新增，已有记录不变。卸载按逐类保留声明保留三个对象类并把记录列入遗留清单。

## 跨仓接口落差（上报，不在本仓修补）

1. **记录写权限**：声明面的写者词表只有会话角色，且空的写者集合在打包序列化时被省略，所以「该类没有任何用户角色可写」（`NOTIFY-C-3`）在清单里无法表达。内核把未登记写者的扩展类按 `KOSD-C-59` 默认放行 Owner（`pkg/extension/registry.go`、`pkg/access/policy.go`）。本产品的写入路径仍拒绝任何用户写记录（`AllowWrite`），内核侧的强制需另行补齐。
2. **页面写入口的产品校验**：页面写入口走通用对象路由 `/api/v1/resources/<Kind>`，内核只校验对象身份、不解释扩展 spec（`pkg/controlplane/objects.go` 的 `normalizeExtension`），也没有调用产品写路径的钩子；`configuration.schema.channels` 同样没有框架消费者（`pkg/pages/form.go` 的 `FormOf` 读的是应用 `ConfigField`）。因此「不支持的渠道类型在创建与修改时被拒绝」与「凭据随渠道配置写入」在产品声明面上没有强制点，校验目前只在 `Writers.WriteChannel` / `Writers.WriteSubscription` 生效。
3. **记录读取的产品判定**：通用对象路由的读（`pkg/controlplane/server.go` 的 `handleGet` / `handleList`）返回存储中的扩展对象，不调用产品的读判定，也没有按 `KOSD-C-59` / `KOSD-C-64` 过滤扩展对象的钩子。记录可见性与「来源已不存在」标记因此只在 `RecordReader.Read` 生效；内核侧的强制需另行补齐。

## 测试

```sh
go build ./... && go vet ./... && go test ./...
```

`tests/acceptance/harness` 是按 `KOSD-C-17`/`C-28`/`C-29` 规则搭的 fixture，验证声明的贡献与启停、保留语义；它不是安装通道。`kosd` 只作为测试依赖，用来核对页面形状、框架版本范围，以及内核的包读取、声明解析与逐类装载。

| 用例 | 判据 |
|---|---|
| `tests/acceptance/NOTIFY-TC-1` | 一次匹配事件在配置的渠道上留下每次尝试一条记录（目标渠道、来源对象、尝试序号、结果与失败原因齐全）；记录集合读出尝试次数与终态；重复读取同一事件不重复投递；用户改写与删除记录被拒绝 |
| `tests/acceptance/NOTIFY-TC-2` | 匹配事件产生一条记录、不匹配事件不产生；订阅建立前与一小时窗口外的事件不补发；状态条件跃迁各触发一次，重复调和无新增；删除订阅后不再新增；停用组件撤下控制器 |
| `tests/acceptance/NOTIFY-TC-3` | 创建入口的类型列表与写入接受的一致；不支持的类型被拒绝并给出原因；订阅目标渠道集合可读；页面三类展示项的来源、修改入口（渠道/订阅各走本类路由）与只读原因；空投影下三项读到空列表；停用后页面与对象类消失、记录保留 |
| `tests/acceptance/NOTIFY-TC-4` | 同一订阅的可用渠道有成功记录；不可达与未设置凭据的渠道各按有界重试后停在终态并保留原因；另一条订阅不受影响 |
| `tests/acceptance/NOTIFY-TC-5` | 凭据只有一个写入入口、只回读是否已设置、非 Owner 写入被拒绝；空凭据撤销注册；渠道写入失败不留凭据；各可观察面不含凭据值 |
| `tests/acceptance/NOTIFY-TC-6` | 同一条记录：Owner 与应用可见成员可读、不拥有来源的 Member 与 Guest 被拒绝并给出原因；来源应用删除成功而记录仍可读并标出来源已不存在；订阅删除后记录仍可读并标出；停用后无新增、重新启用恢复新增且已有记录不变；卸载后记录进遗留清单 |
| `tests/acceptance/KOSD-TC-155` | 打包一次、装载一次、卸载一次；贡献与声明一一对应；三个对象类均按声明保留、保留数据进遗留清单；内核 kind 集合与能力基线不变；内核包路径接受该产物并逐类装载无问题；摘要不匹配拒绝装载 |
